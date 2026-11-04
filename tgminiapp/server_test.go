package tgminiapp

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	migrate "github.com/rubenv/sql-migrate"
	"tg-podcastotron/auth"
	"tg-podcastotron/mediary"
	"tg-podcastotron/mediary/mediarymocks"
	"tg-podcastotron/service"
	jobsqueue "tg-podcastotron/service/jobs_queue"
	"tg-podcastotron/service/servicemocks"
)

type testApp struct {
	t      *testing.T
	server *Server
	svc    *service.Service
	repo   service.Repository
	auth   *auth.Service
}

// newTestApp makes a Mini App with a real service on SQLite. The queue does not run.
func newTestApp(t *testing.T, devUser *DevUser) *testApp {
	t.Helper()
	logger := slog.New(slog.DiscardHandler)
	db, err := sql.Open("sqlite3", "file:"+filepath.Join(t.TempDir(), "test.db")+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := migrate.Exec(db, "sqlite3", &migrate.FileMigrationSource{Dir: "../db/migrations"}, migrate.Up); err != nil {
		t.Fatal(err)
	}
	queue, err := jobsqueue.New(db, 1, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(queue.Shutdown)

	size := int64(5 << 20)
	mediaryMock := &mediarymocks.ServiceMock{
		IsValidURLFunc: func(ctx context.Context, mediaURL string) (bool, error) {
			return strings.HasPrefix(mediaURL, "magnet:"), nil
		},
		FetchMetadataLongPollingFunc: func(ctx context.Context, mediaURL string) (*mediary.Metadata, error) {
			return &mediary.Metadata{URL: mediaURL, DownloaderName: "torrent", Variants: []mediary.Variant{
				{ID: "Book/CD 1/01.mp3", LenBytes: &size},
				{ID: "Book/CD 1/02.mp3", LenBytes: &size},
				{ID: "Book/cover.jpg", LenBytes: &size},
			}}, nil
		},
	}
	s3 := &servicemocks.MockS3Store{
		URLFunc:          func(key string) (string, error) { return "https://s3.example.com/" + key, nil },
		PreSignedURLFunc: func(key string) (string, error) { return "https://s3.example.com/" + key + "?signed", nil },
		DeleteFunc:       func(ctx context.Context, key string) error { return nil },
		ListFunc: func(ctx context.Context, prefix string) ([]service.S3Object, error) {
			return nil, nil
		},
	}
	repo := service.NewSqliteRepository(db, db)
	svc := service.New(mediaryMock, repo, s3, queue, "", func(s string) string { return "p-" + s }, logger, nil)
	authSvc := auth.New("admin", auth.NewSqliteRepository(db), logger)

	server, err := New(Config{BotToken: testBotToken, BotUsername: "testbot", DevUser: devUser}, svc, authSvc, logger)
	if err != nil {
		t.Fatal(err)
	}
	return &testApp{t: t, server: server, svc: svc, repo: repo, auth: authSvc}
}

// do sends an htmx request. form, if not nil, is the body of a POST.
func (a *testApp) do(method, target string, form url.Values, header ...string) (*http.Response, string) {
	a.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req := httptest.NewRequest(method, target, body)
	req.Header.Set("HX-Request", "true")
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	rec := httptest.NewRecorder()
	a.server.Handler().ServeHTTP(rec, req)
	return rec.Result(), rec.Body.String()
}

// get sends an htmx GET and checks the status and the content.
func (a *testApp) get(target string, wantStatus int, wantContent ...string) string {
	a.t.Helper()
	resp, body := a.do(http.MethodGet, target, nil)
	if resp.StatusCode != wantStatus {
		a.t.Fatalf("GET %s: status = %d, want %d\n%s", target, resp.StatusCode, wantStatus, body)
	}
	for _, want := range wantContent {
		if !strings.Contains(body, want) {
			a.t.Errorf("GET %s: no %q in\n%s", target, want, body)
		}
	}
	return body
}

func (a *testApp) post(target string, form url.Values) (*http.Response, string) {
	a.t.Helper()
	resp, body := a.do(http.MethodPost, target, form)
	if resp.StatusCode >= 400 {
		a.t.Fatalf("POST %s: status = %d\n%s", target, resp.StatusCode, body)
	}
	return resp, body
}

// location returns the path of the HX-Location header.
func location(t *testing.T, resp *http.Response) string {
	t.Helper()
	var loc struct{ Path string }
	if err := json.Unmarshal([]byte(resp.Header.Get("HX-Location")), &loc); err != nil {
		t.Fatalf("HX-Location = %q: %v", resp.Header.Get("HX-Location"), err)
	}
	return loc.Path
}

// seed adds a feed and a request with episodes in different statuses.
func (a *testApp) seed(userID string) {
	a.t.Helper()
	ctx := context.Background()
	books, err := a.svc.CreateFeed(ctx, userID, "Books")
	if err != nil {
		a.t.Fatal(err)
	}
	if err := a.svc.SetFeedPermanent(ctx, userID, books.ID, true); err != nil {
		a.t.Fatal(err)
	}
	now := time.Now().UTC()
	statuses := []service.EpisodeStatus{service.EpisodeStatusComplete, service.EpisodeStatusDownloading, service.EpisodeStatusFailed}
	for i, st := range statuses {
		id := []string{"1", "2", "3"}[i]
		if _, err := a.repo.SaveEpisode(ctx, &service.Episode{
			ID: id, UserID: userID, Title: "Chapter " + id, CreatedAt: now, UpdatedAt: now,
			SourceURL: "magnet:?xt=urn:btih:abc&dn=The+Book", SourceFilepaths: []string{id + ".mp3"},
			URL: "https://s3.example.com/" + id + ".mp3", Status: st, Duration: 90 * time.Minute, FileLenBytes: 80 << 20,
			StorageKey: id + ".mp3", CreationKey: "req1/" + id,
		}); err != nil {
			a.t.Fatal(err)
		}
		if err := a.repo.InsertEpisodeEvents(ctx, []*service.EpisodeEvent{
			{UserID: userID, EpisodeID: id, At: now.Add(-time.Minute), Status: service.EpisodeStatusCreated},
			{UserID: userID, EpisodeID: id, At: now, Status: st, Attempt: 2, MaxAttempts: 6, Detail: "boom " + id},
		}); err != nil {
			a.t.Fatal(err)
		}
	}
	if err := a.svc.AddEpisodesToFeeds(ctx, userID, []string{"1", "2"}, []string{books.ID}); err != nil {
		a.t.Fatal(err)
	}
	if err := a.repo.SaveCover(ctx, &service.Cover{ID: "c1.jpg", MIME: "image/jpeg", Data: []byte{0xFF, 0xD8, 0xFF}}); err != nil {
		a.t.Fatal(err)
	}
	if err := a.repo.SetEpisodeCover(ctx, userID, "1", "c1.jpg"); err != nil {
		a.t.Fatal(err)
	}
}

func TestPages(t *testing.T) {
	app := newTestApp(t, &DevUser{ID: "100", Username: "admin"})
	app.seed("100")

	app.get("/", 200, "Podcast-O-Tron", "Your feeds", "Books", "Recent links", "The Book", "Admin",
		"2 episodes · 3 h · 160 MB", "3 episodes · 4 h 30 min · 240 MB",
		`data-copy="magnet:?xt=urn:btih:abc&amp;dn=The&#43;Book"`, `class="link-box-text">magnet:?xt=urn:btih:abc&amp;dn=The&#43;Book<`,
		`data-copy="https://s3.example.com/feeds/p-100/2"`)
	app.get("/home/active", 200, "The Book", `hx-trigger="every 3s"`)
	app.get("/requests/req1", 200, ">Link<", "3 episodes · 4 h 30 min · 240 MB", `id="request-feeds"`, "Books (2/3)", "Chapter 1", "Chapter 2", "1<span class=\"of\"> of 3", "boom 3", "Books", `class="link-box-text">magnet:?xt=urn:btih:abc`)
	app.get("/requests/req1/progress", 200, `hx-trigger="every 2s"`)
	app.get("/requests/unknown", 200, "Preparing the episodes")
	app.get("/requests/unknown/progress?wait=1000", 200, "This request has no episodes")
	// When the episodes of a new link appear, the poll shows all of the page, with the feeds section.
	if resp, body := app.do(http.MethodGet, "/requests/req1/progress?prep=1", nil); resp.Header.Get("HX-Retarget") != "#main" || !strings.Contains(body, `id="request-feeds"`) {
		t.Errorf("first poll with episodes: retarget = %q", resp.Header.Get("HX-Retarget"))
	}

	app.get("/episodes", 200, "3 episodes · 4 h 30 min · 240 MB", "Chapter 1", "Chapter 3", "No feed", `<span class="chip"><svg class="keep-icon"`, `class="thumb" src="/covers/c1.jpg"`, "thumb-empty")
	app.get("/episodes?show=failed", 200, "1 episode · 1 h 30 min · 80 MB", "Chapter 3", `href="/episodes?show=active" hx-replace-url="true"`)
	if body := app.get("/episodes?show=active", 200, "Chapter 2"); strings.Contains(body, "Chapter 1") {
		t.Error("the active filter shows a complete episode")
	}
	app.get("/episodes/1", 200, "Chapter 1", "<audio", "Books", "stays, because it is in a feed that keeps episodes",
		`class="cover" src="/covers/c1.jpg"`)
	app.get("/episodes/3", 200, "Chapter 3", "boom 3", "Status history", "This episode is in no feed")
	app.get("/episodes/2/status", 200, `hx-trigger="every 2s"`, "attempt 2 of 6", "Downloading (attempt 2 of 6)")
	app.get("/episodes/99", 404)
	app.get("/episodes/bulk?ids=1&ids=2,3", 200, "Edit 3 episodes", "2 of 3 are in this feed")

	app.get("/feeds/2", 200, "Books", "2 episodes · 3 h · 160 MB", `name="feed" value="2"`, "The new episodes go to this feed.", "Feed link", `class="link-box" data-copy="https://s3.example.com/feeds/p-100/2"`, `data-edit-toggle="feed-rename"`, "Chapter 1", "Delete feed")
	app.get("/feeds/1", 200, "You cannot delete it")
	app.get("/feeds/new", 200, "New feed")
	app.get("/feeds/99", 404)
	app.get("/admin", 200, "Make an invite link")
}

func TestNewRequestFlow(t *testing.T) {
	app := newTestApp(t, &DevUser{ID: "100", Username: "admin"})

	resp, _ := app.do(http.MethodPost, "/links", url.Values{"url": {"https://example.com/not-supported"}})
	if resp.StatusCode != http.StatusBadRequest || resp.Header.Get("HX-Retarget") != "#toast" {
		t.Errorf("unsupported link: status = %d, retarget = %q", resp.StatusCode, resp.Header.Get("HX-Retarget"))
	}

	resp, _ = app.post("/links", url.Values{"url": {"magnet:?xt=urn:btih:abc&dn=The+Book"}})
	newPath := location(t, resp)
	if !strings.HasPrefix(newPath, "/new/") {
		t.Fatalf("HX-Location = %q", newPath)
	}

	var body string
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(body, `data-file`) {
		if time.Now().After(deadline) {
			t.Fatalf("the file list did not load:\n%s", body)
		}
		time.Sleep(10 * time.Millisecond)
		body = app.get(newPath+"/files", 200)
	}
	app.get(newPath, 200, "The Book", "CD 1", "01.mp3", "All .mp3 (2)", "All (3)", "Add to feeds")
	// No file is selected, and all folders are closed.
	if strings.Contains(body, `data-size="5242880" checked`) || strings.Contains(body, "<details class=\"tree-folder\" open") {
		t.Errorf("a file is selected or a folder is open:\n%s", body)
	}
	if !strings.Contains(body, `name="feed" value="1" checked`) {
		t.Errorf("the default feed is not selected:\n%s", body)
	}
	// A folder has no checkbox, and two buttons select how to make the episodes.
	if strings.Contains(body, `class="folder-check"`) || strings.Contains(body, `type="radio" name="mode"`) {
		t.Errorf("a folder has a checkbox, or the mode is a selector:\n%s", body)
	}
	if !strings.Contains(body, `name="mode" value="separate"`) || !strings.Contains(body, `name="mode" value="join"`) {
		t.Errorf("the buttons to create the episodes are missing:\n%s", body)
	}

	resp, _ = app.do(http.MethodPost, newPath, url.Values{"file": {"Book/CD 1/01.mp3"}})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("request without a feed: status = %d, want 400", resp.StatusCode)
	}
	resp, _ = app.do(http.MethodPost, newPath, url.Values{"file": {"other.mp3"}, "feed": {"1"}})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("request with an unknown file: status = %d, want 400", resp.StatusCode)
	}

	resp, _ = app.post(newPath, url.Values{
		"file":     {"Book/CD 1/01.mp3", "Book/CD 1/02.mp3"},
		"mode":     {"join"},
		"feed":     {"1"},
		"new_feed": {"Audiobooks"},
	})
	// The finished form leaves the history: Back must not open it again.
	if path := resp.Header.Get("HX-Replace-Url"); !strings.HasPrefix(path, "/requests/") || resp.Header.Get("HX-Retarget") != "#main" {
		t.Fatalf("HX-Replace-Url = %q, HX-Retarget = %q", path, resp.Header.Get("HX-Retarget"))
	}
	feeds, err := app.svc.ListFeeds(context.Background(), "100")
	if err != nil || len(feeds) != 2 || feeds[1].Title != "Audiobooks" {
		t.Errorf("feeds after the request = %v, %v; want the new feed", feeds, err)
	}
}

