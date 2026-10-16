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
	if !strings.Contains(page, "Edit shortcut") {
		t.Errorf("visiting a bare URL does not open the edit dialog:\n%s", page)
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

// TestStartPageProvidesAURLFieldForQueryURLs ensures the shortcut dialog can
// save URLs containing a query or fragment through the /save endpoint.
func TestStartPageDocumentsThatAQueryURLUsesTheSaveEndpoint(t *testing.T) {
	h := newHandler(t, filepath.Join(t.TempDir(), "db.sqlite"), stubFetcher{})

	page := get(t, h, "/", nil).Body.String()
	if !strings.Contains(page, `name="u"`) || !strings.Contains(page, `action="/save"`) {
		t.Errorf("shortcut dialog does not submit the URL through /save:\n%s", page)
	}
}

// TestTheAliasRefusesAPathThatIsNotAHost guards the catch-all's sharp edge: it
// fires on any path the app does not serve, so a browser's or crawler's stray
// request, or a typo, would otherwise be saved as a Bookmark. A bare URL always
// names a host, so a path that does not is not one.
func TestTheAliasRefusesAPathThatIsNotAHost(t *testing.T) {
	for _, target := range []string{"/about", "/favicon.ico", "/robots.txt", "/sitemap.xml"} {
		t.Run(target, func(t *testing.T) {
			h := newHandler(t, filepath.Join(t.TempDir(), "db.sqlite"), stubFetcher{})

			if rr := get(t, h, target, nil); rr.Code != http.StatusNotFound {
				t.Errorf("status = %d, want 404", rr.Code)
			}
			if page := get(t, h, "/", nil).Body.String(); !strings.Contains(page, "No bookmarks yet") {
				t.Errorf("GET %s was saved as a Bookmark:\n%s", target, page)
			}
		})
	}
}

// TestTheAliasStillSavesAHostWithNoDot keeps the guard from being too eager: a
// LAN deployment may be reached by a single-label name.
func TestTheAliasStillSavesAHostWithNoDot(t *testing.T) {
	h := newHandler(t, filepath.Join(t.TempDir(), "db.sqlite"), stubFetcher{})

	rr := get(t, h, "/localhost:8080/x", nil)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rr.Code)
	}
	page := get(t, h, rr.Header().Get("Location"), nil).Body.String()
	if !strings.Contains(page, "https://localhost:8080/x") {
		t.Errorf("a single-label host was not saved:\n%s", page)
	}
}

// TestTheAliasRefusesAQueryRatherThanDroppingIt pins that the alias does not
// quietly save a different URL than the one asked for. A path cannot carry a
// query, so /example.com/s?q=1 cannot become https://example.com/s.
func TestTheAliasRefusesAQueryRatherThanDroppingIt(t *testing.T) {
	h := newHandler(t, filepath.Join(t.TempDir(), "db.sqlite"), stubFetcher{})

	if rr := get(t, h, "/example.com/s?q=1", nil); rr.Code == http.StatusSeeOther {
		t.Errorf("status = %d, want the query refused rather than dropped", rr.Code)
	}
	if page := get(t, h, "/", nil).Body.String(); !strings.Contains(page, "No bookmarks yet") {
		t.Errorf("a URL with its query dropped was saved:\n%s", page)
	}
}
