package app_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/afrieirham/bookmark-manager/internal/app"
	"github.com/afrieirham/bookmark-manager/internal/store"
)

// stubFetcher stands in for the network page-Title lookup.
type stubFetcher struct {
	title string
	ok    bool
}

func (f stubFetcher) Title(context.Context, string) (string, bool) { return f.title, f.ok }

func openStore(t *testing.T, dbPath string) *store.SQLite {
	t.Helper()
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// testToken is the shared secret every handler under test is built with.
const testToken = "test-token"

// newHandler builds a handler with the shared secret on, which is what an
// operator gets by default.
func newHandler(t *testing.T, dbPath string, fetch app.Fetcher) http.Handler {
	t.Helper()
	return app.New(openStore(t, dbPath), fetch, app.Config{Token: testToken}).Handler()
}

// newOpenHandler builds a handler with authentication switched off.
func newOpenHandler(t *testing.T, dbPath string, fetch app.Fetcher) http.Handler {
	t.Helper()
	return app.New(openStore(t, dbPath), fetch, app.Config{AuthDisabled: true}).Handler()
}

func serve(t *testing.T, h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// get and post carry the Token, so the tests about saving, renaming and the
// grid stay about their own behaviour rather than about authentication. The
// anonymous variants are for the tests that are about authentication.
func get(t *testing.T, h http.Handler, target string, header map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	return serve(t, h, request(t, http.MethodGet, target, header, nil))
}

// getAnon issues a GET carrying no Token and no cookie.
func getAnon(t *testing.T, h http.Handler, target string, header map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := request(t, http.MethodGet, target, header, nil)
	req.Header.Del("Authorization")
	return serve(t, h, req)
}

// request builds a request carrying the Token, so callers only describe what
// makes their case different.
func request(t *testing.T, method, target string, header map[string]string, body io.Reader) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, target, body)
	req.Header.Set("Authorization", "Bearer "+testToken)
	for k, v := range header {
		if k == "Host" {
			req.Host = v
			continue
		}
		req.Header.Set(k, v)
	}
	return req
}

func TestStartPageShowsPasteBox(t *testing.T) {
	h := newHandler(t, filepath.Join(t.TempDir(), "db.sqlite"), stubFetcher{})

	rr := get(t, h, "/", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `action="/save"`) || !strings.Contains(body, `name="u"`) {
		t.Errorf("Start page has no paste box posting to /save:\n%s", body)
	}
	if !strings.Contains(body, "No bookmarks yet") {
		t.Errorf("empty Start page does not say it is empty:\n%s", body)
	}
}

func TestSaveRedirectsToStartPageWithSavedID(t *testing.T) {
	h := newHandler(t, filepath.Join(t.TempDir(), "db.sqlite"), stubFetcher{})

	rr := get(t, h, "/save?u="+url.QueryEscape("https://example.com"), nil)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rr.Code)
	}
	if loc := rr.Header().Get("Location"); loc != "/?saved=1" {
		t.Fatalf("Location = %q, want %q", loc, "/?saved=1")
	}

	page := get(t, h, rr.Header().Get("Location"), nil).Body.String()
	if !strings.Contains(page, "Saved — rename it?") {
		t.Errorf("Start page does not prompt to rename:\n%s", page)
	}
	if !strings.Contains(page, `class="card hot" data-id="1"`) {
		t.Errorf("new Bookmark is not highlighted:\n%s", page)
	}
	if !strings.Contains(page, "example.com") {
		t.Errorf("new Bookmark is not shown:\n%s", page)
	}
}

func TestSaveUsesGivenTitle(t *testing.T) {
	h := newHandler(t, filepath.Join(t.TempDir(), "db.sqlite"), stubFetcher{title: "Ignored", ok: true})

	rr := get(t, h, "/save?u="+url.QueryEscape("https://example.com")+"&title="+url.QueryEscape("My Page"), nil)
	page := get(t, h, rr.Header().Get("Location"), nil).Body.String()
	if !strings.Contains(page, "My Page") {
		t.Errorf("given Title not used:\n%s", page)
	}
	if strings.Contains(page, "Ignored") {
		t.Errorf("fetched Title used despite a given one:\n%s", page)
	}
}

