package service_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"tg-podcastotron/mediary"
	"tg-podcastotron/mediary/mediarymocks"
	"tg-podcastotron/service"
	"tg-podcastotron/service/servicemocks"
)

func completingMediary() *mediarymocks.ServiceMock {
	return &mediarymocks.ServiceMock{
		FetchMetadataLongPollingFunc: func(ctx context.Context, mediaURL string) (*mediary.Metadata, error) {
			return &mediary.Metadata{URL: mediaURL, DownloaderName: "torrent"}, nil
		},
		CreateUploadJobFunc: func(ctx context.Context, params *mediary.CreateUploadJobParams) (string, error) {
			return "job-" + params.Params.(mediary.UploadOriginalJobParams).Variant, nil
		},
		FetchJobStatusMapFunc: func(ctx context.Context, jobIDs []string) (map[string]*mediary.JobStatus, error) {
			result := make(map[string]*mediary.JobStatus, len(jobIDs))
			for _, id := range jobIDs {
				result[id] = &mediary.JobStatus{Id: id, Status: mediary.JobStatusComplete, ResultFileBytes: 42}
			}
			return result, nil
		},
	}
}

// waitForRequest waits until the request has n finished episodes.
func waitForRequest(ctx context.Context, t *testing.T, svc *service.Service, userID, requestID string, n int) *service.Request {
	t.Helper()
	for {
		req, err := svc.GetRequest(ctx, userID, requestID)
		if err != nil {
			t.Fatal(err)
		}
		if req != nil && len(req.Episodes) == n && req.Finished() {
			return req
		}
		select {
		case <-ctx.Done():
			t.Fatalf("request did not finish: %+v", req)
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func TestCreateEpisodesAsync_AddsEpisodesToTheChosenFeeds(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	svc, _, _ := newQueueBackedService(t, completingMediary())
	const userID = "user-1"

	books := must(svc.CreateFeed(ctx, userID, "Books"))(t)
	// A feed that does not exist (for example, it was deleted after the form opened) is skipped.
	requestID := must(svc.CreateEpisodesAsync(ctx, userID, "magnet:?xt=urn:btih:abc&dn=Book",
		[][]string{{"1.mp3"}, {"2.mp3"}}, service.ProcessingTypeUploadOriginal, []string{books.ID, "999"}))(t)

	req := waitForRequest(ctx, t, svc, userID, requestID, 2)
	for _, ep := range req.Episodes {
		if !slices.Equal(ep.FeedIDs, []string{books.ID}) {
			t.Errorf("episode %s feeds = %v, want [%s]", ep.ID, ep.FeedIDs, books.ID)
		}
		events := must(svc.ListEpisodeEvents(ctx, userID, ep.ID))(t)
		if len(events) < 2 || events[0].Status != service.EpisodeStatusCreated || events[len(events)-1].Status != service.EpisodeStatusComplete {
			t.Errorf("episode %s history = %+v, want created ... complete", ep.ID, events)
		}
	}

	// Without feeds, the episodes go to the default feed.
	requestID = must(svc.CreateEpisodesAsync(ctx, userID, "magnet:?xt=urn:btih:def",
		[][]string{{"3.mp3"}}, service.ProcessingTypeUploadOriginal, nil))(t)
	req = waitForRequest(ctx, t, svc, userID, requestID, 1)
	if got := req.Episodes[0].FeedIDs; !slices.Equal(got, []string{service.DefaultFeedID}) {
		t.Errorf("feeds without a choice = %v, want the default feed", got)
	}

	if requests := must(svc.ListRequests(ctx, userID))(t); len(requests) != 2 || requests[0].ID != requestID {
		t.Errorf("ListRequests() = %d requests, want 2 with the newest first", len(requests))
	}
}

func TestAddAndRemoveEpisodesFromFeeds(t *testing.T) {
	ctx := context.Background()
	s3 := &servicemocks.MockS3Store{URLFunc: func(key string) (string, error) { return "https://example.com/" + key, nil }}
	svc, repo := newGCTestService(t, s3)
	const userID = "user-1"

	a := must(svc.CreateFeed(ctx, userID, "A"))(t)
	b := must(svc.CreateFeed(ctx, userID, "B"))(t)
	saveEpisode(t, repo, userID, "1", "k1", service.EpisodeStatusComplete)
	saveEpisode(t, repo, userID, "2", "k2", service.EpisodeStatusComplete)

	feedsOf := func(id string) []string {
		t.Helper()
		feeds := must(svc.GetEpisodesMap(ctx, userID, []string{id}))(t)[id].FeedIDs
		slices.Sort(feeds)
		return feeds
	}

	if err := svc.AddEpisodesToFeeds(ctx, userID, []string{"1", "2"}, []string{a.ID}); err != nil {
		t.Fatal(err)
	}
	if err := svc.AddEpisodesToFeeds(ctx, userID, []string{"1"}, []string{a.ID, b.ID}); err != nil {
		t.Fatal(err)
	}
	if got := feedsOf("1"); !slices.Equal(got, []string{a.ID, b.ID}) {
		t.Fatalf("episode 1 feeds = %v, want both", got)
	}

	// Remove keeps the other feeds.
	if err := svc.RemoveEpisodesFromFeeds(ctx, userID, []string{"1", "2"}, []string{a.ID}); err != nil {
		t.Fatal(err)
	}
	if got := feedsOf("1"); !slices.Equal(got, []string{b.ID}) {
		t.Errorf("episode 1 feeds = %v, want [%s]", got, b.ID)
	}
	if got := feedsOf("2"); len(got) != 0 {
		t.Errorf("episode 2 feeds = %v, want none", got)
	}

	// The episodes and the feeds of other users cannot be used.
	if err := svc.AddEpisodesToFeeds(ctx, "user-2", []string{"1"}, []string{a.ID}); !errors.Is(err, service.ErrEpisodeNotFound) {
		t.Errorf("add as another user: error = %v, want ErrEpisodeNotFound", err)
	}
	if err := svc.AddEpisodesToFeeds(ctx, userID, []string{"1"}, []string{"999"}); !errors.Is(err, service.ErrFeedNotFound) {
		t.Errorf("add to unknown feed: error = %v, want ErrFeedNotFound", err)
	}
}

func TestSourceLinks(t *testing.T) {
	ctx := context.Background()
	svc, _ := newGCTestService(t, &servicemocks.MockS3Store{})

	link := must(svc.SaveSourceLink(ctx, "user-1", "  magnet:?xt=urn:btih:abc  "))(t)
	if link.URL != "magnet:?xt=urn:btih:abc" || link.ID == "" {
		t.Fatalf("SaveSourceLink() = %+v", link)
	}
	if got := must(svc.GetSourceLink(ctx, "user-1", link.ID))(t); got == nil || got.URL != link.URL {
		t.Errorf("GetSourceLink() = %+v", got)
	}
	if got := must(svc.GetSourceLink(ctx, "user-2", link.ID))(t); got != nil {
		t.Errorf("another user got the link: %+v", got)
	}
}

func TestDeleteEpisodesDeletesTheStatusHistory(t *testing.T) {
	ctx := context.Background()
	s3 := &servicemocks.MockS3Store{DeleteFunc: func(ctx context.Context, key string) error { return nil }}
	svc, repo := newGCTestService(t, s3)
	ep := saveEpisode(t, repo, "user-1", "1", "k1", service.EpisodeStatusComplete)
	if err := repo.InsertEpisodeEvents(ctx, []*service.EpisodeEvent{{UserID: ep.UserID, EpisodeID: ep.ID, At: time.Now(), Status: service.EpisodeStatusComplete}}); err != nil {
		t.Fatal(err)
	}

	if err := svc.DeleteEpisodes(ctx, ep.UserID, []string{ep.ID}); err != nil {
		t.Fatal(err)
	}
	if events := must(svc.ListEpisodeEvents(ctx, ep.UserID, ep.ID))(t); len(events) != 0 {
		t.Errorf("history after delete = %+v, want none", events)
	}
}

func TestGroupRequests(t *testing.T) {
	now := time.Now()
	ep := func(id, key string, created time.Time) *service.Episode {
		return &service.Episode{ID: id, CreationKey: key, CreatedAt: created, SourceURL: "magnet:?dn=" + key[:1]}
	}
	requests := service.GroupRequests([]*service.Episode{
		ep("10", "a/1", now.Add(-time.Hour)),
		ep("9", "a/0", now.Add(-time.Hour)),
		ep("11", "b/0", now),
		{ID: "1"}, // an old episode without a request
	})
	if len(requests) != 2 || requests[0].ID != "b" || requests[1].ID != "a" {
		t.Fatalf("requests = %+v, want b, a", requests)
	}
	if ids := []string{requests[1].Episodes[0].ID, requests[1].Episodes[1].ID}; !slices.Equal(ids, []string{"9", "10"}) {
		t.Errorf("episodes of a = %v, want [9 10]", ids)
	}
}
