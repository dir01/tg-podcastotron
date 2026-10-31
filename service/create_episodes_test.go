package service_test

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	migrate "github.com/rubenv/sql-migrate"
	"tg-podcastotron/mediary"
	"tg-podcastotron/mediary/mediarymocks"
	"tg-podcastotron/service"
	jobsqueue "tg-podcastotron/service/jobs_queue"
	"tg-podcastotron/service/servicemocks"
)

// newQueueBackedService returns a started service backed by a file SQLite DB, so that
// queue workers and the repository can use separate connections like in production.
func newQueueBackedService(t *testing.T, mediarySvc mediary.Service) (*service.Service, service.Repository, <-chan []service.EpisodeStatusChange) {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelWarn}))

	dsn := "file:" + filepath.Join(t.TempDir(), "test.db") + "?_journal_mode=WAL&_busy_timeout=5000"
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrate.Exec(db, "sqlite3", &migrate.FileMigrationSource{Dir: "../db/migrations"}, migrate.Up); err != nil {
		t.Fatalf("failed to apply migrations: %v", err)
	}

	queue := must(jobsqueue.New(db, 2, logger))(t)
	s3Store := &servicemocks.MockS3Store{
		PreSignedURLFunc: func(key string) (string, error) { return "https://example.com/" + key + "?signed", nil },
		URLFunc:          func(key string) (string, error) { return "https://example.com/" + key, nil },
		DeleteFunc:       func(ctx context.Context, key string) error { return nil },
	}

	repo := service.NewSqliteRepository(db)
	svc := service.New(mediarySvc, repo, s3Store, queue, "", func(s string) string { return s }, logger, nil)

	ctx, cancel := context.WithCancel(context.Background())
	statusChanges := make(chan []service.EpisodeStatusChange, 100)
	svcChan := svc.Start(ctx)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case c := <-svcChan:
				statusChanges <- c
			}
		}
	}()
	t.Cleanup(func() {
		cancel()
		queue.Shutdown()
		_ = db.Close()
	})

	return svc, repo, statusChanges
}

// A variant whose upload fails must be retried on its own: every variant gets exactly
// one episode row and exactly one mediary upload job, and they all reach polling.
func TestCreateEpisodesAsync_FailingVariantIsRetriedWithoutDuplicatingOthers(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
	defer cancel()

	variantsPerEpisode := [][]string{{"ch1.mp3"}, {"ch2.mp3"}, {"ch3.mp3"}, {"ch4.mp3"}}
	const failingVariant = "ch3.mp3"
	const failuresBeforeSuccess = 2

	var mu sync.Mutex
	failuresLeft := failuresBeforeSuccess
	uploadURLsByVariant := map[string][]string{} // successful CreateUploadJob calls
	mediaryIDByUploadURL := map[string]string{}

	mediaryMock := &mediarymocks.ServiceMock{
		FetchMetadataLongPollingFunc: func(ctx context.Context, mediaURL string) (*mediary.Metadata, error) {
			return &mediary.Metadata{URL: mediaURL, DownloaderName: "torrent"}, nil
		},
		CreateUploadJobFunc: func(ctx context.Context, params *mediary.CreateUploadJobParams) (string, error) {
			p := params.Params.(mediary.ConcatenateJobParams)
			variant := p.Variants[0]

			mu.Lock()
			defer mu.Unlock()
			if variant == failingVariant && failuresLeft > 0 {
				failuresLeft--
				return "", errors.New("mediary is unavailable")
			}
			uploadURLsByVariant[variant] = append(uploadURLsByVariant[variant], p.UploadURL)
			id := "job-" + variant
			mediaryIDByUploadURL[p.UploadURL] = id
			return id, nil
		},
		FetchJobStatusMapFunc: func(ctx context.Context, jobIDs []string) (map[string]*mediary.JobStatus, error) {
			result := make(map[string]*mediary.JobStatus, len(jobIDs))
			for _, id := range jobIDs {
				result[id] = &mediary.JobStatus{Id: id, Status: mediary.JobStatusComplete, ResultFileBytes: 42}
			}
			return result, nil
		},
	}

	svc, repo, _ := newQueueBackedService(t, mediaryMock)
	userID := "user-1"

	if err := svc.CreateEpisodesAsync(ctx, userID, "magnet:?xt=urn:btih:abc&dn=Book", variantsPerEpisode, service.ProcessingTypeConcatenate); err != nil {
		t.Fatal(err)
	}

	// Wait until every episode is complete, i.e. the failing variant was retried and polled too.
	var episodes []*service.Episode
	for {
		var err error
		episodes, err = repo.ListUserEpisodes(ctx, userID)
		if err != nil {
			t.Fatal(err)
		}
		done := len(episodes) >= len(variantsPerEpisode)
		for _, ep := range episodes {
			done = done && ep.Status == service.EpisodeStatusComplete
		}
		if done {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("episodes did not complete: %+v", episodes)
		case <-time.After(100 * time.Millisecond):
		}
	}

	if len(episodes) != len(variantsPerEpisode) {
		t.Fatalf("expected %d episodes, got %d", len(variantsPerEpisode), len(episodes))
	}

	mu.Lock()
	defer mu.Unlock()
	if failuresLeft != 0 {
		t.Fatalf("expected the failing variant to fail %d times, %d failures left", failuresBeforeSuccess, failuresLeft)
	}

	storageKeys := map[string]bool{}
	for _, ep := range episodes {
		if storageKeys[ep.StorageKey] {
			t.Fatalf("storage key %q is used by more than one episode", ep.StorageKey)
		}
		storageKeys[ep.StorageKey] = true
	}

	for _, variants := range variantsPerEpisode {
		urls := uploadURLsByVariant[variants[0]]
		if len(urls) != 1 {
			t.Fatalf("expected exactly one upload for variant %s, got %d: %v", variants[0], len(urls), urls)
		}
	}

	// Every upload goes to the storage key of the episode that owns it.
	for _, ep := range episodes {
		if mediaryIDByUploadURL["https://example.com/"+ep.StorageKey+"?signed"] != ep.MediaryID {
			t.Fatalf("episode %s (key %s) is not owned by the upload that produced its file", ep.ID, ep.StorageKey)
		}
	}
}