func TestSaveFallsBackToFetchedTitle(t *testing.T) {
	h := newHandler(t, filepath.Join(t.TempDir(), "db.sqlite"), stubFetcher{title: "Fetched Title", ok: true})

	rr := get(t, h, "/save?u="+url.QueryEscape("https://example.com"), nil)
	page := get(t, h, rr.Header().Get("Location"), nil).Body.String()
	if !strings.Contains(page, "Fetched Title") {
		t.Errorf("fetched Title not used:\n%s", page)
	}
}

func TestSaveURLWithQueryRoundTrips(t *testing.T) {
	h := newHandler(t, filepath.Join(t.TempDir(), "db.sqlite"), stubFetcher{})
	raw := "https://example.com/s?q=1&r=2"

	rr := get(t, h, "/save?u="+url.QueryEscape(raw), nil)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rr.Code)
	}
	page := get(t, h, rr.Header().Get("Location"), nil).Body.String()
	if !strings.Contains(page, "s?q=1&amp;r=2") {
		t.Errorf("URL with ? and & did not round-trip:\n%s", page)
	}
}

func TestSaveDuplicateDoesNotCreateASecond(t *testing.T) {
	h := newHandler(t, filepath.Join(t.TempDir(), "db.sqlite"), stubFetcher{})

	first := get(t, h, "/save?u="+url.QueryEscape("https://example.com/page"), nil)
	second := get(t, h, "/save?u="+url.QueryEscape("https://example.com/page#later"), nil)

	if loc := second.Header().Get("Location"); loc != "/?saved=1&dup=1" {
		t.Fatalf("duplicate Location = %q, want %q", loc, "/?saved=1&dup=1")
	}
	if first.Header().Get("Location") != second.Header().Get("Location")[:len("/?saved=1")] {
		t.Fatal("duplicate did not re-select the first Bookmark")
	}
	page := get(t, h, "/", nil).Body.String()
	if n := strings.Count(page, `class="card`); n != 1 {
		t.Errorf("grid has %d cards, want 1 after a duplicate save", n)
	}
}

func TestBookmarksPersistAcrossRestarts(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "db.sqlite")

	h := newHandler(t, dbPath, stubFetcher{})
	get(t, h, "/save?u="+url.QueryEscape("https://example.com")+"&title="+url.QueryEscape("Persisted"), nil)

	// A fresh store over the same file stands in for a restart.
	restarted := app.New(openStore(t, dbPath), stubFetcher{}, app.Config{Token: testToken}).Handler()
	page := get(t, restarted, "/", nil).Body.String()
	if !strings.Contains(page, "Persisted") {
		t.Errorf("Bookmark did not survive a restart:\n%s", page)
	}
}

func TestBaseURLHonoursForwardedProto(t *testing.T) {
	h := newHandler(t, filepath.Join(t.TempDir(), "db.sqlite"), stubFetcher{})

	page := get(t, h, "/", map[string]string{
		"Host":              "bookmarks.example.com",
		"X-Forwarded-Proto": "https",
	}).Body.String()
	if !strings.Contains(page, "https://bookmarks.example.com/save") {
		t.Errorf("base URL not built from the request:\n%s", page)
	}
}

