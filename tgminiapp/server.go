// Package tgminiapp is the Telegram Mini App: server-rendered HTML pages with htmx.
//
// The Mini App gets the user identity only as initData in JavaScript, so a full page
// load cannot be authenticated. Each full page load gets the same shell page. The
// shell loads the content of the page with htmx, and htmx sends the initData in a
// header with each request. All page handlers return HTML fragments.
package tgminiapp

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel/trace"
	"tg-podcastotron/auth"
	"tg-podcastotron/service"
	"tg-podcastotron/telemetry"
)

//go:embed templates static
var assets embed.FS

const initDataHeader = "X-Telegram-Init-Data"

// Config configures the Mini App server.
type Config struct {
	BotToken string
	// BotUsername makes the invite links: https://t.me/<BotUsername>?start=inv_<token>
	BotUsername string
	// InitDataMaxAge limits how long one opening of the Mini App works.
	InitDataMaxAge time.Duration
	// DevUser, if set, is the user of requests that have no initData. Use it only
	// in development, to open the app in a normal browser.
	DevUser *DevUser
}

// DevUser is a Telegram user for development.
type DevUser struct {
	ID       string
	Username string
}

// User is the authenticated user of a request.
type User struct {
	ID        string
	Username  string
	FirstName string
	IsAdmin   bool
}

type Server struct {
	cfg      Config
	svc      *service.Service
	auth     *auth.Service
	logger   *slog.Logger
	shell    *template.Template
	pages    map[string]*template.Template
	partials *template.Template
	metadata *metadataCache
	gcScans  gcPendingScans
	mux      *http.ServeMux
	now      func() time.Time
	// assetsVersion changes when the static files change. The shell adds it to their URLs.
	assetsVersion string
}

func New(cfg Config, svc *service.Service, authSvc *auth.Service, logger *slog.Logger) (*Server, error) {
	if cfg.InitDataMaxAge == 0 {
		cfg.InitDataMaxAge = 24 * time.Hour
	}
	s := &Server{
		cfg:      cfg,
		svc:      svc,
		auth:     authSvc,
		logger:   logger,
		metadata: newMetadataCache(svc.FetchMetadata),
		now:      time.Now,
	}
	if err := s.parseTemplates(); err != nil {
		return nil, err
	}
	version, err := hashFiles(assets, "static")
	if err != nil {
		return nil, err
	}
	s.assetsVersion = version
	s.mux = s.routes()
	return s, nil
}

func (s *Server) parseTemplates() error {
	var err error
	s.shell, err = template.New("shell.html").Funcs(templateFuncs).ParseFS(assets, "templates/shell.html")
	if err != nil {
		return fmt.Errorf("failed to parse shell: %w", err)
	}
	s.partials, err = template.New("partials").Funcs(templateFuncs).ParseFS(assets, "templates/partials/*.html")
	if err != nil {
		return fmt.Errorf("failed to parse partials: %w", err)
	}
	files, err := fs.Glob(assets, "templates/pages/*.html")
	if err != nil {
		return err
	}
	s.pages = make(map[string]*template.Template, len(files))
	for _, f := range files {
		t, err := template.Must(s.partials.Clone()).ParseFS(assets, f)
		if err != nil {
			return fmt.Errorf("failed to parse %s: %w", f, err)
		}
		name := path.Base(f)
		s.pages[name[:len(name)-len(".html")]] = t
	}
	return nil
}

// Handler returns the HTTP handler of the Mini App.
func (s *Server) Handler() http.Handler {
	return s.mux
}

