package fetcher_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/afrieirham/bookmark-manager/internal/fetcher"
)

// Every page these tests read is served by an httptest server on the loopback
// interface. Nothing here reaches the network.

func pageServer(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestTitleReadsThePagesTitle(t *testing.T) {
	url := pageServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><head><title>  A Page  </title></head><body>hi</body></html>`))
	})

	got, ok := testFetcher(fetcher.DefaultTimeout).Title(context.Background(), url)
	if !ok {
		t.Fatal("no Title was found")
	}
	if got != "A Page" {
		t.Errorf("Title = %q, want %q with surrounding space trimmed", got, "A Page")
	}
}

func TestTitleFindsATitleAmongOtherHeadElements(t *testing.T) {
	url := pageServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><head><meta charset="utf-8">` +
			`<link rel="icon" href="/favicon.ico">` +
			`<title>Real Title</title><style>body{color:red}</style>` +
			`</head><body></body></html>`))
	})

	got, ok := testFetcher(fetcher.DefaultTimeout).Title(context.Background(), url)
	if !ok || got != "Real Title" {
		t.Errorf("Title = %q, ok = %v; want %q", got, ok, "Real Title")
	}
}

func TestTitleReportsFailureWhenThePageHasNoTitle(t *testing.T) {
	url := pageServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><head></head><body>no title here</body></html>`))
	})

	if got, ok := testFetcher(fetcher.DefaultTimeout).Title(context.Background(), url); ok {
		t.Errorf("Title = %q, ok = true; want no Title", got)
	}
}

func TestTitleReportsFailureOnAnErrorStatus(t *testing.T) {
	url := pageServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`<title>Not Found</title>`))
	})

	if _, ok := testFetcher(fetcher.DefaultTimeout).Title(context.Background(), url); ok {
		t.Error("a 404 still produced a Title")
	}
}

func TestTitleOnlyReadsHTTPAndHTTPS(t *testing.T) {
	for _, rawURL := range []string{
		"file:///etc/passwd",
		"ftp://example.com/page",
		"javascript:alert(1)",
		"data:text/html,<title>Inline</title>",
	} {
		t.Run(rawURL, func(t *testing.T) {
			if got, ok := testFetcher(fetcher.DefaultTimeout).Title(context.Background(), rawURL); ok {
				t.Errorf("Title = %q, ok = true; want the scheme refused", got)
			}
		})
	}
}

func TestTitleGivesUpOnASlowPage(t *testing.T) {
	url := pageServer(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(250 * time.Millisecond)
		w.Write([]byte(`<title>Too Late</title>`))
	})

	f := testFetcher(25 * time.Millisecond)
	start := time.Now()
	if got, ok := f.Title(context.Background(), url); ok {
		t.Errorf("Title = %q, ok = true; want the slow page abandoned", got)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("gave up after %v, want the short timeout to cut it short", elapsed)
	}
}

func TestTitleGivesUpWhenTheCallerCancels(t *testing.T) {
	url := pageServer(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(250 * time.Millisecond)
		w.Write([]byte(`<title>Too Late</title>`))
	})

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()

	if _, ok := testFetcher(fetcher.DefaultTimeout).Title(ctx, url); ok {
		t.Error("a cancelled read still produced a Title")
	}
}

func TestTitleReportsFailureWhenTheHostCannotBeReached(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close() // nothing is listening now

	if _, ok := testFetcher(fetcher.DefaultTimeout).Title(context.Background(), url); ok {
		t.Error("an unreachable host still produced a Title")
	}
}

func TestTitleReadsAPageLargerThanTheCap(t *testing.T) {
	url := pageServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<title>Found Early</title>`))
		// Far more than the read cap, to prove the read is bounded rather than
		// unbounded, while a Title near the start is still found.
		big := make([]byte, 4<<20)
		for i := range big {
			big[i] = 'x'
		}
		w.Write(big)
	})

	got, ok := testFetcher(fetcher.DefaultTimeout).Title(context.Background(), url)
	if !ok || got != "Found Early" {
		t.Errorf("Title = %q, ok = %v; want %q", got, ok, "Found Early")
	}
}

// TestTitleRefusesAPrivateAddress is the SSRF guard. A saved URL is fetched by
// the server, not the browser, so a loopback or private target would let a
// Bookmark read a service the client cannot reach — and because the Title is
// stored and shown on the Start page, the response is not blind.
func TestTitleRefusesAPrivateAddress(t *testing.T) {
	url := pageServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<title>Internal Service</title>`))
	})

	if got, ok := fetcher.New().Title(context.Background(), url); ok {
		t.Errorf("Title = %q, ok = true; want the private address refused", got)
	}
}

// TestTitleRefusesAPrivateLiteralAddress covers a target given as an IP rather
// than a name, which never goes through DNS.
func TestTitleRefusesAPrivateLiteralAddress(t *testing.T) {
	for _, rawURL := range []string{
		"http://127.0.0.1/",
		"http://10.0.0.1/",
		"http://192.168.1.1/",
		"http://169.254.169.254/latest/meta-data/",
		"http://[::1]/",
	} {
		t.Run(rawURL, func(t *testing.T) {
			if got, ok := fetcher.New().Title(context.Background(), rawURL); ok {
				t.Errorf("Title = %q, ok = true; want the private address refused", got)
			}
		})
	}
}

// testFetcher reaches the loopback servers these tests stand up. New() refuses
// private addresses on purpose (see TestTitleRefusesAPrivateAddress), so the
// tests that are about reading a Title supply their own client.
func testFetcher(d time.Duration) *fetcher.HTTP {
	return fetcher.NewWithClient(&http.Client{Timeout: d})
}
