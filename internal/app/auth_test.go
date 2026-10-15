package app_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

func newAuthedHandler(t *testing.T) http.Handler {
	t.Helper()
	return newHandler(t, filepath.Join(t.TempDir(), "db.sqlite"), stubFetcher{})
}

func formRequest(t *testing.T, target string, form url.Values) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

func TestStartPageIsRefusedWithoutTheToken(t *testing.T) {
	h := newAuthedHandler(t)
	get(t, h, "/save?u="+url.QueryEscape("https://example.com")+"&title="+url.QueryEscape("Secret"), nil)

	rr := getAnon(t, h, "/", nil)
	if rr.Code == http.StatusOK {
		t.Fatalf("the Start page was served without the Token:\n%s", rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "Secret") {
		t.Error("a Bookmark leaked to an unauthenticated caller")
	}
}

func TestBrowserWithoutTheTokenIsSentToTheLoginPage(t *testing.T) {
	rr := getAnon(t, newAuthedHandler(t), "/", nil)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rr.Code)
	}
	if loc := rr.Header().Get("Location"); loc != "/login" {
		t.Errorf("Location = %q, want %q", loc, "/login")
	}
}

func TestBearerTokenAuthenticatesAnAPIClient(t *testing.T) {
	h := newAuthedHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	if rr := serve(t, h, req); rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 with a Bearer Token", rr.Code)
	}
}

func TestWrongBearerTokenIsRefused(t *testing.T) {
	h := newAuthedHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer not-the-token")
	if rr := serve(t, h, req); rr.Code == http.StatusOK {
		t.Fatal("a wrong Bearer Token was accepted")
	}
}

func TestCookieAuthenticatesLaterVisits(t *testing.T) {
	h := newAuthedHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "bm_token", Value: testToken})
	if rr := serve(t, h, req); rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 with the cookie", rr.Code)
	}
}

func TestEnteringTheTokenOnceSetsTheCookie(t *testing.T) {
	h := newAuthedHandler(t)

	rr := serve(t, h, formRequest(t, "/login", url.Values{"token": {testToken}}))
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rr.Code)
	}
	cookies := rr.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("entering the Token set no cookie")
	}
	c := cookies[0]
	if c.Name != "bm_token" || c.Value != testToken {
		t.Errorf("cookie = %s=%s, want bm_token=%s", c.Name, c.Value, testToken)
	}
	// Lax, not Strict: the bookmarklet and the Android share target arrive as
	// cross-site top-level GETs, and Strict would withhold the cookie from them.
	if c.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, want Lax", c.SameSite)
	}
	// Plain HTTP is the MVP's deployment. A Secure cookie would be dropped there.
	if c.Secure {
		t.Error("cookie is Secure over plain HTTP, so a browser would drop it")
	}
	if !c.HttpOnly {
		t.Error("cookie is not HttpOnly")
	}
}

func TestCookieIsSecureWhenServedOverHTTPS(t *testing.T) {
	h := newAuthedHandler(t)

	req := formRequest(t, "/login", url.Values{"token": {testToken}})
	req.Header.Set("X-Forwarded-Proto", "https")
	rr := serve(t, h, req)

	cookies := rr.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("entering the Token set no cookie")
	}
	if !cookies[0].Secure {
		t.Error("cookie is not Secure when the app is reached over https")
	}
}

func TestWrongTokenOnTheLoginFormIsRefused(t *testing.T) {
	h := newAuthedHandler(t)

	rr := serve(t, h, formRequest(t, "/login", url.Values{"token": {"wrong"}}))
	if len(rr.Result().Cookies()) != 0 {
		t.Error("a wrong Token still set a cookie")
	}
	if loc := rr.Header().Get("Location"); loc != "/login?error=1" {
		t.Errorf("Location = %q, want the login page with an error", loc)
	}
}

func TestLoginPageShowsTheTokenEntry(t *testing.T) {
	rr := getAnon(t, newAuthedHandler(t), "/login", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `action="/login"`) || !strings.Contains(body, `name="token"`) {
		t.Errorf("login page has no Token entry:\n%s", body)
	}
}

func TestAuthDisabledAllowsAccessWithNoToken(t *testing.T) {
	h := newOpenHandler(t, filepath.Join(t.TempDir(), "db.sqlite"), stubFetcher{})

	if rr := getAnon(t, h, "/", nil); rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 when auth is disabled", rr.Code)
	}
	if rr := getAnon(t, h, "/save?u="+url.QueryEscape("https://example.com"), nil); rr.Code != http.StatusSeeOther {
		t.Fatalf("saving with auth disabled: status = %d, want 303", rr.Code)
	}
}

func TestWriteWithoutTheTokenIsRefused(t *testing.T) {
	rr := serve(t, newAuthedHandler(t), formRequest(t, "/delete", url.Values{"id": {"1"}}))
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for an unauthenticated write", rr.Code)
	}
}

func TestJSONClientWithoutTheTokenGetsUnauthorized(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept", "application/json")
	if rr := serve(t, newAuthedHandler(t), req); rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for a JSON client", rr.Code)
	}
}
