package app_test

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

// manifest is the shape the tests read the served manifest into, so they assert
// on the PWA contract (name, start_url, share_target) rather than on bytes.
type manifest struct {
	Name        string      `json:"name"`
	ShortName   string      `json:"short_name"`
	StartURL    string      `json:"start_url"`
	Display     string      `json:"display"`
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