func (s *Server) routes() *http.ServeMux {
	mux := http.NewServeMux()

	static, _ := fs.Sub(assets, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", cacheFor(30*24*time.Hour, http.FileServerFS(static))))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) })
	mux.HandleFunc("GET /covers/{id}", s.cover)

	handle := func(pattern string, h handlerFunc) { mux.Handle(pattern, s.page(h)) }

	handle("GET /{$}", s.home)
	handle("GET /home/active", s.homeActive)
	handle("POST /links", s.createLink)
	handle("GET /new/{link}", s.newRequest)
	handle("GET /new/{link}/files", s.newRequestFiles)
	handle("POST /new/{link}", s.createRequest)
	handle("GET /requests/{id}", s.request)
	handle("GET /requests/{id}/progress", s.requestProgress)
	handle("POST /requests/{id}/feeds", s.requestFeeds)
	handle("POST /requests/{id}/delete", s.deleteRequest)

	handle("GET /episodes", s.episodes)
	handle("GET /episodes/bulk", s.bulk)
	handle("POST /episodes/bulk/feeds", s.bulkFeeds)
	handle("POST /episodes/bulk/rename", s.bulkRename)
	handle("POST /episodes/bulk/delete", s.bulkDelete)
	handle("GET /episodes/{id}", s.episode)
	handle("GET /episodes/{id}/status", s.episodeStatus)
	handle("POST /episodes/{id}/title", s.renameEpisode)
	handle("POST /episodes/{id}/feeds", s.episodeFeeds)
	handle("POST /episodes/{id}/delete", s.deleteEpisode)

	handle("GET /feeds/new", s.newFeed)
	handle("POST /feeds", s.createFeed)
	handle("GET /feeds/{id}", s.feed)
	handle("POST /feeds/{id}/title", s.renameFeed)
	handle("POST /feeds/{id}/permanent", s.setFeedPermanent)
	handle("POST /feeds/{id}/regenerate", s.regenerateFeed)
	handle("POST /feeds/{id}/remove", s.removeFromFeed)
	handle("POST /feeds/{id}/delete", s.deleteFeed)

	handle("GET /admin", s.admin)
	handle("POST /admin/invites", s.createInvite)
	handle("POST /admin/gc/scan", s.gcScan)
	handle("POST /admin/gc/delete", s.gcDelete)

	return mux
}

type handlerFunc func(w http.ResponseWriter, r *http.Request, u *User) error

// page serves the shell for full page loads, and authenticates htmx requests.
func (s *Server) page(h handlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("HX-Request") != "true" {
			if r.Method != http.MethodGet {
				http.Error(w, "use the Mini App", http.StatusBadRequest)
				return
			}
			s.serveShell(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Vary", "HX-Request")

		u, err := s.authenticate(r)
		if err != nil {
			s.renderNoAccess(w, r, err)
			return
		}
		trace.SpanFromContext(r.Context()).SetAttributes(telemetry.AttrUserID(u.ID))
		if err := h(w, r, u); err != nil {
			s.renderError(w, r, err)
		}
	})
}

func (s *Server) serveShell(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Vary", "HX-Request")
	path := r.URL.RequestURI()
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
		path = "/" // the shell must load a page of this app, not of another host
	}
	if err := s.shell.Execute(w, map[string]any{"Path": path, "V": s.assetsVersion}); err != nil {
		s.logger.ErrorContext(r.Context(), "failed to render shell", slog.Any("error", err))
	}
}

func (s *Server) authenticate(r *http.Request) (*User, error) {
	ctx := r.Context()
	initData := r.Header.Get(initDataHeader)
	var u *User
	if initData == "" && s.cfg.DevUser != nil {
		u = &User{ID: s.cfg.DevUser.ID, Username: s.cfg.DevUser.Username, FirstName: s.cfg.DevUser.Username}
	} else {
		tu, err := validateInitData(initData, s.cfg.BotToken, s.now(), s.cfg.InitDataMaxAge)
		if err != nil {
			return nil, err
		}
		u = &User{ID: strconv.FormatInt(tu.ID, 10), Username: tu.Username, FirstName: tu.FirstName}
	}

	ok, err := s.auth.IsAuthenticated(ctx, u.ID, u.Username)
	if err != nil {
		return nil, fmt.Errorf("failed to check access: %w", err)
	}
	if !ok {
		return nil, errNoAccess
	}
	u.IsAdmin, _ = s.auth.IsAdmin(ctx, u.Username)
	return u, nil
}

var errNoAccess = errors.New("no access")

// userError is an error with a message for the user.
type userError struct {
	status int
	msg    string
}

func (e *userError) Error() string { return e.msg }

func badRequest(format string, args ...any) error {
	return &userError{http.StatusBadRequest, fmt.Sprintf(format, args...)}
}

func notFound(what string) error {
	return &userError{http.StatusNotFound, what + " not found. It was maybe deleted."}
}

func (s *Server) renderNoAccess(w http.ResponseWriter, r *http.Request, err error) {
	status, reason := http.StatusUnauthorized, "session"
	switch {
	case errors.Is(err, errNoAccess):
		status, reason = http.StatusForbidden, "no-access"
	case errors.Is(err, errInitDataMissing):
		reason = "not-telegram"
	case errors.Is(err, errInitDataExpired):
		reason = "expired"
	case errors.Is(err, errInitDataInvalid):
	default:
		s.logger.ErrorContext(r.Context(), "failed to authenticate", slog.Any("error", err))
		status, reason = http.StatusInternalServerError, "error"
	}
	w.WriteHeader(status)
	s.renderPartial(w, r, "no-access", map[string]any{"Reason": reason})
}

