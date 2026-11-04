package tgminiapp

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"tg-podcastotron/service"
)

// homeRecentRequests is the number of finished requests on the home page.
const homeRecentRequests = 5

type homeData struct {
	User     *User
	Activity *activityData
	Feeds    []feedSummary
	Episodes totals
	Now      time.Time
}

type activityData struct {
	Active []*requestSummary
	Recent []*requestSummary
	Now    time.Time
}

func (a *activityData) Polling() bool { return len(a.Active) > 0 }

type requestSummary struct {
	*service.Request
	Bar   []barPart
	Ready int
	Fail  int
}

type feedSummary struct {
	*service.Feed
	Totals totals
}

func (s *Server) home(w http.ResponseWriter, r *http.Request, u *User) error {
	ctx := r.Context()
	episodes, err := s.svc.ListUserEpisodes(ctx, u.ID)
	if err != nil {
		return err
	}
	feeds, err := s.feedIndex(ctx, u.ID)
	if err != nil {
		return err
	}
	// An episode in two feeds counts in each of them.
	byFeed := map[string]*totals{}
	for _, f := range feeds.list {
		byFeed[f.ID] = &totals{}
	}
	for _, ep := range episodes {
		for _, id := range ep.FeedIDs {
			if t, ok := byFeed[id]; ok {
				t.add(ep)
			}
		}
	}
	data := &homeData{User: u, Activity: activity(episodes, s.now()), Episodes: sumEpisodes(episodes), Now: s.now()}
	for _, f := range feeds.list {
		data.Feeds = append(data.Feeds, feedSummary{Feed: f, Totals: *byFeed[f.ID]})
	}
	return s.render(w, "home", data)
}

// homeActive is the activity part of the home page. It polls while requests run.
func (s *Server) homeActive(w http.ResponseWriter, r *http.Request, u *User) error {
	episodes, err := s.svc.ListUserEpisodes(r.Context(), u.ID)
	if err != nil {
		return err
	}
	return s.partial(w, "activity", activity(episodes, s.now()))
}

func activity(episodes []*service.Episode, now time.Time) *activityData {
	a := &activityData{Now: now}
	for _, req := range service.GroupRequests(episodes) {
		sum := &requestSummary{
			Request: req,
			Bar:     progressBar(req.Episodes),
			Ready:   req.Count(service.EpisodeStatusComplete),
			Fail:    req.Count(service.EpisodeStatusFailed),
		}
		if !req.Finished() {
			a.Active = append(a.Active, sum)
		} else if len(a.Recent) < homeRecentRequests {
			a.Recent = append(a.Recent, sum)
		}
	}
	return a
}

// createLink saves a pasted link and opens the page to choose files.
func (s *Server) createLink(w http.ResponseWriter, r *http.Request, u *User) error {
	ctx := r.Context()
	source := strings.TrimSpace(r.FormValue("url"))
	if source == "" {
		return badRequest("Paste a link first.")
	}
	valid, err := s.svc.IsValidURL(ctx, source)
	if err != nil {
		return fmt.Errorf("failed to check url: %w", err)
	}
	if !valid {
		return badRequest("This link is not supported. Use a magnet link or a link to a video.")
	}
	link, err := s.svc.SaveSourceLink(ctx, u.ID, source)
	if err != nil {
		return err
	}
	s.metadata.start(ctx, link.URL)
	path := "/new/" + link.ID
	// A link pasted on a feed page makes episodes for that feed.
	if feed := r.FormValue("feed"); feed != "" {
		path += "?feed=" + url.QueryEscape(feed)
	}
	navigate(w, path)
	return nil
}

type newRequestData struct {
	Link  *service.SourceLink
	Title string
	Files *filesData
}

type filesData struct {
	Link     *service.SourceLink
	Feed     string // the feed of the page where the link was pasted, or ""
	Loading  bool
	Error    string
	Wait     int // number of polls so far
	Metadata *service.Metadata
	Torrent  bool
	Tree     *fileNode
	Feeds    []*service.Feed
	Checked  []string // the feeds that are selected when the page opens
	Default  string   // ID of the default feed
}

func (s *Server) sourceLink(r *http.Request, u *User) (*service.SourceLink, error) {
	link, err := s.svc.GetSourceLink(r.Context(), u.ID, r.PathValue("link"))
	if err != nil {
		return nil, err
	}
	if link == nil {
		return nil, notFound("Link")
	}
	return link, nil
}

func (s *Server) newRequest(w http.ResponseWriter, r *http.Request, u *User) error {
	link, err := s.sourceLink(r, u)
	if err != nil {
		return err
	}
	files, err := s.filesData(r, u, link)
	if err != nil {
		return err
	}
	return s.render(w, "new", &newRequestData{Link: link, Title: service.TitleFromSourceURL(link.URL), Files: files})
}

