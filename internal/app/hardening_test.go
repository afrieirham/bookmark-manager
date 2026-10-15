package app_test

import (
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/afrieirham/bookmark-manager/internal/app"
)

// TestForwardedProtoIsIgnoredUnlessItNamesAScheme keeps a client from putting
// arbitrary text into the base URL the Start page renders. The header feeds
// baseURLFor and the cookie's Secure check, so anything that is not http or
// https falls back to the scheme the request actually used.
func TestForwardedProtoIsIgnoredUnlessItNamesAScheme(t *testing.T) {
	h := newHandler(t, filepath.Join(t.TempDir(), "db.sqlite"), stubFetcher{})

	page := get(t, h, "/", map[string]string{
		"Host":              "bookmarks.pi",
		"X-Forwarded-Proto": "javascript:alert(1)//",
	}).Body.String()

	if strings.Contains(page, "javascript:alert(1)") {
		t.Errorf("the X-Forwarded-Proto value reached the page:\n%s", page)
	}
	if !strings.Contains(page, "http://bookmarks.pi/save") {
		t.Errorf("the base URL did not fall back to the transport scheme:\n%s", page)
	}
}

// TestForwardedProtoAcceptsAProxyChain covers the real shape a proxy sends.
func TestForwardedProtoAcceptsAProxyChain(t *testing.T) {
	h := newHandler(t, filepath.Join(t.TempDir(), "db.sqlite"), stubFetcher{})

	page := get(t, h, "/", map[string]string{
		"Host":              "bookmarks.pi",
		"X-Forwarded-Proto": "https, http",
	}).Body.String()
	if !strings.Contains(page, "https://bookmarks.pi/save") {
		t.Errorf("the first hop's scheme was not honoured:\n%s", page)
	}
}

// TestACrossSiteSaveStillSavesByDesign pins the CSRF boundary the spec chose,
// so that changing it is a deliberate act rather than an accident.
//
// /save changes state on a GET, and the session cookie is SameSite=Lax, which a
// browser still sends on a cross-site top-level navigation — so any page can
// make a logged-in browser save a URL. The bookmarklet and the Android share
// target arrive as exactly that, so the request alone cannot tell a real save
// from a forged one. Issue #11 makes GET /save the canonical endpoint and both
// of those clients depend on it, so this is a spec-level choice, not an
// oversight; what bounds the damage is that the forgery is blind — the attacker
// cannot read the response, so it learns nothing — and that the worst it can do
// is add a row, since the fetcher refuses private addresses (see the SSRF guard
// in internal/fetcher) and so cannot be turned into a probe of the LAN.
func TestACrossSiteSaveStillSavesByDesign(t *testing.T) {
	h := newHandler(t, filepath.Join(t.TempDir(), "db.sqlite"), stubFetcher{title: "Internal Admin", ok: true})

	req := request(t, http.MethodGet, "/save?u="+url.QueryEscape("http://192.168.1.1/admin"), nil, nil)
	req.Header.Del("Authorization")
	req.AddCookie(&http.Cookie{Name: "bm_token", Value: app.SessionValue(testToken)})
	req.Header.Set("Sec-Fetch-Site", "cross-site")

	rr := serve(t, h, req)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rr.Code)
	}
	page := get(t, h, rr.Header().Get("Location"), nil).Body.String()
	if !strings.Contains(page, "192.168.1.1/admin") {
		t.Errorf("the cross-site save did not happen:\n%s", page)
	}
}
