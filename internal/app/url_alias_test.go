package app_test

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

// TestVisitingABareURLSavesIt covers the catch-all alias: reaching the app with
// the URL in the path saves it exactly as /save?u= would.
func TestVisitingABareURLSavesIt(t *testing.T) {
	h := newHandler(t, filepath.Join(t.TempDir(), "db.sqlite"), stubFetcher{})

	rr := get(t, h, "/example.com", nil)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rr.Code)
	}
	if loc := rr.Header().Get("Location"); loc != "/?saved=1" {
		t.Fatalf("Location = %q, want %q", loc, "/?saved=1")
	}
	page := get(t, h, rr.Header().Get("Location"), nil).Body.String()
	if !strings.Contains(page, "Saved — rename it?") {
		t.Errorf("visiting a bare URL does not show the pending-rename result:\n%s", page)
	}
	if !strings.Contains(page, "example.com") {
		t.Errorf("the bare URL was not saved:\n%s", page)
	}
}

// TestVisitingABareURLKeepsTheWholePath checks the alias does not save just the
// first segment.
func TestVisitingABareURLKeepsTheWholePath(t *testing.T) {
	h := newHandler(t, filepath.Join(t.TempDir(), "db.sqlite"), stubFetcher{})

	rr := get(t, h, "/example.com/deep/page", nil)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rr.Code)
	}
	page := get(t, h, rr.Header().Get("Location"), nil).Body.String()
	if !strings.Contains(page, "https://example.com/deep/page") {
		t.Errorf("the whole path was not saved:\n%s", page)
	}
}

// TestKnownRoutesAreNeverSavedAsURLs is the crux: the alias must never mistake
// one of the app's own routes for a URL. A GET to each known route must leave
// the grid empty.
func TestKnownRoutesAreNeverSavedAsURLs(t *testing.T) {
	targets := []string{
		"/", "/save", "/rename", "/delete", "/reorder", "/login",
		"/manifest.webmanifest", "/sw.js", "/static/icon-192.png",
	}
	for _, target := range targets {
		t.Run(target, func(t *testing.T) {
			h := newHandler(t, filepath.Join(t.TempDir(), "db.sqlite"), stubFetcher{})

			rr := get(t, h, target, nil)
			if loc := rr.Header().Get("Location"); strings.HasPrefix(loc, "/?saved=") {
				t.Fatalf("GET %s was treated as a URL to save (Location %q)", target, loc)
			}

			page := get(t, h, "/", nil).Body.String()
			if !strings.Contains(page, "No bookmarks yet") {
				t.Errorf("GET %s created a Bookmark:\n%s", target, page)
			}
		})
	}
}

// TestStartPageDocumentsThatAQueryURLUsesTheSaveEndpoint pins the one limit of
// the path alias: a URL carrying a query or fragment cannot survive as a path,
// so the Start page must send those to /save?u=.
func TestStartPageDocumentsThatAQueryURLUsesTheSaveEndpoint(t *testing.T) {
	h := newHandler(t, filepath.Join(t.TempDir(), "db.sqlite"), stubFetcher{})

	page := get(t, h, "/", nil).Body.String()
	if !strings.Contains(page, "A URL containing ? or # must use the paste box or /save?u=.") {
		t.Errorf("Start page does not document that ? and # URLs need /save?u=:\n%s", page)
	}
}