// Re-running a create request (as a queue retry does) must not create new episodes or uploads.
func TestCreateEpisodesAsync_SameRequestTwiceIsIdempotent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
	defer cancel()

	var mu sync.Mutex
	uploads := 0
	mediaryMock := &mediarymocks.ServiceMock{
		FetchMetadataLongPollingFunc: func(ctx context.Context, mediaURL string) (*mediary.Metadata, error) {
			return &mediary.Metadata{URL: mediaURL, DownloaderName: "torrent"}, nil
		},
		CreateUploadJobFunc: func(ctx context.Context, params *mediary.CreateUploadJobParams) (string, error) {
			mu.Lock()
			defer mu.Unlock()
			uploads++
			return params.Params.(mediary.ConcatenateJobParams).UploadURL, nil
		},
		FetchJobStatusMapFunc: func(ctx context.Context, jobIDs []string) (map[string]*mediary.JobStatus, error) {
			return map[string]*mediary.JobStatus{}, errors.New("not polled in this test")
		},
	}

	svc, repo, statusChanges := newQueueBackedService(t, mediaryMock)
	userID := "user-1"
	variants := [][]string{{"a.mp3"}, {"b.mp3"}}

	// Two separate requests for the same content: separate episodes and separate files.
	for i := 0; i < 2; i++ {
		if err := svc.CreateEpisodesAsync(ctx, userID, "magnet:?xt=urn:btih:abc", variants, service.ProcessingTypeConcatenate); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		select {
		case <-statusChanges:
		case <-ctx.Done():
			t.Fatal("episodes were not created")
		}
	}

	waitFor(ctx, t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return uploads == 4
	})

	episodes := must(repo.ListUserEpisodes(ctx, userID))(t)
	if len(episodes) != 4 {
		t.Fatalf("expected 4 episodes, got %d", len(episodes))
	}
	keys := map[string]bool{}
	for _, ep := range episodes {
		keys[ep.StorageKey] = true
	}
	if len(keys) != 4 {
		t.Fatalf("expected 4 distinct storage keys, got %d", len(keys))
	}

	time.Sleep(3 * time.Second) // give any stray duplicate jobs a chance to run
	mu.Lock()
	defer mu.Unlock()
	if uploads != 4 {
		t.Fatalf("expected 4 uploads, got %d", uploads)
	}
}

func waitFor(ctx context.Context, t *testing.T, cond func() bool) {
	t.Helper()
	for !cond() {
		select {
		case <-ctx.Done():
			t.Fatal("condition was not met in time")
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// An episode deleted while its upload job is created must stay deleted, and the job
// must be cancelled.
func TestCreateEpisode_DeletedDuringUploadStartIsNotRecreated(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
	defer cancel()

	userID := "user-1"
	var repo service.Repository
	var cancelledJobs []string
	mediaryMock := &mediarymocks.ServiceMock{
		FetchMetadataLongPollingFunc: func(ctx context.Context, mediaURL string) (*mediary.Metadata, error) {
			return &mediary.Metadata{URL: mediaURL, DownloaderName: "torrent"}, nil
		},
		CreateUploadJobFunc: func(ctx context.Context, params *mediary.CreateUploadJobParams) (string, error) {
			episodes, err := repo.ListUserEpisodes(ctx, userID)
			if err != nil {
				return "", err
			}
			for _, ep := range episodes {
				if err := repo.DeleteEpisodes(ctx, userID, []string{ep.ID}); err != nil {
					return "", err
				}
			}
			return "job-1", nil
		},
		CancelJobFunc: func(ctx context.Context, jobID string) error {
			cancelledJobs = append(cancelledJobs, jobID)
			return nil
		},
	}

	svc, r, _ := newQueueBackedService(t, mediaryMock)
	repo = r

	if _, err := svc.CreateEpisode(ctx, userID, "magnet:?xt=urn:btih:abc&dn=Book", []string{"ch1.mp3"}, service.ProcessingTypeConcatenate); err == nil {
		t.Fatal("expected an error for an episode deleted during upload start")
	}

	episodes, err := repo.ListUserEpisodes(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if len(episodes) != 0 {
		t.Fatalf("expected the deleted episode to stay deleted, got %d episodes", len(episodes))
	}
	if len(cancelledJobs) != 1 || cancelledJobs[0] != "job-1" {
		t.Fatalf("expected job-1 to be cancelled, got %v", cancelledJobs)
	}
}
