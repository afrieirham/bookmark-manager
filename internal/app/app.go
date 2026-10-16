// Package app serves the Start page and the save endpoint over HTTP.
package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"net/url"
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

// FaviconFetcher optionally reads a site's conventional favicon when a
// Bookmark is added or its URL changes.
type FaviconFetcher interface {
	Favicon(ctx context.Context, url string) ([]byte, string, bool)
}

// FetcherFunc adapts a function to the Fetcher interface.
type FetcherFunc func(ctx context.Context, url string) (string, bool)

// Title implements Fetcher.
func (f FetcherFunc) Title(ctx context.Context, url string) (string, bool) { return f(ctx, url) }

// Store is the persistence the Start page needs.
type Store interface {
	List(ctx context.Context) ([]domain.Bookmark, error)
	Get(ctx context.Context, id int64) (domain.Bookmark, bool, error)
	Favicon(ctx context.Context, id int64) ([]byte, string, bool, error)
	Insert(ctx context.Context, b domain.Bookmark) (domain.Bookmark, error)
	Rename(ctx context.Context, id int64, title string) error
	Update(ctx context.Context, id int64, title, url string, favicon []byte, faviconType string) error
	Delete(ctx context.Context, id int64) error
	Reorder(ctx context.Context, ids []int64) error
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
		tmpl:    template.Must(template.New("start.html").Funcs(template.FuncMap{"initial": bookmarkInitial}).ParseFS(templatesFS, "templates/*.html")),
		static:  http.StripPrefix(staticPrefix, http.FileServer(http.FS(staticDir))),
		now:     time.Now,
	}
}

func bookmarkInitial(title string) string {
	for _, r := range strings.TrimSpace(title) {
		return strings.ToUpper(string(r))
	}
	return "?"
}

// Handler returns the HTTP routes. The fixed-path routes are registered first,
// ahead of the /{url...} catch-all that #18 adds last, so a bare-URL save can
// never swallow them; known routes win over the wildcard either way.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+healthPath, s.handleHealth)
	mux.HandleFunc("GET "+manifestPath, s.handleManifest)
	mux.HandleFunc("GET "+serviceWorkerPath, s.handleServiceWorker)
	mux.HandleFunc("GET /favicon/{id}", s.handleFavicon)
	mux.Handle("GET "+staticPrefix, s.static)
	// The exact root is its own pattern so the bare-URL catch-all below does not
	// swallow it: GET /{$} matches only "/", and a more specific pattern wins
	// over a wildcard. The catch-all is registered last, but precedence, not
	// order, is what keeps the named routes above it.
	mux.HandleFunc("GET /{$}", s.handleStart)
	// GET only, and named, so it outranks the /{url...} wildcard below: the mux
	// rejects two patterns where neither is strictly more specific, and a method-
	// less /save would be ambiguous with a GET wildcard.
	mux.HandleFunc("GET /save", s.handleSave)
	mux.HandleFunc("POST /rename", s.handleRename)
	mux.HandleFunc("POST /delete", s.handleDelete)
	mux.HandleFunc("POST /reorder", s.handleReorder)
	mux.HandleFunc("GET "+loginPath, s.handleLoginForm)
	mux.HandleFunc("POST "+loginPath, s.handleLogin)
	mux.HandleFunc("GET /{url...}", s.handleAlias)
	return s.authenticate(mux)
}

func (s *Server) handleFavicon(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		http.NotFound(w, r)
		return
	}
	data, mediaType, ok, err := s.store.Favicon(r.Context(), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", mediaType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Cache-Control", "private, no-cache")
	sum := sha256.Sum256(data)
	etag := `"` + hex.EncodeToString(sum[:]) + `"`
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	_, _ = w.Write(data)
}

// Routes served from a fixed path: the health probe and the PWA files. The
// service worker must sit at the origin root so its scope is "/"; a file under
// /static/ could only ever control /static/.
//
// Each is listed again in isKnownRoute and isPublic, which is the Shotgun
// Surgery #23 collapses into one table of routes; adding a path here means
// editing three places until then.
const (
	healthPath        = "/healthz"
	manifestPath      = "/manifest.webmanifest"
	serviceWorkerPath = "/sw.js"
	staticPrefix      = "/static/"
)