func TestEditing(t *testing.T) {
	app := newTestApp(t, &DevUser{ID: "100", Username: "admin"})
	app.seed("100")
	ctx := context.Background()

	_, body := app.post("/episodes/3/feeds", url.Values{"feed": {"2"}, "in": {"1"}})
	if !strings.Contains(body, "stays, because it is in a feed that keeps episodes") {
		t.Errorf("episode 3 is not in the permanent feed:\n%s", body)
	}
	app.post("/episodes/1/feeds", url.Values{"feed": {"2"}})
	if eps, _ := app.svc.GetEpisodesMap(ctx, "100", []string{"1"}); len(eps["1"].FeedIDs) != 0 {
		t.Errorf("episode 1 feeds = %v, want none", eps["1"].FeedIDs)
	}

	// The feeds of all episodes of a link change from the link page.
	_, body = app.post("/requests/req1/feeds", url.Values{"feed": {"1"}, "in": {"1"}})
	if !strings.Contains(body, `id="request-feeds"`) || !strings.Contains(body, "open") {
		t.Errorf("link feeds:\n%s", body)
	}
	if eps, _ := app.svc.ListRequests(ctx, "100"); len(eps) != 1 || len(eps[0].Episodes[0].FeedIDs) == 0 {
		t.Errorf("link episodes are not in the feed")
	}
	app.post("/requests/req1/feeds", url.Values{"feed": {"1"}})

	_, body = app.post("/episodes/bulk/feeds", url.Values{"ids": {"1,2,3"}, "feed": {"1"}, "action": {"add"}})
	if !strings.Contains(body, "All are in this feed") {
		t.Errorf("bulk add:\n%s", body)
	}
	_, body = app.post("/episodes/bulk/rename", url.Values{"ids": {"1,2"}, "pattern": {"Part %id"}})
	if !strings.Contains(body, "Part 1") || !strings.Contains(body, "Part 2") {
		t.Errorf("bulk rename:\n%s", body)
	}
	resp, body := app.post("/episodes/2/title", url.Values{"title": {"New name"}})
	if !strings.Contains(body, "New name") || !strings.Contains(resp.Header.Get("HX-Trigger"), "Renamed") {
		t.Errorf("rename: trigger = %q\n%s", resp.Header.Get("HX-Trigger"), body)
	}

	_, body = app.post("/feeds/2/permanent", url.Values{})
	if strings.Contains(body, "checked") {
		t.Errorf("the feed is still permanent:\n%s", body)
	}
	app.post("/feeds/2/title", url.Values{"title": {"Audiobooks"}})
	if f, _ := app.svc.GetFeed(ctx, "100", "2"); f.Title != "Audiobooks" {
		t.Errorf("feed title = %q", f.Title)
	}

	resp, _ = app.do(http.MethodPost, "/feeds/1/delete", url.Values{})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("delete the default feed: status = %d, want 400", resp.StatusCode)
	}
	resp, body = app.post("/episodes/bulk/delete", url.Values{"ids": {"2,3"}})
	if resp.Header.Get("HX-Replace-Url") != "/episodes" || !strings.Contains(body, "Part 1") || strings.Contains(body, "New name") {
		t.Errorf("bulk delete: HX-Replace-Url = %q\n%s", resp.Header.Get("HX-Replace-Url"), body)
	}
	if eps, _ := app.svc.ListUserEpisodes(ctx, "100"); len(eps) != 1 {
		t.Errorf("episodes after delete = %d, want 1", len(eps))
	}
}

