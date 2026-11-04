package tgminiapp

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"tg-podcastotron/service"
)

const (
	episodesPageSize = 50
	maxBulkEpisodes  = 1000
)

// episodeFilters are the filters of the episodes list.
var episodeFilters = []struct{ Key, Label string }{
	{"all", "All"},
	{"active", "In progress"},
	{"failed", "Failed"},
	{"nofeed", "No feed"},
}

type episodesData struct {
	Filter     string
	Filters    []struct{ Key, Label string }
	Episodes   []*episodeView
	Totals     totals // of all episodes of the filter, not only of this page
	NextOffset int    // 0 if there are no more episodes
	Now        time.Time
}

func (s *Server) episodes(w http.ResponseWriter, r *http.Request, u *User) error {
	ctx := r.Context()
	filter := r.URL.Query().Get("show")
	if !slices.ContainsFunc(episodeFilters, func(f struct{ Key, Label string }) bool { return f.Key == filter }) {
		filter = "all"
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))

	all, err := s.svc.ListUserEpisodes(ctx, u.ID)
	if err != nil {
		return err
	}
	var episodes []*service.Episode
	for _, ep := range all {
		switch {
		case filter == "active" && ep.Status.IsFinal(),
			filter == "failed" && ep.Status != service.EpisodeStatusFailed,
			filter == "nofeed" && len(ep.FeedIDs) > 0:
			continue
		}
		episodes = append(episodes, ep)
	}
	sortNewestFirst(episodes)

	data := &episodesData{Filter: filter, Filters: episodeFilters, Totals: sumEpisodes(episodes), Now: s.now()}
	if offset > len(episodes) {
		offset = len(episodes)
	}
	page := episodes[offset:]
	if len(page) > episodesPageSize {
		page = page[:episodesPageSize]
		data.NextOffset = offset + episodesPageSize
	}
	feeds, err := s.feedIndex(ctx, u.ID)
	if err != nil {
		return err
	}
	if data.Episodes, err = s.episodeViews(ctx, u.ID, page, feeds); err != nil {
		return err
	}
	if offset > 0 {
		return s.partial(w, "episode-rows", data)
	}
	return s.render(w, "episodes", data)
}

type episodeData struct {
	Episode *episodeView
	Events  []*service.EpisodeEvent
	Feeds   *episodeFeedsData
	Now     time.Time
}

type episodeFeedsData struct {
	Episode *episodeView
	Feeds   []*service.Feed
	Now     time.Time
}

func (d *episodeData) Polling() bool { return !d.Episode.Status.IsFinal() }

func (s *Server) getEpisode(r *http.Request, u *User) (*service.Episode, error) {
	id := r.PathValue("id")
	episodes, err := s.svc.GetEpisodesMap(r.Context(), u.ID, []string{id})
	if err != nil {
		return nil, err
	}
	ep, ok := episodes[id]
	if !ok {
		return nil, notFound("Episode")
	}
	return ep, nil
}

func (s *Server) episodeData(r *http.Request, u *User) (*episodeData, error) {
	ctx := r.Context()
	ep, err := s.getEpisode(r, u)
	if err != nil {
		return nil, err
	}
	events, err := s.svc.ListEpisodeEvents(ctx, u.ID, ep.ID)
	if err != nil {
		return nil, err
	}
	feeds, err := s.feedIndex(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	view := newEpisodeView(ep, events, feeds)
	return &episodeData{
		Episode: view,
		Events:  events,
		Feeds:   &episodeFeedsData{Episode: view, Feeds: feeds.list, Now: s.now()},
		Now:     s.now(),
	}, nil
}

func (s *Server) episode(w http.ResponseWriter, r *http.Request, u *User) error {
	data, err := s.episodeData(r, u)
	if err != nil {
		return err
	}
	return s.render(w, "episode", data)
}

func (s *Server) episodeStatus(w http.ResponseWriter, r *http.Request, u *User) error {
	data, err := s.episodeData(r, u)
	if err != nil {
		return err
	}
	return s.partial(w, "episode-status", data)
}

func (s *Server) renameEpisode(w http.ResponseWriter, r *http.Request, u *User) error {
	ep, err := s.getEpisode(r, u)
	if err != nil {
		return err
	}
	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" {
		return badRequest("The title cannot be empty.")
	}
	if err := s.svc.RenameEpisodes(r.Context(), u.ID, []string{ep.ID}, title); err != nil {
		return err
	}
	toast(w, "Renamed")
	data, err := s.episodeData(r, u)
	if err != nil {
		return err
	}
	return s.partial(w, "episode-title", data)
}

// episodeFeeds adds the episode to a feed or removes it, and shows the feeds again.
func (s *Server) episodeFeeds(w http.ResponseWriter, r *http.Request, u *User) error {
	ctx := r.Context()
	ep, err := s.getEpisode(r, u)
	if err != nil {
		return err
	}
	feedID := r.FormValue("feed")
	if r.FormValue("in") == "1" {
		err = s.svc.AddEpisodesToFeeds(ctx, u.ID, []string{ep.ID}, []string{feedID})
	} else {
		err = s.svc.RemoveEpisodesFromFeeds(ctx, u.ID, []string{ep.ID}, []string{feedID})
	}
	if err != nil {
		return err
	}
	data, err := s.episodeData(r, u)
	if err != nil {
		return err
	}
	return s.partial(w, "episode-feeds", data.Feeds)
}

