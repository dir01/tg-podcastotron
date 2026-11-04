package tgminiapp

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"tg-podcastotron/service"
)

// episodeView is an episode with the data that the templates show.
type episodeView struct {
	*service.Episode
	Stage     stage
	Steps     []step
	Feeds     []*service.Feed
	ExpiresAt time.Time // zero if the episode is in a permanent feed
	// LastError is the last failure reason. Only retrying and failed episodes have it.
	LastError string
	Attempt   string // "2 of 6" while an attempt after the first runs
}

func (v *episodeView) Expires() bool { return !v.ExpiresAt.IsZero() }

// feedIndex has the feeds of a user by ID, in the order of the feeds list.
type feedIndex struct {
	list []*service.Feed
	byID map[string]*service.Feed
}

func (s *Server) feedIndex(ctx context.Context, userID string) (*feedIndex, error) {
	feeds, err := s.svc.ListFeeds(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to list feeds: %w", err)
	}
	slices.SortStableFunc(feeds, func(a, b *service.Feed) int { return service.CompareEpisodeIDs(a.ID, b.ID) })
	idx := &feedIndex{list: feeds, byID: make(map[string]*service.Feed, len(feeds))}
	for _, f := range feeds {
		idx.byID[f.ID] = f
	}
	return idx, nil
}

func (idx *feedIndex) of(ep *service.Episode) []*service.Feed {
	var feeds []*service.Feed
	for _, f := range idx.list {
		if slices.Contains(ep.FeedIDs, f.ID) {
			feeds = append(feeds, f)
		}
	}
	return feeds
}

// episodeViews makes the views of the episodes. It reads the status history only
// for retrying and failed episodes, to show the reason and the last stage.
func (s *Server) episodeViews(ctx context.Context, userID string, episodes []*service.Episode, feeds *feedIndex) ([]*episodeView, error) {
	views := make([]*episodeView, len(episodes))
	for i, ep := range episodes {
		var events []*service.EpisodeEvent
		if ep.Status == service.EpisodeStatusRetrying || ep.Status == service.EpisodeStatusFailed {
			var err error
			if events, err = s.svc.ListEpisodeEvents(ctx, userID, ep.ID); err != nil {
				return nil, err
			}
		}
		views[i] = newEpisodeView(ep, events, feeds)
	}
	return views, nil
}

func newEpisodeView(ep *service.Episode, events []*service.EpisodeEvent, feeds *feedIndex) *episodeView {
	v := &episodeView{
		Episode: ep,
		Stage:   stageOf(ep.Status),
		Steps:   stepsOf(ep, events),
		Feeds:   feeds.of(ep),
	}
	if at, ok := service.ExpiresAt(ep, feeds.byID); ok {
		v.ExpiresAt = at
	}
	if !ep.Status.IsFinal() || ep.Status == service.EpisodeStatusFailed {
		for i := len(events) - 1; i >= 0; i-- {
			if events[i].Detail != "" {
				v.LastError = strings.TrimSpace(events[i].Detail)
				break
			}
		}
	}
	if n := len(events); n > 0 && !ep.Status.IsFinal() && events[n-1].Attempt > 1 {
		v.Attempt = attemptText(events[n-1].Attempt, events[n-1].MaxAttempts)
	}
	return v
}

// sortNewestFirst sorts episodes by creation time, newest first.
func sortNewestFirst(episodes []*service.Episode) {
	slices.SortStableFunc(episodes, func(a, b *service.Episode) int {
		if c := b.CreatedAt.Compare(a.CreatedAt); c != 0 {
			return c
		}
		return service.CompareEpisodeIDs(b.ID, a.ID)
	})
}
