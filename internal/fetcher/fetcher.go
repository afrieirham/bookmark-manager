// Package fetcher reads a page's <title> over the network.
package fetcher

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"

	"golang.org/x/net/html"
)

// maxBody caps how much of a page is read while hunting for the <title>.
const maxBody = 1 << 20

const maxFaviconBody = 256 << 10

// HTTP fetches a page's Title over http/https with a short timeout. It is
// best-effort: any failure reports ok=false and the caller falls back.
type HTTP struct {
	client *http.Client
}

// DefaultTimeout bounds how long a page read may take. A save waits on it, so it
// stays short: a slow page falls back to the hostname rather than holding up the
// redirect.
const DefaultTimeout = 5 * time.Second

// New returns an HTTP fetcher with DefaultTimeout that will not connect to a
// private address. The URL it reads is chosen by whoever can reach the save
// endpoint, but the server is the one that makes the request, so an unguarded
// fetcher would reach a service the client cannot — and because the Title is
// stored and shown on the Start page, the answer would come back.
func New() *HTTP { return NewWithTimeout(DefaultTimeout) }

// NewWithTimeout returns a guarded fetcher that gives up on a page after d.
func NewWithTimeout(d time.Duration) *HTTP {
	return NewWithClient(&http.Client{Timeout: d, Transport: guardedTransport(d)})
}

// NewWithClient is the seam for the reachability policy: the caller supplies the
// client, and with it decides which addresses may be reached. Production code
// wants New.
func NewWithClient(c *http.Client) *HTTP { return &HTTP{client: c} }

// guardedTransport dials only public addresses. The check runs at connect time
// against the address actually being connected to, so it covers a redirect to a
// private host and a name that resolves differently on the second lookup.
func guardedTransport(d time.Duration) *http.Transport {
	dialer := &net.Dialer{
		Timeout: d,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return fmt.Errorf("refusing %q: %w", address, err)
			}
			ip := net.ParseIP(host)
			if ip == nil {
				return fmt.Errorf("refusing %q: not an IP address", address)
			}
			if isPrivate(ip) {
				return fmt.Errorf("refusing %s: private address", ip)
			}
			return nil
		},
	}
	return &http.Transport{
		DialContext:         dialer.DialContext,
		ForceAttemptHTTP2:   true,
		TLSHandshakeTimeout: 5 * time.Second,
		// No Proxy: a proxy resolves the target itself, so the guard would only
		// ever see the proxy's address and the private-address check would be
		// decoration.
	}
}

// isPrivate reports whether ip is an address the app should not reach on a
// saved URL's behalf: loopback, the private ranges, and link-local, which is
// where the cloud metadata services live. Carrier-grade NAT is deliberately not
// included — that is where a Tailscale address sits, and bookmarking a service
// on the operator's own tailnet is a reasonable thing to want.
func isPrivate(ip net.IP) bool {
	return ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() ||
		ip.IsMulticast()
}

// Title returns the page's <title> text, or ok=false when it cannot be read.
func (f *HTTP) Title(ctx context.Context, rawURL string) (string, bool) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "", false
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", false
	}
	req.Header.Set("User-Agent", "bookmark-manager/0.1")
	resp, err := f.client.Do(req)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", false
	}
	doc, err := html.Parse(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return "", false
	}
	if title := firstTitle(doc); title != "" {
		return title, true
	}
	return "", false
}

// Favicon fetches the conventional /favicon.ico image for a saved URL. It is
// best-effort, bounded in size, and accepts image media types only.
func (f *HTTP) Favicon(ctx context.Context, rawURL string) ([]byte, string, bool) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return nil, "", false
	}
	target := (&url.URL{Scheme: u.Scheme, Host: u.Host, Path: "/favicon.ico"}).String()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, "", false
	}
	req.Header.Set("User-Agent", "bookmark-manager/0.1")
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", false
	}
	mediaType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || !supportedFaviconType(strings.ToLower(mediaType)) {
		return nil, "", false
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxFaviconBody+1))
	if err != nil || len(data) == 0 || len(data) > maxFaviconBody {
		return nil, "", false
	}
	return data, mediaType, true
}

func supportedFaviconType(mediaType string) bool {
	switch mediaType {
	case "image/gif", "image/jpeg", "image/png", "image/svg+xml", "image/vnd.microsoft.icon", "image/webp", "image/x-icon":
		return true
	default:
		return false
	}
}

func firstTitle(n *html.Node) string {
	if n.Type == html.ElementNode && n.Data == "title" {
		if n.FirstChild != nil && n.FirstChild.Type == html.TextNode {
			if t := strings.TrimSpace(n.FirstChild.Data); t != "" {
				return t
			}
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if t := firstTitle(c); t != "" {
			return t
		}
	}
	return ""
}