// renderError shows the error in the toast of the shell.
func (s *Server) renderError(w http.ResponseWriter, r *http.Request, err error) {
	var ue *userError
	msg, status := "", http.StatusInternalServerError
	switch {
	case errors.As(err, &ue):
		msg, status = ue.msg, ue.status
	case errors.Is(err, service.ErrEpisodeNotFound):
		msg, status = "Episode not found. It was maybe deleted.", http.StatusNotFound
	case errors.Is(err, service.ErrFeedNotFound):
		msg, status = "Feed not found. It was maybe deleted.", http.StatusNotFound
	default:
		id := trace.SpanContextFromContext(r.Context()).TraceID().String()
		telemetry.RecordError(trace.SpanFromContext(r.Context()), err)
		s.logger.ErrorContext(r.Context(), "request failed", slog.String("path", r.URL.Path), slog.Any("error", err))
		msg = "Something went wrong. Try again later. Error ID: " + id
	}
	w.Header().Set("HX-Retarget", "#toast")
	w.Header().Set("HX-Reswap", "innerHTML")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(template.HTMLEscapeString(msg)))
}

func (s *Server) render(w http.ResponseWriter, page string, data any) error {
	t, ok := s.pages[page]
	if !ok {
		return fmt.Errorf("unknown page %q", page)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, "page", data); err != nil {
		return fmt.Errorf("failed to render %s: %w", page, err)
	}
	return nil
}

func (s *Server) renderPartial(w http.ResponseWriter, r *http.Request, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.partials.ExecuteTemplate(w, name, data); err != nil {
		s.logger.ErrorContext(r.Context(), "failed to render partial", slog.String("name", name), slog.Any("error", err))
	}
}

func (s *Server) partial(w http.ResponseWriter, name string, data any) error {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.partials.ExecuteTemplate(w, name, data); err != nil {
		return fmt.Errorf("failed to render %s: %w", name, err)
	}
	return nil
}

// replacePage answers with the page at path, and replaces the current page and its
// history entry with it. Use it after a step that is finished (a form was sent, or
// the page was deleted): then Back goes to the screen before, not to the old step.
func (s *Server) replacePage(w http.ResponseWriter, r *http.Request, path string) error {
	u, err := url.Parse(path)
	if err != nil {
		return err
	}
	req := r.Clone(r.Context())
	req.Method = http.MethodGet
	req.URL = u
	req.RequestURI = path
	req.Body = http.NoBody
	req.ContentLength = 0
	req.Form, req.PostForm, req.MultipartForm = nil, nil, nil
	req.Header.Del("Content-Type")

	w.Header().Set("HX-Retarget", "#main")
	w.Header().Set("HX-Reswap", "innerHTML show:window:top")
	w.Header().Set("HX-Replace-Url", path)
	s.mux.ServeHTTP(w, req)
	return nil
}

// navigate tells htmx to load the page at path into the main area, and to add it to the history.
func navigate(w http.ResponseWriter, path string) {
	b, _ := json.Marshal(map[string]string{"path": path, "target": "#main"})
	w.Header().Set("HX-Location", string(b))
}

// toast tells the shell to show a short message.
func toast(w http.ResponseWriter, msg string) {
	b, _ := json.Marshal(map[string]string{"toast": msg})
	w.Header().Set("HX-Trigger", string(b))
}

func cacheFor(d time.Duration, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", int(d.Seconds())))
		h.ServeHTTP(w, r)
	})
}

// hashFiles returns a short hash of the contents of all files in dir.
func hashFiles(fsys fs.FS, dir string) (string, error) {
	h := sha256.New()
	err := fs.WalkDir(fsys, dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		h.Write([]byte(p))
		h.Write(b)
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("failed to hash static files: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil))[:12], nil
}

// cover serves a cover picture. It needs no initData: an <img> cannot send it, and
// podcast apps load the covers of the feeds. The ID is random, like the S3 keys of
// the episode files, so nobody can guess it. A cover never changes.
func (s *Server) cover(w http.ResponseWriter, r *http.Request) {
	cover, err := s.svc.GetCover(r.Context(), r.PathValue("id"))
	if err != nil {
		s.logger.ErrorContext(r.Context(), "failed to get cover", slog.Any("error", err))
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	if cover == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", cover.MIME)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	_, _ = w.Write(cover.Data)
}

// Prefetch starts to get the file list of a link, so that the Mini App can show it sooner.
func (s *Server) Prefetch(ctx context.Context, url string) {
	s.metadata.start(ctx, url)
}