// handleHealth reports whether the app can serve, for an orchestrator's probe.
// It reads the store rather than returning a bare 200, so what it catches is the
// state that leaves the process running but broken — an unmounted, read-only or
// full volume under the SQLite file — instead of only proving the port is open.
// The body is fixed text, because the route is public: it carries nothing an
// unauthenticated caller should not see, not even how many Bookmarks exist.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if _, err := s.store.List(r.Context()); err != nil {
		log.Printf("app: health: %v", err)
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok\n"))
}

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
	Add         bool
	Error       string
	BaseURL     string
	Bookmarklet template.URL
}

func (s *Server) handleStart(w http.ResponseWriter, r *http.Request) {
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
		Add:         r.URL.Query().Get("add") == "1",
	}
	if idStr := r.URL.Query().Get("saved"); idStr != "" {
		if id, err := strconv.ParseInt(idStr, 10, 64); err == nil {
			if b, ok, err := s.store.Get(r.Context(), id); err == nil && ok {
				if title, exists := r.URL.Query()["title"]; exists {
					b.Title = title[0]
				}
				if rawURL, exists := r.URL.Query()["u"]; exists {
					b.URL = rawURL[0]
				}
				data.Pending = &b
				data.Duplicate = r.URL.Query().Get("dup") == "1"
			}
		}
	}
	switch r.URL.Query().Get("error") {
	case "invalid":
		data.Error = "Enter a valid name and URL."
	case "duplicate":
		data.Error = "That URL is already in your bookmarks."
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "start.html", data); err != nil {
		log.Printf("app: render Start page: %v", err)
	}
}

// handleSave is the canonical save endpoint: GET /save?u=<url>&title=<optional>.
func (s *Server) handleSave(w http.ResponseWriter, r *http.Request) {
	s.save(w, r, r.URL.Query().Get("u"), r.URL.Query().Get("title"))
}

// handleAlias is the bare-URL alias for /save?u=: reaching the app with the URL
// in the path saves it. The whole path is the URL, so /example.com/deep/page
// saves https://example.com/deep/page. A path cannot carry a query or fragment,
// so a URL with one must use /save?u= instead (the Start page says so). The
// app's own routes are never treated as URLs, or the catch-all would shadow
// them.
func (s *Server) handleAlias(w http.ResponseWriter, r *http.Request) {
	raw := r.PathValue("url")
	// A path cannot carry a query, so refusing beats quietly saving a different
	// URL than the one asked for: /example.com/s?q=1 cannot become
	// https://example.com/s.
	if isKnownRoute(raw) || r.URL.RawQuery != "" || !looksLikeHost(raw) {
		http.NotFound(w, r)
		return
	}
	s.save(w, r, raw, "")
}

// looksLikeHost reports whether the path a bare-URL request carried names a
// host. A bare URL always has one, and this wildcard fires on every path the app
// does not serve, so without the guard a typo like /about would be saved as a
// Bookmark. "localhost" is allowed without a dot, since a LAN deployment may be
// reached by that name alone.
func looksLikeHost(path string) bool {
	raw := path
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := u.Hostname()
	return host == "localhost" || strings.Contains(host, ".")
}

// isKnownRoute reports whether the path a bare-URL request carried — with no
// leading slash, as the wildcard captures it — names one of the app's own
// routes. Most are matched by their own patterns before the wildcard reaches
// them; /rename, /delete and /reorder are POST-only, so a GET to one lands on
// the wildcard and must 404 rather than save.
func isKnownRoute(path string) bool {
	full := "/" + path
	switch full {
	case "/save", "/rename", "/delete", "/reorder", loginPath, healthPath, manifestPath, serviceWorkerPath:
		return true
	// Not routes the app serves, but paths a browser or a crawler asks for
	// unprompted. They are not URLs anyone meant to save, and the wildcard would
	// otherwise swallow them.
	case "/favicon.ico", "/robots.txt", "/sitemap.xml":
		return true
	}
	return strings.HasPrefix(full, staticPrefix) || strings.HasPrefix(full, "/favicon/")
}

// save stores raw as a Bookmark, the work /save and the /<url> alias share. An
// unusable URL and a duplicate are handled the same way from either entry.
func (s *Server) save(w http.ResponseWriter, r *http.Request, raw, given string) {
	nu, ok := domain.NormalizeURL(raw)
	if !ok {
		s.respondSaveError(w, r)
		return
	}

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
		s.respondSaveError(w, r)
		return
	}
	if res.Duplicate {
		s.respondSaved(w, r, res.Bookmark.ID, true)
		return
	}
	if iconFetcher, ok := s.fetcher.(FaviconFetcher); ok {
		res.Bookmark.Favicon, res.Bookmark.FaviconType, _ = iconFetcher.Favicon(r.Context(), nu)
	}

	saved, err := s.store.Insert(r.Context(), res.Bookmark)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.respondSaved(w, r, saved.ID, false)
}

