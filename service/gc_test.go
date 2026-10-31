package service_test

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	migrate "github.com/rubenv/sql-migrate"
	"tg-podcastotron/mediary/mediarymocks"
	"tg-podcastotron/service"
	jobsqueue "tg-podcastotron/service/jobs_queue"
	"tg-podcastotron/service/servicemocks"
)

func newGCTestService(t *testing.T, s3 *servicemocks.MockS3Store) (*service.Service, service.Repository) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelWarn}))
	db, err := sql.Open("sqlite3", "file:"+filepath.Join(t.TempDir(), "test.db")+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := migrate.Exec(db, "sqlite3", &migrate.FileMigrationSource{Dir: "../db/migrations"}, migrate.Up); err != nil {
		t.Fatal(err)
	}
	queue := must(jobsqueue.New(db, 1, logger))(t)
	t.Cleanup(queue.Shutdown)
	repo := service.NewSqliteRepository(db)
	return service.New(&mediarymocks.ServiceMock{}, repo, s3, queue, "", func(s string) string { return "p-" + s }, logger, nil), repo
}

func saveEpisode(t *testing.T, repo service.Repository, userID, id, storageKey string, status service.EpisodeStatus) *service.Episode {
	t.Helper()
	now := time.Now().UTC()
	return must(repo.SaveEpisode(context.Background(), &service.Episode{
		ID: id, UserID: userID, Title: "ep " + id, CreatedAt: now, UpdatedAt: now,
		URL: "https://example.com/" + storageKey, StorageKey: storageKey, Status: status,
	}))(t)
}

func TestScanAndDeleteStorageGarbage(t *testing.T) {
	ctx := context.Background()
	old := time.Now().Add(-48 * time.Hour)
	fresh := time.Now().Add(-1 * time.Hour)

	objects := []service.S3Object{
		{Key: "episodes/p-u1/owned.mp3", Size: 10, LastModified: old},
		{Key: "episodes/p-u1/orphan-1.mp3", Size: 100, LastModified: old.Add(-time.Hour)},
		{Key: "episodes/p-u1/orphan-2.mp3", Size: 200, LastModified: old},
		{Key: "episodes/p-unknown/orphan-3.mp3", Size: 1000, LastModified: old},
		{Key: "episodes/p-u1/in-flight.mp3", Size: 5, LastModified: fresh}, // within grace period
		{Key: "episodes/p-u1/nested/odd.mp3", Size: 7, LastModified: old},  // unexpected shape
		{Key: "episodes/p-u1/failed.mp3", Size: 3, LastModified: old},      // owned by a failed episode
	}
	var listedPrefixes []string
	var deleted [][]string
	s3 := &servicemocks.MockS3Store{
		ListFunc: func(ctx context.Context, prefix string) ([]service.S3Object, error) {
			listedPrefixes = append(listedPrefixes, prefix)
			return objects, nil
		},
		DeleteManyFunc: func(ctx context.Context, keys []string) error {
			deleted = append(deleted, keys)
			return nil
		},
	}
	svc, repo := newGCTestService(t, s3)

	saveEpisode(t, repo, "u1", "1", "episodes/p-u1/owned.mp3", service.EpisodeStatusComplete)
	saveEpisode(t, repo, "u1", "2", "episodes/p-u1/failed.mp3", service.EpisodeStatusFailed)
	missing := saveEpisode(t, repo, "u1", "3", "episodes/p-u1/gone.mp3", service.EpisodeStatusComplete)
	saveEpisode(t, repo, "u1", "4", "episodes/p-u1/not-yet.mp3", service.EpisodeStatusCreated) // no file yet: not "missing"
	if err := repo.BulkInsertPublications(ctx, []*service.Publication{{UserID: "u1", FeedID: "1", EpisodeID: missing.ID, CreatedAt: time.Now()}}); err != nil {
		t.Fatal(err)
	}

	report := must(svc.ScanStorageGarbage(ctx))(t)

	var garbageKeys []string
	for _, o := range report.Garbage {
		garbageKeys = append(garbageKeys, o.Key)
	}
	sort.Strings(garbageKeys)
	want := []string{"episodes/p-u1/orphan-1.mp3", "episodes/p-u1/orphan-2.mp3", "episodes/p-unknown/orphan-3.mp3"}
	if len(garbageKeys) != len(want) {
		t.Fatalf("expected garbage %v, got %v", want, garbageKeys)
	}
	for i := range want {
		if garbageKeys[i] != want[i] {
			t.Fatalf("expected garbage %v, got %v", want, garbageKeys)
		}
	}
	if report.GarbageBytes != 1300 {
		t.Fatalf("expected 1300 garbage bytes, got %d", report.GarbageBytes)
	}
	if len(report.ByOwner) != 2 || report.ByOwner[0].KeyPrefix != "p-unknown" || report.ByOwner[0].UserID != "" ||
		report.ByOwner[1].UserID != "u1" || report.ByOwner[1].Count != 2 || report.ByOwner[1].Bytes != 300 ||
		!report.ByOwner[1].Oldest.Equal(old.Add(-time.Hour)) || !report.ByOwner[1].Newest.Equal(old) {
		t.Fatalf("unexpected per-owner stats: %+v", report.ByOwner)
	}
	if len(report.Missing) != 1 || report.Missing[0].Episode.ID != missing.ID || len(report.Missing[0].FeedIDs) != 1 {
		t.Fatalf("expected episode %s to be reported missing with its feed, got %+v", missing.ID, report.Missing)
	}
	for _, p := range listedPrefixes {
		if p != "episodes/" {
			t.Fatalf("gc must only list under episodes/, listed %q", p)
		}
	}

	// Between the scan and the confirmation, orphan-2 gains an owner: it must survive.
	saveEpisode(t, repo, "u1", "5", "episodes/p-u1/orphan-2.mp3", service.EpisodeStatusComplete)
	n, bytes, err := svc.DeleteStorageGarbage(ctx, append(garbageKeys, "episodes/p-u1/owned.mp3", "feeds/p-u1/1"))
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 || bytes != 1100 {
		t.Fatalf("expected 2 files / 1100 bytes deleted, got %d / %d", n, bytes)
	}
	if len(deleted) != 1 || len(deleted[0]) != 2 ||
		deleted[0][0] != "episodes/p-u1/orphan-1.mp3" || deleted[0][1] != "episodes/p-unknown/orphan-3.mp3" {
		t.Fatalf("unexpected deletions: %v", deleted)
	}
}

