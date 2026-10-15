// Package app serves the Start page and the save endpoint over HTTP.
package app

import (
	"context"
	"crypto/subtle"
	"embed"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/afrieirham/bookmark-manager/internal/domain"
)

//go:embed templates/*.html
var templatesFS embed.FS

// staticFS carries the PWA files: the manifest, the service worker, and the
// icons. They are served so the app is installable once the operator serves
// trusted HTTPS; over plain HTTP the browser simply withholds install and share.
//
//go:embed static
var staticFS embed.FS

// Fetcher looks up a page's Title. It never fails a request: ok reports whether
// a Title was found, and the caller falls back to the host when it was not.
type Fetcher interface {
	Title(ctx context.Context, url string) (string, bool)
}

// FetcherFunc adapts a function to the Fetcher interface.
type FetcherFunc func(ctx context.Context, url string) (string, bool)

// Title implements Fetcher.
func (f FetcherFunc) Title(ctx context.Context, url string) (string, bool) { return f(ctx, url) }

// Store is the persistence the Start page needs.
type Store interface {
	List(ctx context.Context) ([]domain.Bookmark, error)
	Get(ctx context.Context, id int64) (domain.Bookmark, bool, error)
	Insert(ctx context.Context, b domain.Bookmark) (domain.Bookmark, error)
	Rename(ctx context.Context, id int64, title string) error
	Delete(ctx context.Context, id int64) error
}

// Config is everything the Server takes from the environment.
type Config struct {
	// BaseURL overrides the request-derived base URL when the app runs behind a
	// proxy that presents a different public address. Empty derives it from each
	// request instead.
	BaseURL string
	// Token is the shared secret every request must carry. It is ignored when
	// AuthDisabled is set.
	Token string
	// AuthDisabled turns authentication off, for a network already trusted.
	AuthDisabled bool
}

// Server holds the dependencies the handlers share.
type Server struct {
	store   Store
	fetcher Fetcher
	cfg     Config
	tmpl    *template.Template
	static  http.Handler
	now     func() time.Time
}

// New builds a Server from its dependencies and configuration.
func New(store Store, fetcher Fetcher, cfg Config) *Server {
	staticDir, err := fs.Sub(staticFS, "static")
	if err != nil {
		// The embedded path is fixed at build time, so this cannot fail at run time.
		panic(err)
	}
	return &Server{
		store:   store,
		fetcher: fetcher,
		cfg:     cfg,
		tmpl:    template.Must(template.ParseFS(templatesFS, "templates/*.html")),
		static:  http.StripPrefix(staticPrefix, http.FileServer(http.FS(staticDir))),
		now:     time.Now,
	}
}

// Handler returns the HTTP routes. The PWA files are registered first, ahead of
// the /{url...} catch-all that #18 adds last, so a bare-URL save can never
// swallow them; known routes win over the wildcard either way.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+manifestPath, s.handleManifest)
	mux.HandleFunc("GET "+serviceWorkerPath, s.handleServiceWorker)
	mux.Handle("GET "+staticPrefix, s.static)
	mux.HandleFunc("/", s.handleStart)
	mux.HandleFunc("/save", s.handleSave)
	mux.HandleFunc("POST /rename", s.handleRename)
	mux.HandleFunc("POST /delete", s.handleDelete)
	mux.HandleFunc("GET "+loginPath, s.handleLoginForm)
	mux.HandleFunc("POST "+loginPath, s.handleLogin)
	return s.authenticate(mux)
}

// PWA routes. The service worker must sit at the origin root so its scope is
// "/"; a file under /static/ could only ever control /static/.
const (
	manifestPath      = "/manifest.webmanifest"
	serviceWorkerPath = "/sw.js"
	staticPrefix      = "/static/"
)

// handleManifest serves the web app manifest, which carries the Android
// share_target that maps a shared link onto /save.
func (s *Server) handleManifest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/manifest+json")
	serveEmbedded(w, "static/manifest.webmanifest")
}

// handleServiceWorker serves the pass-through service worker from the root, so
// it claims scope "/" and the browser offers to install the app.
func (s *Server) handleServiceWorker(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	serveEmbedded(w, "static/sw.js")
}

