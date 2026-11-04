package service

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"time"
)

// Request is the group of episodes that one create request made.
type Request struct {
	ID        string
	SourceURL string
	CreatedAt time.Time  // the creation time of the first episode
	Episodes  []*Episode // ordered by ID
}

// Title is the title of the source, for example the name of the torrent. A link
// without a title in it (for example, a video link) gets the title of its first episode.
func (r *Request) Title() string {
	if title := TitleFromSourceURL(r.SourceURL); title != "" {
		return title
	}
	if len(r.Episodes) > 0 {
		return r.Episodes[0].Title
	}
	return ""
}

// Finished tells if all episodes are complete or failed.
func (r *Request) Finished() bool {
	for _, ep := range r.Episodes {
		if !ep.Status.IsFinal() {
			return false
		}
	}
	return true
}

// Count returns the number of episodes with one of the statuses.
func (r *Request) Count(statuses ...EpisodeStatus) int {
	n := 0
	for _, ep := range r.Episodes {
		if slices.Contains(statuses, ep.Status) {
			n++
		}
	}
	return n
}

// IsFinal tells if the status does not change again.
func (s EpisodeStatus) IsFinal() bool {
	return s == EpisodeStatusComplete || s == EpisodeStatusFailed
}

// GetRequest returns the request, or nil if it has no episodes. The episodes of a
// request are created by a queued job, so a new request has no episodes for some time.
func (svc *Service) GetRequest(ctx context.Context, userID, requestID string) (*Request, error) {
	episodes, err := svc.repository.ListEpisodesByRequestID(ctx, userID, requestID)
	if err != nil {
		return nil, fmt.Errorf("failed to list request episodes: %w", err)
	}
	if len(episodes) == 0 {
		return nil, nil
	}
	return newRequest(requestID, episodes), nil
}

// ListRequests returns the requests of the user, newest first.
func (svc *Service) ListRequests(ctx context.Context, userID string) ([]*Request, error) {
	episodes, err := svc.repository.ListUserEpisodes(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to list episodes: %w", err)
	}
	return GroupRequests(episodes), nil
}

// GroupRequests groups the episodes by request, newest request first. Episodes that
// were created before requests existed are not in a request.
func GroupRequests(episodes []*Episode) []*Request {
	byID := make(map[string][]*Episode)
	for _, ep := range episodes {
		if id := ep.RequestID(); id != "" {
			byID[id] = append(byID[id], ep)
		}
	}
	requests := make([]*Request, 0, len(byID))
	for id, eps := range byID {
		slices.SortFunc(eps, func(a, b *Episode) int { return CompareEpisodeIDs(a.ID, b.ID) })
		requests = append(requests, newRequest(id, eps))
	}
	slices.SortFunc(requests, func(a, b *Request) int { return b.CreatedAt.Compare(a.CreatedAt) })
	return requests
}

func newRequest(id string, episodes []*Episode) *Request {
	r := &Request{ID: id, SourceURL: episodes[0].SourceURL, CreatedAt: episodes[0].CreatedAt, Episodes: episodes}
	for _, ep := range episodes {
		if ep.CreatedAt.Before(r.CreatedAt) {
			r.CreatedAt = ep.CreatedAt
		}
	}
	return r
}

// CompareEpisodeIDs compares numeric IDs as numbers, so that "9" is before "10".
func CompareEpisodeIDs(a, b string) int {
	ai, aErr := strconv.Atoi(a)
	bi, bErr := strconv.Atoi(b)
	if aErr != nil || bErr != nil {
		if a < b {
			return -1
		} else if a > b {
			return 1
		}
		return 0
	}
	return ai - bi
}