// respondSaved reports a completed save. A browser goes back to the Start page
// showing the pending-rename prompt, flagged when the URL was already saved; a
// client asking for JSON gets the Bookmark's id instead, since it has no page to
// return to and needs the id to act on. A duplicate still returns the existing
// id and marks the result so a script can keep its form open with feedback.
func (s *Server) respondSaved(w http.ResponseWriter, r *http.Request, id int64, duplicate bool) {
	if wantsJSON(r) {
		writeJSON(w, saveResult{OK: true, ID: id, Duplicate: duplicate}, http.StatusOK)
		return
	}
	http.Redirect(w, r, savedLocation(id, duplicate), http.StatusSeeOther)
}

// respondSaveError reports a URL that cannot be saved. A JSON client gets a 400
// rather than a redirect to an HTML page it cannot read.
func (s *Server) respondSaveError(w http.ResponseWriter, r *http.Request) {
	if wantsJSON(r) {
		writeJSON(w, saveResult{OK: false, Error: "invalid url"}, http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/?error=invalid", http.StatusSeeOther)
}

// saveResult is the machine-readable outcome of a save: {"ok":true,"id":n} when
// it worked, and an explanation instead when the URL was unusable.
type saveResult struct {
	OK        bool   `json:"ok"`
	ID        int64  `json:"id,omitempty"`
	Duplicate bool   `json:"duplicate,omitempty"`
	Error     string `json:"error,omitempty"`
}

func writeJSON(w http.ResponseWriter, v any, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("app: write JSON: %v", err)
	}
}

func savedLocation(id int64, duplicate bool) string {
	loc := "/?saved=" + strconv.FormatInt(id, 10)
	if duplicate {
		loc += "&dup=1"
	}
	return loc
}

// handleRename updates a Bookmark's title and URL. The plain form redirect is
// the no-JavaScript fallback; the dialog uses the JSON response to stay open
// when it needs to show a validation error.
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
	title := r.PostFormValue("title")
	rawURL := r.PostFormValue("u")
	renamed, applied := domain.Rename(b, title)
	if !applied {
		s.respondUpdateError(w, r, id, title, rawURL, http.StatusBadRequest, "Enter a name and URL.", "invalid")
		return
	}
	if rawURL == "" {
		if err := s.store.Rename(r.Context(), id, renamed.Title); err != nil {
			s.fail(w, err)
			return
		}
	} else {
		normalizedURL, valid := domain.NormalizeURL(rawURL)
		if !valid {
			s.respondUpdateError(w, r, id, title, rawURL, http.StatusBadRequest, "Enter a valid URL.", "invalid")
			return
		}
		bookmarks, err := s.store.List(r.Context())
		if err != nil {
			s.fail(w, err)
			return
		}
		for _, other := range bookmarks {
			if other.ID != id && other.URL == normalizedURL {
				s.respondUpdateError(w, r, id, title, rawURL, http.StatusConflict, "That URL is already in your bookmarks.", "duplicate")
				return
			}
		}
		if normalizedURL == b.URL {
			if err := s.store.Rename(r.Context(), id, renamed.Title); err != nil {
				s.fail(w, err)
				return
			}
		} else {
			var favicon []byte
			var faviconType string
			if iconFetcher, ok := s.fetcher.(FaviconFetcher); ok {
				favicon, faviconType, _ = iconFetcher.Favicon(r.Context(), normalizedURL)
			}
			if err := s.store.Update(r.Context(), id, renamed.Title, normalizedURL, favicon, faviconType); err != nil {
				s.fail(w, err)
				return
			}
		}
	}
	if wantsJSON(r) {
		writeJSON(w, saveResult{OK: true, ID: id}, http.StatusOK)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) respondUpdateError(w http.ResponseWriter, r *http.Request, id int64, title, rawURL string, status int, message, code string) {
	if wantsJSON(r) {
		writeJSON(w, saveResult{OK: false, Error: message}, status)
		return
	}
	query := url.Values{
		"saved": {strconv.FormatInt(id, 10)},
		"error": {code},
		"title": {title},
		"u":     {rawURL},
	}
	http.Redirect(w, r, "/?"+query.Encode(), http.StatusSeeOther)
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

// handleReorder stores the grid order: the full list of Bookmark ids, in the
// order they should appear, sent as a repeated form field by the drag-and-drop
// script. The domain decides what the full order is, so a partial or stale list
// still leaves every Bookmark on the grid.
func (s *Server) handleReorder(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	existing, err := s.store.List(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}

	ids := make([]int64, 0, len(r.PostForm["ids"]))
	for _, raw := range r.PostForm["ids"] {
		if id, err := strconv.ParseInt(raw, 10, 64); err == nil {
			ids = append(ids, id)
		}
	}

	ordered := domain.Reorder(existing, ids)
	full := make([]int64, len(ordered))
	for i, b := range ordered {
		full[i] = b.ID
	}
	if err := s.store.Reorder(r.Context(), full); err != nil {
		s.fail(w, err)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// scheme reports the scheme the client used, as seen through a proxy.
// X-Forwarded-Proto wins over the transport scheme.
func (s *Server) scheme(r *http.Request) string {
	if p := forwardedProto(r.Header.Get("X-Forwarded-Proto")); p != "" {
		return p
	}
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

// forwardedProto reads the first hop of an X-Forwarded-Proto header, which a
// chain of proxies may have appended to ("https, http"). Only a scheme the app
// knows is returned: this value reaches the base URL the Start page renders and
// the cookie's Secure flag, so arbitrary text here would end up in both.
func forwardedProto(header string) string {
	first, _, _ := strings.Cut(header, ",")
	switch p := strings.ToLower(strings.TrimSpace(first)); p {
	case "http", "https":
		return p
	}
	return ""
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
	js := "javascript:(function(){window.open('" + jsString(base) +
		"/save?u='+encodeURIComponent(location.href)+'&title='+encodeURIComponent(document.title),'_blank')})()"
	return template.URL(js)
}

// jsString escapes s for use inside a single-quoted JavaScript string. base
// comes from the Host header, and the finished bookmarklet is a template.URL,
// which tells html/template to leave the href alone — so the escaping that would
// normally make this safe never runs, and a quote in the host would close the
// string and run whatever followed it.
func jsString(s string) string {
	return strings.NewReplacer(`\`, `\\`, "'", `\'`, "\n", `\n`, "\r", `\r`).Replace(s)
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
// the login page, these are the health and PWA routes. The health route is
// probed by an orchestrator that holds no Token, and gating it would report
// every deployment unhealthy. The manifest is fetched with credentials mode
// "omit", so a browser sends no cookie for it: gating it behind the Token would
// 401 that fetch and installability would never happen. None of them hold
// Bookmark data, so there is nothing to protect. Saving stays gated: the share
// target lands on /save, which a browser reaches as a top-level navigation that
// does carry the cookie.
func (s *Server) isPublic(path string) bool {
	switch path {
	case loginPath, healthPath, manifestPath, serviceWorkerPath:
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
	if c, err := r.Cookie(cookieName); err == nil && equalSecret(c.Value, SessionValue(s.cfg.Token)) {
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
	http.SetCookie(w, s.sessionCookie(r, SessionValue(s.cfg.Token)))
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// sessionContext separates this derivation from any other use of the Token as a
// key, so the cookie value cannot be confused with a value derived elsewhere.
const sessionContext = "bookmark-manager session v1"

// SessionValue is the value the session cookie carries for a given Token. It is
// derived rather than being the Token itself: a cookie is the credential most
// likely to be copied out of a browser, and the Token is also the operator's
// long-lived secret, which they may well have reused elsewhere. This value
// authenticates this app and nothing else, and rotating the Token retires every
// cookie issued under the old one.
func SessionValue(token string) string {
	mac := hmac.New(sha256.New, []byte(token))
	mac.Write([]byte(sessionContext))
	return hex.EncodeToString(mac.Sum(nil))
}

// sessionCookie is the cookie that keeps a device logged in after it has entered
// the Token once.
func (s *Server) sessionCookie(r *http.Request, value string) *http.Cookie {
	return &http.Cookie{
		Name:  cookieName,
		Value: value,
		Path:  "/",
		// HttpOnly keeps the credential away from any script on the page.
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
