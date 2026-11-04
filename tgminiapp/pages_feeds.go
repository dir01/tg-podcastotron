package tgminiapp

import (
	"net/http"
	"strings"
	"time"

	"tg-podcastotron/service"
)

type feedData struct {
	Feed      *service.Feed
	IsDefault bool
	Episodes  []*episodeView
	Now       time.Time
}

func (s *Server) getFeed(r *http.Request, u *User) (*service.Feed, error) {
	feed, err := s.svc.GetFeed(r.Context(), u.ID, r.PathValue("id"))
	if err != nil {
		return nil, err
	}
	if feed == nil {
		return nil, notFound("Feed")
	}
	return feed, nil
}

func (s *Server) feedData(r *http.Request, u *User) (*feedData, error) {
	ctx := r.Context()
	feed, err := s.getFeed(r, u)
	if err != nil {
		return nil, err
	}
	episodes, err := s.svc.ListFeedEpisodes(ctx, u.ID, feed.ID)
	if err != nil {
		return nil, err
	}
	sortNewestFirst(episodes)
	feeds, err := s.feedIndex(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	data := &feedData{Feed: feed, IsDefault: feed.ID == service.DefaultFeedID, Now: s.now()}
	if data.Episodes, err = s.episodeViews(ctx, u.ID, episodes, feeds); err != nil {
		return nil, err
	}
	return data, nil
}

func (s *Server) feed(w http.ResponseWriter, r *http.Request, u *User) error {
	data, err := s.feedData(r, u)
	if err != nil {
		return err
	}
	return s.render(w, "feed", data)
}

func (s *Server) newFeed(w http.ResponseWriter, r *http.Request, u *User) error {
	return s.render(w, "feed-new", nil)
}

func (s *Server) createFeed(w http.ResponseWriter, r *http.Request, u *User) error {
	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" {
		return badRequest("Give the feed a name.")
	}
	feed, err := s.svc.CreateFeed(r.Context(), u.ID, title)
	if err != nil {
		return err
	}
	navigate(w, "/feeds/"+feed.ID)
	return nil
}

func (s *Server) renameFeed(w http.ResponseWriter, r *http.Request, u *User) error {
	feed, err := s.getFeed(r, u)
	if err != nil {
		return err
	}
	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" {
		return badRequest("The name cannot be empty.")
	}
	if err := s.svc.RenameFeed(r.Context(), u.ID, feed.ID, title); err != nil {
		return err
	}
	toast(w, "Renamed")
	data, err := s.feedData(r, u)
	if err != nil {
		return err
	}
	return s.partial(w, "feed-title", data)
}

func (s *Server) setFeedPermanent(w http.ResponseWriter, r *http.Request, u *User) error {
	feed, err := s.getFeed(r, u)
	if err != nil {
		return err
	}
	if err := s.svc.SetFeedPermanent(r.Context(), u.ID, feed.ID, r.FormValue("permanent") == "1"); err != nil {
		return err
	}
	data, err := s.feedData(r, u)
	if err != nil {
		return err
	}
	return s.partial(w, "feed-body", data)
}

func (s *Server) regenerateFeed(w http.ResponseWriter, r *http.Request, u *User) error {
	feed, err := s.getFeed(r, u)
	if err != nil {
		return err
	}
	if err := s.svc.RegenerateFeed(r.Context(), u.ID, feed.ID); err != nil {
		return err
	}
	toast(w, "The feed file will be made again in a few seconds")
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// removeFromFeed removes one episode from the feed. The episode stays in the library.
func (s *Server) removeFromFeed(w http.ResponseWriter, r *http.Request, u *User) error {
	feed, err := s.getFeed(r, u)
	if err != nil {
		return err
	}
	epID := r.FormValue("episode")
	if err := s.svc.RemoveEpisodesFromFeeds(r.Context(), u.ID, []string{epID}, []string{feed.ID}); err != nil {
		return err
	}
	toast(w, "Removed from "+feed.Title)
	data, err := s.feedData(r, u)
	if err != nil {
		return err
	}
	return s.partial(w, "feed-body", data)
}

func (s *Server) deleteFeed(w http.ResponseWriter, r *http.Request, u *User) error {
	feed, err := s.getFeed(r, u)
	if err != nil {
		return err
	}
	if feed.ID == service.DefaultFeedID {
		return badRequest("You cannot delete the default feed.")
	}
	withEpisodes := r.FormValue("with_episodes") == "1"
	if err := s.svc.DeleteFeed(r.Context(), u.ID, feed.ID, withEpisodes); err != nil {
		return err
	}
	if withEpisodes {
		toast(w, "Feed and its episodes deleted")
	} else {
		toast(w, "Feed deleted. Its episodes are still in your library")
	}
	navigate(w, "/")
	return nil
}
