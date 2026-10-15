// Package app serves the Start page and the save endpoint over HTTP.
package app

import (
	"context"
	"embed"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/afrieirham/bookmark-manager/internal/domain"
)

//go:embed templates/start.html
var templatesFS embed.FS

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
}

// Server holds the dependencies the handlers share.
type Server struct {
	store   Store
	fetcher Fetcher
	baseURL string
	tmpl    *template.Template
	now     func() time.Time
}

// New builds a Server. baseURL overrides the request-derived base URL when the
// app runs behind a proxy that presents a different public address; leave it
// empty to derive the base URL from each request.
func New(store Store, fetcher Fetcher, baseURL string) *Server {
	return &Server{
		store:   store,
		fetcher: fetcher,
		baseURL: baseURL,
		tmpl:    template.Must(template.ParseFS(templatesFS, "templates/start.html")),
		now:     time.Now,
	}
}

// Handler returns the HTTP routes for the Start page and save.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleStart)
	mux.HandleFunc("/save", s.handleSave)
	return mux
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
	if err := s.tmpl.Execute(w, data); err != nil {
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

// baseURLFor builds the app's public base URL from the request, so links are
// correct behind a proxy. X-Forwarded-Proto wins over the transport scheme.
func (s *Server) baseURLFor(r *http.Request) string {
	if s.baseURL != "" {
		return strings.TrimRight(s.baseURL, "/")
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if p := r.Header.Get("X-Forwarded-Proto"); p != "" {
		scheme = p
	}
	return scheme + "://" + r.Host
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
