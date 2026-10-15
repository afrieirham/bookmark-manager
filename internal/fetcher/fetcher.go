// Package fetcher reads a page's <title> over the network.
package fetcher

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// maxBody caps how much of a page is read while hunting for the <title>.
const maxBody = 1 << 20

// HTTP fetches a page's Title over http/https with a short timeout. It is
// best-effort: any failure reports ok=false and the caller falls back.
type HTTP struct {
	client *http.Client
}

// New returns an HTTP fetcher with a short timeout.
func New() *HTTP {
	return &HTTP{client: &http.Client{Timeout: 5 * time.Second}}
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