func TestAdmin(t *testing.T) {
	app := newTestApp(t, &DevUser{ID: "100", Username: "admin"})
	_, body := app.post("/admin/invites", url.Values{})
	if !strings.Contains(body, "https://t.me/testbot?start=inv_") {
		t.Errorf("invite:\n%s", body)
	}
	_, body = app.post("/admin/gc/scan", url.Values{})
	if !strings.Contains(body, "Scanned 0 episode files") {
		t.Errorf("gc scan:\n%s", body)
	}
	_, body = app.post("/admin/gc/delete", url.Values{"token": {"unknown"}})
	if !strings.Contains(body, "expired") {
		t.Errorf("gc delete with an unknown token:\n%s", body)
	}
}

func TestAccess(t *testing.T) {
	// A user who is not the admin and has no invite.
	app := newTestApp(t, &DevUser{ID: "200", Username: "stranger"})
	app.get("/", http.StatusForbidden, "You have no access yet")

	// The user accepts an invite, and then can open the app, but not the admin page.
	invite, err := app.auth.CreateInvite(context.Background(), "100")
	if err != nil {
		t.Fatal(err)
	}
	if err := app.auth.AcceptInvite(context.Background(), invite.Token, "200"); err != nil {
		t.Fatal(err)
	}
	app.get("/", http.StatusOK, "Your feeds")
	if body := app.get("/", http.StatusOK); strings.Contains(body, `href="/admin"`) {
		t.Error("a user who is not the admin sees the admin link")
	}
	resp, _ := app.do(http.MethodGet, "/admin", nil)
	if resp.StatusCode != http.StatusForbidden || resp.Header.Get("HX-Retarget") != "#toast" {
		t.Errorf("admin page: status = %d, retarget = %q", resp.StatusCode, resp.Header.Get("HX-Retarget"))
	}

	// Without a dev user, a request needs initData.
	app = newTestApp(t, nil)
	app.get("/", http.StatusUnauthorized, "Open this app from Telegram")
	initData := signInitData(t, time.Now(), `{"id":300,"username":"admin","first_name":"Ann"}`, false)
	resp, body := app.do(http.MethodGet, "/", nil, initDataHeader, initData)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "Hi, Ann") {
		t.Errorf("valid initData: status = %d\n%s", resp.StatusCode, body)
	}

	// A full page load gets the shell. It loads the page with htmx.
	req := httptest.NewRequest(http.MethodGet, "/episodes/1?x=1", nil)
	rec := httptest.NewRecorder()
	app.server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `hx-get="/episodes/1?x=1"`) {
		t.Errorf("shell: status = %d\n%s", rec.Code, rec.Body.String())
	}
}