func (s *Server) newRequestFiles(w http.ResponseWriter, r *http.Request, u *User) error {
	link, err := s.sourceLink(r, u)
	if err != nil {
		return err
	}
	files, err := s.filesData(r, u, link)
	if err != nil {
		return err
	}
	return s.partial(w, "new-files", files)
}

func (s *Server) filesData(r *http.Request, u *User, link *service.SourceLink) (*filesData, error) {
	ctx := r.Context()
	md, done, err := s.metadata.get(ctx, link.URL)
	data := &filesData{Link: link, Feed: r.URL.Query().Get("feed")}
	switch {
	case !done:
		data.Loading = true
		data.Wait, _ = strconv.Atoi(r.URL.Query().Get("wait"))
		data.Wait++
		return data, nil
	case err != nil:
		s.logger.WarnContext(ctx, "failed to get metadata", slog.String("url", link.URL), slog.Any("error", err))
		data.Error = "Could not get the file list of this link. Check the link and try again."
		return data, nil
	}

	data.Metadata = md
	data.Torrent = md.DownloaderName == "torrent"
	if data.Torrent {
		data.Tree = buildFileTree(md.Variants)
	}

	feeds, err := s.feedIndex(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	data.Feeds = feeds.list
	data.Default = service.DefaultFeedID
	if _, ok := feeds.byID[data.Feed]; ok {
		data.Checked = []string{data.Feed}
		return data, nil
	}
	data.Checked, err = s.lastUsedFeeds(r, u, feeds)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// lastUsedFeeds returns the feeds of the newest episode, so that a series goes to
// the same feeds as the last request. Without episodes, it returns the default feed.
func (s *Server) lastUsedFeeds(r *http.Request, u *User, feeds *feedIndex) ([]string, error) {
	episodes, err := s.svc.ListUserEpisodes(r.Context(), u.ID)
	if err != nil {
		return nil, err
	}
	sortNewestFirst(episodes)
	for _, ep := range episodes {
		if fs := feeds.of(ep); len(fs) > 0 {
			ids := make([]string, len(fs))
			for i, f := range fs {
				ids[i] = f.ID
			}
			return ids, nil
		}
	}
	return []string{service.DefaultFeedID}, nil
}

func (s *Server) createRequest(w http.ResponseWriter, r *http.Request, u *User) error {
	ctx := r.Context()
	link, err := s.sourceLink(r, u)
	if err != nil {
		return err
	}
	if err := r.ParseForm(); err != nil {
		return badRequest("Could not read the form.")
	}

	files := r.Form["file"]
	if len(files) == 0 {
		return badRequest("Choose at least one file.")
	}
	// The form has the files in the order that the user selected them: that is the
	// order of the episodes, or of the parts of one joined episode.
	// Check the files if the list is still known.
	if md, done, err := s.metadata.get(ctx, link.URL); done && err == nil {
		for _, f := range files {
			if !slices.ContainsFunc(md.Variants, func(v service.Variant) bool { return v.ID == f }) {
				return badRequest("The file list changed. Open the link again.")
			}
		}
	}

	feedIDs := r.Form["feed"]
	newFeed := strings.TrimSpace(r.FormValue("new_feed"))
	if len(feedIDs) == 0 && newFeed == "" {
		return badRequest("Choose a feed for the episodes.")
	}
	if newFeed != "" {
		feed, err := s.svc.CreateFeed(ctx, u.ID, newFeed)
		if err != nil {
			return err
		}
		feedIDs = append(feedIDs, feed.ID)
	}

	variants, processing := episodeVariants(files, r.FormValue("mode") == "join")
	requestID, err := s.svc.CreateEpisodesAsync(ctx, u.ID, link.URL, variants, processing, feedIDs)
	if err != nil {
		return err
	}
	return s.replacePage(w, r, "/requests/"+requestID)
}

// episodeVariants returns the files of each episode. The episodes, and the parts of
// a joined episode, keep the order of files.
func episodeVariants(files []string, join bool) ([][]string, service.ProcessingType) {
	if join && len(files) > 1 {
		return [][]string{files}, service.ProcessingTypeConcatenate
	}
	variants := make([][]string, len(files))
	for i, f := range files {
		variants[i] = []string{f}
	}
	return variants, service.ProcessingTypeUploadOriginal
}

// requestWaitPolls is the number of polls (2 s each) before the request page stops
// to wait for the episodes of a new request.
const requestWaitPolls = 90

type requestData struct {
	ID       string
	Request  *service.Request
	Totals   totals
	Episodes []*episodeView
	Bar      []barPart
	Feeds    *requestFeedsData
	Ready    int
	Failed   int
	Wait     int
	Now      time.Time
}

func (d *requestData) Polling() bool {
	if d.Request == nil {
		return d.Wait < requestWaitPolls
	}
	return !d.Request.Finished()
}

func (s *Server) request(w http.ResponseWriter, r *http.Request, u *User) error {
	data, err := s.requestData(r, u)
	if err != nil {
		return err
	}
	return s.render(w, "request", data)
}

func (s *Server) requestProgress(w http.ResponseWriter, r *http.Request, u *User) error {
	data, err := s.requestData(r, u)
	if err != nil {
		return err
	}
	// The page opened before the episodes existed, so it has no feeds section yet.
	// Show all of the page now.
	if r.URL.Query().Get("prep") == "1" && data.Request != nil {
		w.Header().Set("HX-Retarget", "#main")
		w.Header().Set("HX-Reswap", "innerHTML")
		return s.render(w, "request", data)
	}
	return s.partial(w, "request-progress", data)
}

// requestFeedsData is the feeds section of a link page. The progress card polls,
// so this section is outside of it: a poll must not close it while the user uses it.
type requestFeedsData struct {
	ID       string
	Finished bool
	Total    int
	Rows     []bulkFeedRow
	AnyIn    bool
	Open     bool
}

func newRequestFeedsData(req *service.Request, feeds *feedIndex, open bool) *requestFeedsData {
	data := &requestFeedsData{ID: req.ID, Finished: req.Finished(), Total: len(req.Episodes), Open: open}
	for _, f := range feeds.list {
		row := bulkFeedRow{Feed: f}
		for _, ep := range req.Episodes {
			if slices.Contains(ep.FeedIDs, f.ID) {
				row.In++
			}
		}
		data.AnyIn = data.AnyIn || row.In > 0
		data.Rows = append(data.Rows, row)
	}
	return data
}

// deleteRequest deletes all episodes of the link, also the episodes that are not finished.
func (s *Server) deleteRequest(w http.ResponseWriter, r *http.Request, u *User) error {
	ctx := r.Context()
	req, err := s.svc.GetRequest(ctx, u.ID, r.PathValue("id"))
	if err != nil {
		return err
	}
	if req == nil {
		return notFound("Link")
	}
	ids := strings.Split(joinIDs(req.Episodes), ",")
	if err := s.svc.DeleteEpisodes(ctx, u.ID, ids); err != nil {
		return err
	}
	toast(w, "Deleted "+plural(len(ids), "episode", "episodes"))
	return s.replacePage(w, r, "/")
}

// requestFeeds adds all episodes of the link to a feed, or removes them from it.
func (s *Server) requestFeeds(w http.ResponseWriter, r *http.Request, u *User) error {
	ctx := r.Context()
	req, err := s.svc.GetRequest(ctx, u.ID, r.PathValue("id"))
	if err != nil {
		return err
	}
	if req == nil {
		return notFound("Link")
	}
	ids := strings.Split(joinIDs(req.Episodes), ",")
	feedID := r.FormValue("feed")
	if r.FormValue("in") == "1" {
		err = s.svc.AddEpisodesToFeeds(ctx, u.ID, ids, []string{feedID})
	} else {
		err = s.svc.RemoveEpisodesFromFeeds(ctx, u.ID, ids, []string{feedID})
	}
	if err != nil {
		return err
	}
	if req, err = s.svc.GetRequest(ctx, u.ID, req.ID); err != nil || req == nil {
		return err
	}
	feeds, err := s.feedIndex(ctx, u.ID)
	if err != nil {
		return err
	}
	return s.partial(w, "request-feeds", newRequestFeedsData(req, feeds, true))
}

func (s *Server) requestData(r *http.Request, u *User) (*requestData, error) {
	ctx := r.Context()
	data := &requestData{ID: r.PathValue("id"), Now: s.now()}
	data.Wait, _ = strconv.Atoi(r.URL.Query().Get("wait"))
	data.Wait++

	req, err := s.svc.GetRequest(ctx, u.ID, data.ID)
	if err != nil || req == nil {
		return data, err
	}
	feeds, err := s.feedIndex(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	data.Request = req
	data.Totals = sumEpisodes(req.Episodes)
	data.Bar = progressBar(req.Episodes)
	data.Ready = req.Count(service.EpisodeStatusComplete)
	data.Failed = req.Count(service.EpisodeStatusFailed)
	if data.Episodes, err = s.episodeViews(ctx, u.ID, req.Episodes, feeds); err != nil {
		return nil, err
	}
	data.Feeds = newRequestFeedsData(req, feeds, false)
	return data, nil
}
