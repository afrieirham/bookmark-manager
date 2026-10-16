package app_test

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/afrieirham/bookmark-manager/internal/app"
	"github.com/afrieirham/bookmark-manager/internal/domain"
)

// The probe holds no Token, so the route must answer before authentication:
// gating it would declare every deployment unhealthy while it serves fine.
func TestHealthzAnswersWithoutTheToken(t *testing.T) {
	h := newHandler(t, filepath.Join(t.TempDir(), "db.sqlite"), stubFetcher{})

	rr := getAnon(t, h, "/healthz", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /healthz status = %d, want 200", rr.Code)
	}
	if body := rr.Body.String(); !strings.Contains(body, "ok") {
		t.Errorf("GET /healthz body = %q, want it to report ok", body)
	}
}

// The route is public, so it must carry nothing an unauthenticated caller should
// not see: no Bookmark Titles, no URLs, not even a count.
func TestHealthzCarriesNoBookmarkData(t *testing.T) {
	h := newHandler(t, filepath.Join(t.TempDir(), "db.sqlite"), stubFetcher{title: "Private Page", ok: true})
	get(t, h, "/save?u=https%3A%2F%2Fsecret.example.com%2Fnotes", nil)

	body := getAnon(t, h, "/healthz", nil).Body.String()
	for _, leak := range []string{"Private Page", "secret.example.com", "notes"} {
		if strings.Contains(body, leak) {
			t.Errorf("GET /healthz body = %q, leaks %q", body, leak)
		}
	}
}

// /healthz is one of the app's own routes, so probing it must never save a
// Bookmark: the bare-URL catch-all would otherwise swallow it.
func TestHealthzIsNotSavedAsABookmark(t *testing.T) {
	h := newHandler(t, filepath.Join(t.TempDir(), "db.sqlite"), stubFetcher{})
	getAnon(t, h, "/healthz", nil)

	if page := get(t, h, "/", nil).Body.String(); !strings.Contains(page, "No bookmarks yet.") {
		t.Error("probing /healthz saved a Bookmark")
	}
}

// brokenStore is a Store whose reads fail, standing in for the failure that
// leaves the process up but unable to serve: an unmounted, read-only or full
// volume under the SQLite file. Only List is called, so embedding the interface
// covers the rest.
type brokenStore struct{ app.Store }

func (brokenStore) List(context.Context) ([]domain.Bookmark, error) {
	return nil, errors.New("store is unreachable")
}

// A health route that only proves the TCP port is open would report a
// half-broken deployment as healthy. It must report the store failure instead,
// so the orchestrator restarts and the operator sees it.
func TestHealthzReportsUnavailableWhenTheStoreCannotBeRead(t *testing.T) {
	h := app.New(brokenStore{}, stubFetcher{}, app.Config{AuthDisabled: true}).Handler()

	rr := getAnon(t, h, "/healthz", nil)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET /healthz status = %d, want 503", rr.Code)
	}
}