func TestCovers(t *testing.T) {
	app := newTestApp(t, nil) // an <img> sends no initData
	app.seed("100")

	rec := httptest.NewRecorder()
	app.server.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/covers/c1.jpg", nil))
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/jpeg" ||
		!strings.Contains(rec.Header().Get("Cache-Control"), "immutable") || rec.Body.Len() != 3 {
		t.Errorf("cover: status = %d, headers = %v, %d bytes", rec.Code, rec.Header(), rec.Body.Len())
	}

	rec = httptest.NewRecorder()
	app.server.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/covers/unknown.jpg", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown cover: status = %d, want 404", rec.Code)
	}
}

func TestDeleteLink(t *testing.T) {
	app := newTestApp(t, &DevUser{ID: "100", Username: "admin"})
	app.seed("100")
	app.get("/requests/req1", 200, "Delete the link and its 3 episodes", "The episodes that are not ready stop.")

	resp, _ := app.post("/requests/req1/delete", url.Values{})
	if resp.Header.Get("HX-Replace-Url") != "/" || !strings.Contains(resp.Header.Get("HX-Trigger"), "Deleted 3 episodes") {
		t.Errorf("delete link: replace = %q, trigger = %q", resp.Header.Get("HX-Replace-Url"), resp.Header.Get("HX-Trigger"))
	}
	if eps, _ := app.svc.ListUserEpisodes(context.Background(), "100"); len(eps) != 0 {
		t.Errorf("episodes after delete = %d, want 0", len(eps))
	}
}

