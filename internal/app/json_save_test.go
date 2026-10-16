package app_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

// saveJSON is the machine-readable save result a client asking for JSON gets.
type saveJSON struct {
	OK    bool   `json:"ok"`
	ID    int64  `json:"id"`
	Error string `json:"error"`
}

// jsonClient is the Accept header a script sends when it wants JSON rather than
// a redirect it cannot follow usefully.
func jsonClient() map[string]string {
	return map[string]string{"Accept": "application/json"}
}

// decodeSave reads a save response as the machine-readable result.
func decodeSave(t *testing.T, body string) saveJSON {
	t.Helper()
	var got saveJSON
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("save response is not JSON (%v):\n%s", err, body)
	}
	return got
}

func TestSaveAnswersJSONWhenTheClientAsksForIt(t *testing.T) {
	h := newHandler(t, filepath.Join(t.TempDir(), "db.sqlite"), stubFetcher{})

	rr := get(t, h, "/save?u="+url.QueryEscape("https://example.com"), jsonClient())
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if loc := rr.Header().Get("Location"); loc != "" {
		t.Errorf("Location = %q, want no redirect for a JSON client", loc)
	}

	got := decodeSave(t, rr.Body.String())
	if !got.OK || got.ID != 1 {
		t.Errorf("got %+v, want ok with id 1", got)
	}
}

// TestSaveAnswersJSONForADuplicateWithTheExistingID pins the contract a script
// relies on: saving the same URL twice succeeds both times, with the same id, so
// the caller need not tell a first save from a repeat.
func TestSaveAnswersJSONForADuplicateWithTheExistingID(t *testing.T) {
	h := newHandler(t, filepath.Join(t.TempDir(), "db.sqlite"), stubFetcher{})

	first := decodeSave(t, get(t, h, "/save?u="+url.QueryEscape("https://example.com/p"), jsonClient()).Body.String())
	second := decodeSave(t, get(t, h, "/save?u="+url.QueryEscape("https://example.com/p#later"), jsonClient()).Body.String())

	if !first.OK || !second.OK {
		t.Fatalf("got %+v then %+v, want both ok", first, second)
	}
	if first.ID != second.ID {
		t.Errorf("duplicate save reported id %d, want the first save's %d", second.ID, first.ID)
	}
	if n := strings.Count(get(t, h, "/", nil).Body.String(), `class="card`); n != 1 {
		t.Errorf("grid has %d cards, want 1 after a duplicate save", n)
	}
}

func TestSaveAnswersJSONErrorForAnUnusableURL(t *testing.T) {
	h := newHandler(t, filepath.Join(t.TempDir(), "db.sqlite"), stubFetcher{})

	rr := get(t, h, "/save?u="+url.QueryEscape("not a url"), jsonClient())
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
	got := decodeSave(t, rr.Body.String())
	if got.OK {
		t.Errorf("got %+v, want ok false", got)
	}
	if got.Error == "" {
		t.Errorf("got %+v, want a reason the client can read", got)
	}
}

// TestSaveStillRedirectsForABrowser guards the other half of the contract: JSON
// is opt-in, and the paste box's plain form must keep getting its redirect.
func TestSaveStillRedirectsForABrowser(t *testing.T) {
	h := newHandler(t, filepath.Join(t.TempDir(), "db.sqlite"), stubFetcher{})

	rr := get(t, h, "/save?u="+url.QueryEscape("https://example.com"), map[string]string{"Accept": "text/html"})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rr.Code)
	}
	if loc := rr.Header().Get("Location"); loc != "/?saved=1" {
		t.Errorf("Location = %q, want %q", loc, "/?saved=1")
	}
}

// TestTheBareURLAliasAnswersJSONToo covers the alias reaching the same contract
// as /save, so a script may use whichever is handier.
func TestTheBareURLAliasAnswersJSONToo(t *testing.T) {
	h := newHandler(t, filepath.Join(t.TempDir(), "db.sqlite"), stubFetcher{})

	rr := get(t, h, "/example.com", jsonClient())
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if got := decodeSave(t, rr.Body.String()); !got.OK || got.ID != 1 {
		t.Errorf("got %+v, want ok with id 1", got)
	}
}