// post submits an HTML form the way a browser would with JS off: a plain
// application/x-www-form-urlencoded POST.
func post(t *testing.T, h http.Handler, target string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := request(t, http.MethodPost, target, nil, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return serve(t, h, req)
}

func TestStartPageOffersRenameFormForEachBookmark(t *testing.T) {
	h := newHandler(t, filepath.Join(t.TempDir(), "db.sqlite"), stubFetcher{})
	get(t, h, "/save?u="+url.QueryEscape("https://example.com")+"&title="+url.QueryEscape("A Title"), nil)

	page := get(t, h, "/", nil).Body.String()
	if !strings.Contains(page, `action="/rename"`) {
		t.Errorf("Start page has no rename form (JS-off rename impossible):\n%s", page)
	}
	if !strings.Contains(page, `name="title"`) {
		t.Errorf("rename form has no Title field:\n%s", page)
	}
	if !strings.Contains(page, `action="/delete"`) {
		t.Errorf("Start page has no delete form:\n%s", page)
	}
}

func TestRenameChangesTheTitleOnTheStartPage(t *testing.T) {
	h := newHandler(t, filepath.Join(t.TempDir(), "db.sqlite"), stubFetcher{})
	get(t, h, "/save?u="+url.QueryEscape("https://example.com")+"&title="+url.QueryEscape("Old Title"), nil)

	rr := post(t, h, "/rename", url.Values{"id": {"1"}, "title": {"New Title"}})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rr.Code)
	}

	page := get(t, h, "/", nil).Body.String()
	if !strings.Contains(page, "New Title") {
		t.Errorf("renamed Title not shown:\n%s", page)
	}
	if strings.Contains(page, "Old Title") {
		t.Errorf("old Title still shown after rename:\n%s", page)
	}
}

func TestRenameToEmptyTitleIsIgnored(t *testing.T) {
	h := newHandler(t, filepath.Join(t.TempDir(), "db.sqlite"), stubFetcher{})
	get(t, h, "/save?u="+url.QueryEscape("https://example.com")+"&title="+url.QueryEscape("Keep Me"), nil)

	post(t, h, "/rename", url.Values{"id": {"1"}, "title": {""}})

	page := get(t, h, "/", nil).Body.String()
	if !strings.Contains(page, "Keep Me") {
		t.Errorf("an empty rename wiped the Title:\n%s", page)
	}
}

func TestRenamePersists(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "db.sqlite")
	h := newHandler(t, dbPath, stubFetcher{})
	get(t, h, "/save?u="+url.QueryEscape("https://example.com")+"&title="+url.QueryEscape("Old Title"), nil)
	post(t, h, "/rename", url.Values{"id": {"1"}, "title": {"Renamed"}})

	restarted := app.New(openStore(t, dbPath), stubFetcher{}, app.Config{Token: testToken}).Handler()
	page := get(t, restarted, "/", nil).Body.String()
	if !strings.Contains(page, "Renamed") {
		t.Errorf("renamed Title did not survive a restart:\n%s", page)
	}
}

func TestDeleteRemovesTheBookmarkFromTheGrid(t *testing.T) {
	h := newHandler(t, filepath.Join(t.TempDir(), "db.sqlite"), stubFetcher{})
	get(t, h, "/save?u="+url.QueryEscape("https://example.com")+"&title="+url.QueryEscape("Gone Soon"), nil)

	rr := post(t, h, "/delete", url.Values{"id": {"1"}})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rr.Code)
	}

	page := get(t, h, "/", nil).Body.String()
	if strings.Contains(page, "Gone Soon") {
		t.Errorf("deleted Bookmark still shown:\n%s", page)
	}
	if !strings.Contains(page, "No bookmarks yet") {
		t.Errorf("grid not empty after deleting the only Bookmark:\n%s", page)
	}
}

func TestSaveFallsBackToTheHostWhenTheFetchFails(t *testing.T) {
	h := newHandler(t, filepath.Join(t.TempDir(), "db.sqlite"), stubFetcher{ok: false})

	rr := get(t, h, "/save?u="+url.QueryEscape("https://www.example.com/deep/page"), nil)
	page := get(t, h, rr.Header().Get("Location"), nil).Body.String()
	// Assert the Title itself, not the URL shown beneath it.
	if !strings.Contains(page, ">example.com</a>") {
		t.Errorf("Title did not fall back to the host:\n%s", page)
	}
}

func TestSaveDoesNotFetchWhenATitleIsGiven(t *testing.T) {
	var asked bool
	h := newHandler(t, filepath.Join(t.TempDir(), "db.sqlite"), app.FetcherFunc(
		func(context.Context, string) (string, bool) { asked = true; return "", false },
	))

	get(t, h, "/save?u="+url.QueryEscape("https://example.com")+"&title="+url.QueryEscape("Given"), nil)
	if asked {
		t.Error("the page was fetched even though a Title was given")
	}
}