func (s *Server) deleteEpisode(w http.ResponseWriter, r *http.Request, u *User) error {
	ep, err := s.getEpisode(r, u)
	if err != nil {
		return err
	}
	if err := s.svc.DeleteEpisodes(r.Context(), u.ID, []string{ep.ID}); err != nil {
		return err
	}
	toast(w, "Episode deleted")
	return s.replacePage(w, r, "/episodes")
}

type bulkData struct {
	IDs      string
	Episodes []*service.Episode
	Feeds    *bulkFeedsData
}

type bulkFeedsData struct {
	IDs   string
	Total int
	Rows  []bulkFeedRow
}

type bulkFeedRow struct {
	*service.Feed
	In int // number of the episodes in the feed
}

// bulkEpisodes returns the episodes of the "ids" form value that exist, in that order.
func (s *Server) bulkEpisodes(r *http.Request, u *User) ([]*service.Episode, error) {
	if err := r.ParseForm(); err != nil {
		return nil, badRequest("Could not read the form.")
	}
	// The list form sends "ids" one time for each episode. The other forms send one comma-separated value.
	var ids []string
	for _, value := range r.Form["ids"] {
		for _, id := range strings.Split(value, ",") {
			if id = strings.TrimSpace(id); id != "" && !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
	}
	if len(ids) == 0 {
		return nil, badRequest("Select some episodes first.")
	}
	if len(ids) > maxBulkEpisodes {
		return nil, badRequest("Select at most %d episodes.", maxBulkEpisodes)
	}
	found, err := s.svc.GetEpisodesMap(r.Context(), u.ID, ids)
	if err != nil {
		return nil, err
	}
	var episodes []*service.Episode
	for _, id := range ids {
		if ep, ok := found[id]; ok {
			episodes = append(episodes, ep)
		}
	}
	if len(episodes) == 0 {
		return nil, notFound("Episodes")
	}
	return episodes, nil
}

func (s *Server) bulkData(r *http.Request, u *User) (*bulkData, error) {
	episodes, err := s.bulkEpisodes(r, u)
	if err != nil {
		return nil, err
	}
	feeds, err := s.feedIndex(r.Context(), u.ID)
	if err != nil {
		return nil, err
	}
	ids := joinIDs(episodes)
	data := &bulkData{IDs: ids, Episodes: episodes, Feeds: &bulkFeedsData{IDs: ids, Total: len(episodes)}}
	for _, f := range feeds.list {
		row := bulkFeedRow{Feed: f}
		for _, ep := range episodes {
			if slices.Contains(ep.FeedIDs, f.ID) {
				row.In++
			}
		}
		data.Feeds.Rows = append(data.Feeds.Rows, row)
	}
	return data, nil
}

func (s *Server) bulk(w http.ResponseWriter, r *http.Request, u *User) error {
	data, err := s.bulkData(r, u)
	if err != nil {
		return err
	}
	return s.render(w, "bulk", data)
}

func (s *Server) bulkFeeds(w http.ResponseWriter, r *http.Request, u *User) error {
	episodes, err := s.bulkEpisodes(r, u)
	if err != nil {
		return err
	}
	ids := strings.Split(joinIDs(episodes), ",")
	feedID := r.FormValue("feed")
	if r.FormValue("action") == "add" {
		err = s.svc.AddEpisodesToFeeds(r.Context(), u.ID, ids, []string{feedID})
	} else {
		err = s.svc.RemoveEpisodesFromFeeds(r.Context(), u.ID, ids, []string{feedID})
	}
	if err != nil {
		return err
	}
	data, err := s.bulkData(r, u)
	if err != nil {
		return err
	}
	return s.partial(w, "bulk-feeds", data.Feeds)
}

func (s *Server) bulkRename(w http.ResponseWriter, r *http.Request, u *User) error {
	episodes, err := s.bulkEpisodes(r, u)
	if err != nil {
		return err
	}
	pattern := strings.TrimSpace(r.FormValue("pattern"))
	if pattern == "" {
		return badRequest("The title cannot be empty.")
	}
	ids := strings.Split(joinIDs(episodes), ",")
	if err := s.svc.RenameEpisodes(r.Context(), u.ID, ids, pattern); err != nil {
		return err
	}
	toast(w, "Renamed "+plural(len(ids), "episode", "episodes"))
	data, err := s.bulkData(r, u)
	if err != nil {
		return err
	}
	return s.partial(w, "bulk-titles", data)
}

func (s *Server) bulkDelete(w http.ResponseWriter, r *http.Request, u *User) error {
	episodes, err := s.bulkEpisodes(r, u)
	if err != nil {
		return err
	}
	ids := strings.Split(joinIDs(episodes), ",")
	if err := s.svc.DeleteEpisodes(r.Context(), u.ID, ids); err != nil {
		return err
	}
	toast(w, "Deleted "+plural(len(ids), "episode", "episodes"))
	return s.replacePage(w, r, "/episodes")
}
