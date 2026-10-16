package app_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

// manifest is the shape the tests read the served manifest into, so they assert
// on the PWA contract (name, start_url, share_target) rather than on bytes.
type manifest struct {
	Name        string         `json:"name"`
	ShortName   string         `json:"short_name"`
	StartURL    string         `json:"start_url"`
	Display     string         `json:"display"`
	Icons       []manifestIcon `json:"icons"`
	ShareTarget struct {
		Action string            `json:"action"`
		Method string            `json:"method"`
		Params map[string]string `json:"params"`
	} `json:"share_target"`
}

type manifestIcon struct {
	Src   string `json:"src"`
	Sizes string `json:"sizes"`
	Type  string `json:"type"`
}

func fetchManifest(t *testing.T, h http.Handler) manifest {
	t.Helper()
	rr := get(t, h, "/manifest.webmanifest", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /manifest.webmanifest status = %d, want 200", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/manifest+json") {
		t.Errorf("manifest Content-Type = %q, want application/manifest+json", ct)
	}
	var m manifest
	if err := json.Unmarshal(rr.Body.Bytes(), &m); err != nil {
		t.Fatalf("manifest is not valid JSON: %v", err)
	}
	return m
}

func TestManifestIsServedAsValidJSON(t *testing.T) {
	h := newHandler(t, filepath.Join(t.TempDir(), "db.sqlite"), stubFetcher{})
	fetchManifest(t, h)
}

func TestManifestMapsAndroidShareToSaveQueryParams(t *testing.T) {
	h := newHandler(t, filepath.Join(t.TempDir(), "db.sqlite"), stubFetcher{})
	m := fetchManifest(t, h)

	st := m.ShareTarget
	if st.Action != "/save" {
		t.Errorf("share_target action = %q, want /save", st.Action)
	}
	if st.Method != "GET" {
		t.Errorf("share_target method = %q, want GET", st.Method)
	}
	// Android's shared URL must land on /save's u parameter, its title on title.
	if got := st.Params["url"]; got != "u" {
		t.Errorf("share_target maps url -> %q, want u", got)
	}
	if got := st.Params["title"]; got != "title" {
		t.Errorf("share_target maps title -> %q, want title", got)
	}
}

func TestServiceWorkerIsServedFromTheRootAsJavaScript(t *testing.T) {
	h := newHandler(t, filepath.Join(t.TempDir(), "db.sqlite"), stubFetcher{})

	// Root path, not /static/: a service worker's scope is limited to its own
	// directory, so /static/sw.js could only ever control /static/.
	rr := get(t, h, "/sw.js", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /sw.js status = %d, want 200", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/javascript") {
		t.Errorf("service worker Content-Type = %q, want text/javascript", ct)
	}
	if rr.Body.Len() == 0 {
		t.Error("service worker is empty")
	}
}

func TestManifestIconsAreServedAsPNG(t *testing.T) {
	h := newHandler(t, filepath.Join(t.TempDir(), "db.sqlite"), stubFetcher{})

	for _, icon := range fetchManifest(t, h).Icons {
		rr := get(t, h, icon.Src, nil)
		if rr.Code != http.StatusOK {
			t.Errorf("GET %s status = %d, want 200", icon.Src, rr.Code)
			continue
		}
		if ct := rr.Header().Get("Content-Type"); ct != "image/png" {
			t.Errorf("%s Content-Type = %q, want image/png", icon.Src, ct)
		}
		if sig := rr.Body.Bytes()[:8]; string(sig) != "\x89PNG\r\n\x1a\n" {
			t.Errorf("%s is not a PNG", icon.Src)
		}
	}
}

func TestStartPageLinksTheManifestAndRegistersTheServiceWorker(t *testing.T) {
	h := newHandler(t, filepath.Join(t.TempDir(), "db.sqlite"), stubFetcher{})

	page := get(t, h, "/", nil).Body.String()
	if !strings.Contains(page, `rel="manifest"`) || !strings.Contains(page, `href="/manifest.webmanifest"`) {
		t.Errorf("Start page does not link the manifest:\n%s", page)
	}
	if !strings.Contains(page, `rel="icon"`) {
		t.Errorf("Start page does not link an icon:\n%s", page)
	}
	// The service worker is registered only in a secure context, so plain HTTP
	// (the MVP's deployment) does not try and does not log a failure.
	if !strings.Contains(page, "navigator.serviceWorker.register('/sw.js')") {
		t.Errorf("Start page does not register the service worker:\n%s", page)
	}
	if !strings.Contains(page, "isSecureContext") {
		t.Errorf("service worker registration is not gated on a secure context:\n%s", page)
	}
}

// The browser fetches the manifest with credentials mode "omit" and the icons
// without the app's cookie, so these must be served without the Token or
// installability breaks. None of them carries Bookmark data.
func TestPWAFilesAreServedWithoutTheToken(t *testing.T) {
	h := newAuthedHandler(t)

	for _, path := range []string{"/manifest.webmanifest", "/sw.js", "/static/icon-192.png"} {
		if rr := getAnon(t, h, path, nil); rr.Code != http.StatusOK {
			t.Errorf("anonymous GET %s status = %d, want 200", path, rr.Code)
		}
	}
}

// Saving stays gated: the share target lands on /save, which a browser reaches
// as a top-level navigation carrying the cookie. Without the Token, nothing is
// saved.
func TestShareTargetSaveIsStillRefusedWithoutTheToken(t *testing.T) {
	h := newAuthedHandler(t)

	getAnon(t, h, "/save?u="+url.QueryEscape("https://shared.example/page")+"&title="+url.QueryEscape("Shared"), nil)

	if page := get(t, h, "/", nil).Body.String(); strings.Contains(page, "shared.example") {
		t.Errorf("an unauthenticated share target save created a Bookmark:\n%s", page)
	}
}

// The exact shape Android sends through the share target: the page URL on u,
// the title on title, plus a text field /save ignores.
func TestSharedLinkIsSavedUnderItsTitle(t *testing.T) {
	h := newHandler(t, filepath.Join(t.TempDir(), "db.sqlite"), stubFetcher{})

	rr := get(t, h, "/save?u="+url.QueryEscape("https://shared.example/page")+
		"&title="+url.QueryEscape("A Shared Page")+
		"&text="+url.QueryEscape("https://shared.example/page"), nil)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rr.Code)
	}

	page := get(t, h, rr.Header().Get("Location"), nil).Body.String()
	if !strings.Contains(page, "A Shared Page") {
		t.Errorf("shared link not saved under its Title:\n%s", page)
	}
}

// The MVP runs over plain HTTP on a LAN. Nothing in the PWA wiring may require
// HTTPS: the browser withholds install and share on its own, and the app works
// unchanged. This pins that the Start page and the manifest are still served.
func TestAppStillWorksOverPlainHTTP(t *testing.T) {
	h := newHandler(t, filepath.Join(t.TempDir(), "db.sqlite"), stubFetcher{})

	start := get(t, h, "/", map[string]string{"Host": "bookmarks.example.com"})
	if start.Code != http.StatusOK {
		t.Fatalf("plain-HTTP Start page status = %d, want 200", start.Code)
	}
	if !strings.Contains(start.Body.String(), `action="/save"`) {
		t.Error("plain-HTTP Start page lost its paste box")
	}
	if rr := get(t, h, "/manifest.webmanifest", map[string]string{"Host": "bookmarks.example.com"}); rr.Code != http.StatusOK {
		t.Errorf("plain-HTTP manifest status = %d, want 200", rr.Code)
	}
}