func TestLinkFromFeedPage(t *testing.T) {
	app := newTestApp(t, &DevUser{ID: "100", Username: "admin"})
	app.seed("100") // the default feed and "Books" (2)

	resp, _ := app.post("/links", url.Values{"url": {"magnet:?xt=urn:btih:abc&dn=The+Book"}, "feed": {"2"}})
	path := location(t, resp)
	if !strings.HasPrefix(path, "/new/") || !strings.HasSuffix(path, "?feed=2") {
		t.Fatalf("HX-Location = %q, want the new page for feed 2", path)
	}

	link := strings.TrimSuffix(strings.TrimPrefix(path, "/new/"), "?feed=2")
	var body string
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(body, "data-file") {
		if time.Now().After(deadline) {
			t.Fatalf("the file list did not load:\n%s", body)
		}
		time.Sleep(10 * time.Millisecond)
		body = app.get("/new/"+link+"/files?feed=2", 200)
	}
	if !strings.Contains(body, `name="feed" value="2" checked`) || strings.Contains(body, `name="feed" value="1" checked`) {
		t.Errorf("only feed 2 must be selected:\n%s", body)
	}
}

func TestEpisodesPages(t *testing.T) {
	app := newTestApp(t, &DevUser{ID: "100", Username: "admin"})
	ctx := context.Background()
	now := time.Now().UTC()
	for i := 1; i <= episodesPageSize+5; i++ {
		id := strconv.Itoa(i)
		if _, err := app.repo.SaveEpisode(ctx, &service.Episode{
			ID: id, UserID: "100", Title: "Episode " + id, CreatedAt: now.Add(time.Duration(i) * time.Minute), UpdatedAt: now,
			Status: "", // an old episode without a status
		}); err != nil {
			t.Fatal(err)
		}
	}

	// The loader of the next page must not go into the history.
	body := app.get("/episodes", 200, `offset=50" hx-trigger="revealed" hx-target="this" hx-swap="outerHTML" hx-push-url="false"`)
	if strings.Contains(body, "Episode 5<") {
		t.Error("the first page has the oldest episodes")
	}
	app.get("/episodes?offset=50", 200, "Episode 5<")

	// A negative offset is the first page, not a crash.
	app.get("/episodes?offset=-1", 200, "Episodes</h1>", "Episode 55<")

	// Old episodes without a status are ready, not in progress, and their page does not poll.
	app.get("/episodes?show=active", 200, "No episodes here.")
	if body := app.get("/episodes/1", 200); strings.Contains(body, `hx-trigger="every 2s"`) {
		t.Error("the page of an old episode polls")
	}
}