// serveEmbedded writes a file that shipped inside the binary. A miss can only
// mean a build error, so it is a 500 rather than a 404.
func serveEmbedded(w http.ResponseWriter, name string) {
	b, err := fs.ReadFile(staticFS, name)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	_, _ = w.Write(b)
}

type startData struct {
	Bookmarks   []domain.Bookmark
	Pending     *domain.Bookmark
	Duplicate   bool
	Error       string
	BaseURL     string
	Bookmarklet template.URL
}

func (s *Server) handleStart(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	bookmarks, err := s.store.List(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	base := s.baseURLFor(r)
	data := startData{
		Bookmarks:   bookmarks,
		BaseURL:     base,
		Bookmarklet: bookmarklet(base),
	}
	if idStr := r.URL.Query().Get("saved"); idStr != "" {
		if id, err := strconv.ParseInt(idStr, 10, 64); err == nil {
			if b, ok, err := s.store.Get(r.Context(), id); err == nil && ok {
				data.Pending = &b
				data.Duplicate = r.URL.Query().Get("dup") == "1"
			}
		}
	}
	if r.URL.Query().Get("error") != "" {
		data.Error = "That does not look like a URL."
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "start.html", data); err != nil {
		log.Printf("app: render Start page: %v", err)
	}
}

func (s *Server) handleSave(w http.ResponseWriter, r *http.Request) {
	nu, ok := domain.NormalizeURL(r.URL.Query().Get("u"))
	if !ok {
		http.Redirect(w, r, "/?error=invalid", http.StatusSeeOther)
		return
	}

	given := r.URL.Query().Get("title")
	fetched, fetchedOK := "", false
	if strings.TrimSpace(given) == "" {
		fetched, fetchedOK = s.fetcher.Title(r.Context(), nu)
	}

	existing, err := s.store.List(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	res, ok := domain.Save(existing, nu, given, fetched, fetchedOK, s.now())
	if !ok {
		http.Redirect(w, r, "/?error=invalid", http.StatusSeeOther)
		return
	}
	if res.Duplicate {
		http.Redirect(w, r, savedLocation(res.Bookmark.ID, true), http.StatusSeeOther)
		return
	}

	saved, err := s.store.Insert(r.Context(), res.Bookmark)
	if err != nil {
		s.fail(w, err)
		return
	}
	http.Redirect(w, r, savedLocation(saved.ID, false), http.StatusSeeOther)
}

func savedLocation(id int64, duplicate bool) string {
	loc := "/?saved=" + strconv.FormatInt(id, 10)
	if duplicate {
		loc += "&dup=1"
	}
	return loc
}

// handleRename applies a new Title to a Bookmark. It is the plain form target
// the Start page posts to, so it works with JS off; an inline fetch uses the
// same endpoint. A blank Title is ignored (the Bookmark is left alone).
func (s *Server) handleRename(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PostFormValue("id"), 10, 64)
	if err != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	b, ok, err := s.store.Get(r.Context(), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	if !ok {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if renamed, applied := domain.Rename(b, r.PostFormValue("title")); applied {
		if err := s.store.Rename(r.Context(), id, renamed.Title); err != nil {
			s.fail(w, err)
			return
		}
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// handleDelete removes a Bookmark. Like handleRename it backs the plain form
// fallback and the inline fetch alike.
func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	if id, err := strconv.ParseInt(r.PostFormValue("id"), 10, 64); err == nil {
		if err := s.store.Delete(r.Context(), id); err != nil {
			s.fail(w, err)
			return
		}
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// scheme reports the scheme the client used, as seen through a proxy.
// X-Forwarded-Proto wins over the transport scheme.
func (s *Server) scheme(r *http.Request) string {
	if p := r.Header.Get("X-Forwarded-Proto"); p != "" {
		return p
	}
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

// baseURLFor builds the app's public base URL from the request, so links are
// correct behind a proxy.
func (s *Server) baseURLFor(r *http.Request) string {
	if s.cfg.BaseURL != "" {
		return strings.TrimRight(s.cfg.BaseURL, "/")
	}
	return s.scheme(r) + "://" + r.Host
}

func bookmarklet(base string) template.URL {
	js := "javascript:(function(){window.open('" + base +
		"/save?u='+encodeURIComponent(location.href)+'&title='+encodeURIComponent(document.title),'_blank')})()"
	return template.URL(js)
}

func (s *Server) fail(w http.ResponseWriter, err error) {
	log.Printf("app: %v", err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}

// loginPath is the one route reachable without the shared secret, so a device
// has somewhere to enter the Token.
const loginPath = "/login"

// cookieName is the cookie a device keeps once it has entered the Token.
const cookieName = "bm_token"

// authenticate wraps next so that every route needs the shared secret: the
// Authorization header for API clients, the cookie for the web UI. The login
// page and the PWA files stay open, or there would be no way in and no way to
// install.
func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.AuthDisabled || s.isPublic(r.URL.Path) || s.authenticated(r) {
			next.ServeHTTP(w, r)
			return
		}
		s.refuse(w, r)
	})
}

// isPublic reports whether a path is served without the shared secret. Besides
// the login page, these are the PWA files. The manifest is fetched with
// credentials mode "omit", so a browser sends no cookie for it: gating it behind
// the Token would 401 that fetch and installability would never happen. The
// manifest, service worker and icons hold no Bookmark data, so there is nothing
// to protect. Saving stays gated: the share target lands on /save, which a
// browser reaches as a top-level navigation that does carry the cookie.
func (s *Server) isPublic(path string) bool {
	switch path {
	case loginPath, manifestPath, serviceWorkerPath:
		return true
	}
	return strings.HasPrefix(path, staticPrefix)
}

// authenticated reports whether the request carries the shared secret, either
// as a bearer token or as the cookie a previous login set. Comparison is
// constant-time, so a wrong Token leaks nothing through how long rejection took.
func (s *Server) authenticated(r *http.Request) bool {
	if s.cfg.Token == "" {
		return false
	}
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		if equalSecret(strings.TrimPrefix(h, "Bearer "), s.cfg.Token) {
			return true
		}
	}
	if c, err := r.Cookie(cookieName); err == nil && equalSecret(c.Value, s.cfg.Token) {
		return true
	}
	return false
}

func equalSecret(got, want string) bool {
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// refuse turns away an unauthenticated request. A browser is sent to the login
// page; a client asking for JSON, or using a method a redirect would not
// survive, gets a plain 401 instead.
func (s *Server) refuse(w http.ResponseWriter, r *http.Request) {
	if wantsJSON(r) || r.Method != http.MethodGet {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	http.Redirect(w, r, loginPath, http.StatusSeeOther)
}

// wantsJSON reports whether the client asked for JSON rather than HTML.
func wantsJSON(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "application/json")
}

// handleLoginForm shows the one-time Token entry.
func (s *Server) handleLoginForm(w http.ResponseWriter, r *http.Request) {
	if s.cfg.AuthDisabled || s.authenticated(r) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "login.html", loginData{
		Error: r.URL.Query().Get("error") != "",
	}); err != nil {
		log.Printf("app: render login page: %v", err)
	}
}

// handleLogin checks the entered Token and, when it matches, sets the cookie
// that authenticates later visits from this device.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Token == "" || !equalSecret(r.PostFormValue("token"), s.cfg.Token) {
		http.Redirect(w, r, loginPath+"?error=1", http.StatusSeeOther)
		return
	}
	http.SetCookie(w, s.sessionCookie(r, s.cfg.Token))
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// sessionCookie is the cookie that remembers the Token on a device.
func (s *Server) sessionCookie(r *http.Request, token string) *http.Cookie {
	return &http.Cookie{
		Name:  cookieName,
		Value: token,
		Path:  "/",
		// HttpOnly keeps the Token away from any script on the page.
		HttpOnly: true,
		// Lax, not Strict: the bookmarklet and the Android share target arrive as
		// cross-site top-level GETs, and Strict would withhold the cookie from them.
		SameSite: http.SameSiteLaxMode,
		// Secure only when the request really was https. Setting it over the plain
		// HTTP the MVP runs on would make the browser drop the cookie entirely.
		Secure: s.scheme(r) == "https",
		MaxAge: 60 * 60 * 24 * 365,
	}
}

type loginData struct {
	Error bool
}
