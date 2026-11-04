package service_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"tg-podcastotron/mediary/mediarymocks"
	"tg-podcastotron/service"
	"tg-podcastotron/service/servicemocks"
)

// mp3WithCover returns the start of an mp3 file with an ID3v2.4 tag that has a front cover.
func mp3WithCover(cover []byte) []byte {
	body := []byte{0}
	body = append(body, "image/jpeg"...)
	body = append(body, 0, 3, 0) // front cover, empty description
	body = append(body, cover...)
	n := len(body)
	frame := append([]byte("APIC"), byte(n>>21&0x7f), byte(n>>14&0x7f), byte(n>>7&0x7f), byte(n&0x7f), 0, 0)
	frame = append(frame, body...)
	n = len(frame)
	tag := append([]byte{'I', 'D', '3', 4, 0, 0}, byte(n>>21&0x7f), byte(n>>14&0x7f), byte(n>>7&0x7f), byte(n&0x7f))
	tag = append(tag, frame...)
	return append(tag, bytes.Repeat([]byte{0xFF, 0xFB, 0x90, 0x00}, 1000)...)
}

func TestRunCoverBackfill(t *testing.T) {
	ctx := context.Background()
	cover := []byte{0xFF, 0xD8, 0xFF, 0xE0, 'c', 'o', 'v', 'e', 'r'}
	files := map[string][]byte{
		"/with-cover.mp3": mp3WithCover(cover),
		"/no-tag.mp3":     bytes.Repeat([]byte{0xFF, 0xFB, 0x90, 0x00}, 1000),
	}
	var mu sync.Mutex
	var ranges []string
	files3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		ranges = append(ranges, r.Header.Get("Range"))
		mu.Unlock()
		b, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		http.ServeContent(w, r, r.URL.Path, time.Time{}, bytes.NewReader(b))
	}))
	defer files3.Close()

	s3 := &servicemocks.MockS3Store{}
	svc, repo := newGCTestService(t, s3)
	const userID = "user-1"
	add := func(id, file string) {
		ep := saveEpisode(t, repo, userID, id, "episodes/p-"+userID+"/"+id+".mp3", service.EpisodeStatusComplete)
		ep.URL = files3.URL + file
		if _, err := repo.SaveEpisode(ctx, ep); err != nil {
			t.Fatal(err)
		}
	}
	add("1", "/with-cover.mp3")
	add("2", "/no-tag.mp3")
	add("3", "/missing.mp3")
	saveEpisode(t, repo, userID, "4", "episodes/p-"+userID+"/4.mp3", service.EpisodeStatusDownloading)

	svc.RunCoverBackfill(ctx)

	episodes := must(svc.GetEpisodesMap(ctx, userID, []string{"1", "2", "3"}))(t)
	ep1 := episodes["1"]
	if !strings.HasSuffix(ep1.CoverID, ".jpg") || ep1.CoverPath() != "/covers/"+ep1.CoverID {
		t.Fatalf("episode 1 cover = %q, %q", ep1.CoverID, ep1.CoverPath())
	}
	if c := must(svc.GetCover(ctx, ep1.CoverID))(t); c == nil || c.MIME != "image/jpeg" || !bytes.Equal(c.Data, cover) {
		t.Errorf("GetCover() = %+v, want the cover of episode 1", c)
	}
	for _, id := range []string{"2", "3"} {
		if ep := episodes[id]; ep.CoverID != "" {
			t.Errorf("episode %s cover = %q, want none", id, ep.CoverID)
		}
	}
	// The app read only the start of the files.
	for _, r := range ranges {
		if !strings.HasPrefix(r, "bytes=0-") {
			t.Errorf("request without a range: %q", r)
		}
	}

	// Each complete episode was checked, so the next backfill reads nothing.
	ranges = nil
	svc.RunCoverBackfill(ctx)
	if len(ranges) != 0 {
		t.Errorf("second backfill read %d files, want 0", len(ranges))
	}
}

func TestCoverIsDeletedWithTheEpisode(t *testing.T) {
	ctx := context.Background()
	s3 := &servicemocks.MockS3Store{DeleteFunc: func(ctx context.Context, key string) error { return nil }}
	svc, repo := newGCTestService(t, s3)
	saveEpisode(t, repo, "user-1", "1", "episodes/p-user-1/1.mp3", service.EpisodeStatusComplete)
	cover := &service.Cover{ID: "abc.jpg", MIME: "image/jpeg", Data: []byte{1, 2, 3}}
	if err := repo.SaveCover(ctx, cover); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetEpisodeCover(ctx, "user-1", "1", cover.ID); err != nil {
		t.Fatal(err)
	}

	if err := svc.DeleteEpisodes(ctx, "user-1", []string{"1"}); err != nil {
		t.Fatal(err)
	}
	if c := must(svc.GetCover(ctx, cover.ID))(t); c != nil {
		t.Errorf("cover after delete = %+v, want none", c)
	}
}

func TestDeleteEpisodesCancelsUnfinishedJobs(t *testing.T) {
	ctx := context.Background()
	var cancelled []string
	s3 := &servicemocks.MockS3Store{DeleteFunc: func(ctx context.Context, key string) error { return nil }}
	svc, repo := newGCTestServiceWithMediary(t, s3, &mediarymocks.ServiceMock{
		CancelJobFunc: func(ctx context.Context, jobID string) error {
			cancelled = append(cancelled, jobID)
			return nil
		},
	})
	for id, st := range map[string]service.EpisodeStatus{"1": service.EpisodeStatusDownloading, "2": service.EpisodeStatusComplete} {
		ep := saveEpisode(t, repo, "user-1", id, "episodes/p-user-1/"+id+".mp3", st)
		ep.MediaryID = "job-" + id
		if _, err := repo.SaveEpisode(ctx, ep); err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.DeleteEpisodes(ctx, "user-1", []string{"1", "2"}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(cancelled, ",") != "job-1" {
		t.Errorf("cancelled = %v, want only the job of the downloading episode", cancelled)
	}
}
