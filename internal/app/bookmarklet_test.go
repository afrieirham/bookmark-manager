package app_test

import (
	"html"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/afrieirham/bookmark-manager/internal/app"
)

// bookmarkletHref reads the Save-to-app link's target off the Start page, in the
// form a browser would run it. html/template escapes the attribute and
// percent-encodes the javascript: URL, and a browser undoes both before it
// evaluates the script, so the tests do the same rather than asserting on the
// encoded text.
func bookmarkletHref(t *testing.T, page string) string {
	t.Helper()
	_, after, ok := strings.Cut(page, `class="bmk" href="`)
	if !ok {
		t.Fatalf("Start page has no Save-to-app link:\n%s", page)
	}
	href, _, ok := strings.Cut(after, `"`)
	if !ok {
		t.Fatalf("Save-to-app link has no href:\n%s", page)
	}
	decoded, err := url.PathUnescape(html.UnescapeString(href))
	if err != nil {
		t.Fatalf("bookmarklet href is not a valid URL (%v): %q", err, href)
	}
	return decoded
}

func TestStartPageShowsADraggableSaveLink(t *testing.T) {
	h := newHandler(t, filepath.Join(t.TempDir(), "db.sqlite"), stubFetcher{})

	page := get(t, h, "/", nil).Body.String()
	_, after, ok := strings.Cut(page, `class="bmk"`)
	if !ok {
		t.Fatalf("Start page has no Save-to-app link:\n%s", page)
	}
	if !strings.Contains(before(after, ">"), `draggable="true"`) {
		t.Errorf("the Save-to-app link is not draggable, so it cannot be dragged to the bookmarks bar:\n%s", page)
	}
	if !strings.Contains(page, "Save to app") {
		t.Errorf("the Save-to-app link is not labelled:\n%s", page)
	}
	if href := bookmarkletHref(t, page); !strings.HasPrefix(href, "javascript:") {
		t.Errorf("Save-to-app href = %q, want a javascript: URL", href)
	}
}

// before returns the part of s ahead of the first cut, or all of s when cut is
// absent.
func before(s, cut string) string {
	if i := strings.Index(s, cut); i >= 0 {
		return s[:i]
	}
	return s
}

// TestBookmarkletTargetsTheHostItWasServedFrom is the "works on any deployment"
// criterion: the link is generated per request, so the same binary serves
// bookmarks.pi and anything else without reconfiguration.
func TestBookmarkletTargetsTheHostItWasServedFrom(t *testing.T) {
	h := newHandler(t, filepath.Join(t.TempDir(), "db.sqlite"), stubFetcher{})

	page := get(t, h, "/", map[string]string{
		"Host":              "bookmarks.pi",
		"X-Forwarded-Proto": "https",
	}).Body.String()

	if href := bookmarkletHref(t, page); !strings.Contains(href, "https://bookmarks.pi/save?u=") {
		t.Errorf("bookmarklet does not target the host it was served from: %q", href)
	}
}

func TestBookmarkletUsesTheConfiguredBaseURL(t *testing.T) {
	h := app.New(openStore(t, filepath.Join(t.TempDir(), "db.sqlite")), stubFetcher{},
		app.Config{Token: testToken, BaseURL: "https://links.example"}).Handler()

	page := get(t, h, "/", map[string]string{"Host": "internal:8080"}).Body.String()
	if href := bookmarkletHref(t, page); !strings.Contains(href, "https://links.example/save?u=") {
		t.Errorf("bookmarklet ignored BASE_URL: %q", href)
	}
}

// TestBookmarkletCarriesThePageItWasClickedOn covers the "works on any page"
// criterion: the link reads the page it is clicked on, not a URL baked in when
// the Start page was rendered.
func TestBookmarkletCarriesThePageItWasClickedOn(t *testing.T) {
	h := newHandler(t, filepath.Join(t.TempDir(), "db.sqlite"), stubFetcher{})

	href := bookmarkletHref(t, get(t, h, "/", nil).Body.String())
	if !strings.Contains(href, "encodeURIComponent(location.href)") {
		t.Errorf("bookmarklet does not send the page it was clicked on: %q", href)
	}
	if !strings.Contains(href, "encodeURIComponent(document.title)") {
		t.Errorf("bookmarklet does not send the page's title: %q", href)
	}
}

// TestBookmarkletEmbedsNoToken is the security half of the design: the link
// lives in the browser's bookmarks bar, on a page the app does not control, so a
// Token in it would leak to anyone who reads it. The cookie authenticates the
// navigation instead.
func TestBookmarkletEmbedsNoToken(t *testing.T) {
	h := newHandler(t, filepath.Join(t.TempDir(), "db.sqlite"), stubFetcher{})

	href := bookmarkletHref(t, get(t, h, "/", nil).Body.String())
	if strings.Contains(href, testToken) {
		t.Errorf("the bookmarklet embeds the Token: %q", href)
	}
	if strings.Contains(href, "Authorization") || strings.Contains(href, "bm_token") {
		t.Errorf("the bookmarklet carries credentials: %q", href)
	}
}

// TestBookmarkletSaveArrivesAsACrossSiteNavigation follows the bookmarklet
// through: clicking it on a third-party page is a top-level GET to /save from
// another origin, carrying no Authorization header — only the cookie, which the
// browser sends because the session cookie is SameSite=Lax rather than Strict
// (see TestEnteringTheTokenOnceSetsTheCookie).
func TestBookmarkletSaveArrivesAsACrossSiteNavigation(t *testing.T) {
	h := newHandler(t, filepath.Join(t.TempDir(), "db.sqlite"), stubFetcher{})

	req := request(t, http.MethodGet,
		"/save?u="+url.QueryEscape("https://third.example/page")+"&title="+url.QueryEscape("Third Party Page"),
		nil, nil)
	req.Header.Del("Authorization")
	req.AddCookie(&http.Cookie{Name: "bm_token", Value: testToken})
	req.Header.Set("Referer", "https://third.example/page")
	req.Header.Set("Sec-Fetch-Site", "cross-site")

	rr := serve(t, h, req)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303: the bookmarklet's navigation was refused", rr.Code)
	}
	if loc := rr.Header().Get("Location"); loc != "/?saved=1" {
		t.Fatalf("Location = %q, want %q", loc, "/?saved=1")
	}

	page := get(t, h, rr.Header().Get("Location"), nil).Body.String()
	if !strings.Contains(page, "Third Party Page") {
		t.Errorf("the page the bookmarklet was clicked on was not saved under its title:\n%s", page)
	}
	if !strings.Contains(page, "third.example/page") {
		t.Errorf("the page the bookmarklet was clicked on was not saved:\n%s", page)
	}
}