func TestScanStorageGarbageRefusesEmptyDatabase(t *testing.T) {
	s3 := &servicemocks.MockS3Store{
		ListFunc: func(ctx context.Context, prefix string) ([]service.S3Object, error) {
			return []service.S3Object{{Key: "episodes/p-u1/a.mp3", Size: 1, LastModified: time.Now().Add(-48 * time.Hour)}}, nil
		},
	}
	svc, _ := newGCTestService(t, s3)
	if _, err := svc.ScanStorageGarbage(context.Background()); err == nil {
		t.Fatal("expected scan against an empty database to be refused")
	}
}

// Episodes created while files were derived from content may share one file;
// deleting one of them must keep the file the other still uses.
func TestDeleteEpisodesKeepsSharedFile(t *testing.T) {
	ctx := context.Background()
	var deletedKeys []string
	s3 := &servicemocks.MockS3Store{
		DeleteFunc: func(ctx context.Context, key string) error {
			deletedKeys = append(deletedKeys, key)
			return nil
		},
	}
	svc, repo := newGCTestService(t, s3)

	saveEpisode(t, repo, "u1", "1", "episodes/p-u1/shared.mp3", service.EpisodeStatusComplete)
	saveEpisode(t, repo, "u1", "2", "episodes/p-u1/shared.mp3", service.EpisodeStatusComplete)

	if err := svc.DeleteEpisodes(ctx, "u1", []string{"1"}); err != nil {
		t.Fatal(err)
	}
	if len(deletedKeys) != 0 {
		t.Fatalf("shared file must be kept, deleted %v", deletedKeys)
	}

	if err := svc.DeleteEpisodes(ctx, "u1", []string{"2"}); err != nil {
		t.Fatal(err)
	}
	if len(deletedKeys) != 1 || deletedKeys[0] != "episodes/p-u1/shared.mp3" {
		t.Fatalf("expected the file to be deleted with its last episode, deleted %v", deletedKeys)
	}
}

// Legacy episodes can share one file: if it is missing, each complete episode is reported.
func TestScanStorageGarbageReportsEveryEpisodeOfMissingSharedFile(t *testing.T) {
	ctx := context.Background()
	s3 := &servicemocks.MockS3Store{
		ListFunc: func(ctx context.Context, prefix string) ([]service.S3Object, error) {
			return []service.S3Object{{Key: "episodes/p-u1/other.mp3", Size: 1, LastModified: time.Now()}}, nil
		},
	}
	svc, repo := newGCTestService(t, s3)

	saveEpisode(t, repo, "u1", "1", "episodes/p-u1/shared.mp3", service.EpisodeStatusComplete)
	saveEpisode(t, repo, "u1", "2", "episodes/p-u1/shared.mp3", service.EpisodeStatusComplete)
	saveEpisode(t, repo, "u1", "3", "episodes/p-u1/shared.mp3", service.EpisodeStatusCreated)

	report := must(svc.ScanStorageGarbage(ctx))(t)

	var ids []string
	for _, m := range report.Missing {
		ids = append(ids, m.Episode.ID)
	}
	sort.Strings(ids)
	if len(ids) != 2 || ids[0] != "1" || ids[1] != "2" {
		t.Fatalf("expected episodes 1 and 2 to be reported missing, got %v", ids)
	}
}
