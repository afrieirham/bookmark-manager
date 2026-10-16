// Package domain holds the pure rules of a Bookmark. It touches no I/O so the
// save/dedupe/title-fallback logic can be tested directly.
package domain

import (
	"net/url"
	"strings"
	"time"
)

// Bookmark is a saved URL with a Title, as shown on the Start page.
type Bookmark struct {
	ID          int64
	URL         string
	Title       string
	HasFavicon  bool
	Favicon     []byte
	FaviconType string
	CreatedAt   time.Time
	Position    int
}

// NormalizeURL turns raw into the URL a Bookmark stores: surrounding space
// trimmed, a missing scheme filled in with https, and the fragment stripped.
// ok is false when raw cannot be read as a URL with a host.
func NormalizeURL(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	// Hostname, not Host: "https://:8080" parses with a port and no hostname, so
	// checking Host would let it through with the Title ":8080".
	if err != nil || u.Hostname() == "" {
		return "", false
	}
	// Only the two schemes the app can fetch and safely render. A javascript: or
	// file: URL is not a Bookmark, and should not reach storage to be neutralised
	// on the way out.
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", false
	}
	u.Fragment = ""
	return u.String(), true
}

// nextPosition is where a new Bookmark goes: one past the highest position in
// use. The number of Bookmarks is not the same thing, because deleting never
// reindexes, so a grid that has lost one has a gap in its positions and a new
// Bookmark would land in the middle of it.
func nextPosition(existing []Bookmark) int {
	next := 0
	for _, b := range existing {
		if b.Position >= next {
			next = b.Position + 1
		}
	}
	return next
}

// FallbackTitle chooses a Bookmark's Title: the given title when there is one,
// else the fetched page title when the fetch succeeded, else the URL's host
// with a leading "www." removed.
func FallbackTitle(normalizedURL, given, fetched string, fetchedOK bool) string {
	if t := strings.TrimSpace(given); t != "" {
		return t
	}
	if fetchedOK {
		if t := strings.TrimSpace(fetched); t != "" {
			return t
		}
	}
	if u, err := url.Parse(normalizedURL); err == nil && u.Host != "" {
		return strings.TrimPrefix(u.Host, "www.")
	}
	return normalizedURL
}

// SaveResult is the outcome of a save request.
type SaveResult struct {
	// Bookmark is the existing Bookmark when Duplicate is true; otherwise it is
	// the new Bookmark, with ID unset and Position at the end of the grid.
	Bookmark Bookmark
	// Duplicate reports that the URL was already saved.
	Duplicate bool
}

// Save decides what saving rawURL does against the current Bookmarks. ok is
// false when rawURL is unusable. A URL already saved — compared after
// NormalizeURL, so a differing fragment still matches — yields that Bookmark
// with Duplicate set and its Title left untouched.
func Save(existing []Bookmark, rawURL, givenTitle, fetchedTitle string, fetchedOK bool, now time.Time) (SaveResult, bool) {
	nu, ok := NormalizeURL(rawURL)
	if !ok {
		return SaveResult{}, false
	}
	for _, b := range existing {
		if b.URL == nu {
			return SaveResult{Bookmark: b, Duplicate: true}, true
		}
	}
	b := Bookmark{
		URL:       nu,
		Title:     FallbackTitle(nu, givenTitle, fetchedTitle, fetchedOK),
		CreatedAt: now,
		Position:  nextPosition(existing),
	}
	return SaveResult{Bookmark: b}, true
}

// Rename returns b with its Title set to title. A blank title is ignored: ok is
// false and b is returned unchanged. Everything else about the Bookmark — its
// URL, identity, and grid position — is untouched.
func Rename(b Bookmark, title string) (Bookmark, bool) {
	t := strings.TrimSpace(title)
	if t == "" {
		return b, false
	}
	b.Title = t
	return b, true
}

// Reorder returns bookmarks in the order given by ids, the rule behind
// POST /reorder. ids names the Bookmarks that should come first, in order; an
// id that names no Bookmark, or names one twice, is ignored. Every Bookmark
// left out keeps its place after the named ones, in its existing order. So a
// partial or stale list can never drop a Bookmark, and the result is always the
// full set with contiguous positions. Each Bookmark comes back with Position
// set to its new index; everything else about it is untouched.
func Reorder(existing []Bookmark, ids []int64) []Bookmark {
	byID := make(map[int64]Bookmark, len(existing))
	for _, b := range existing {
		byID[b.ID] = b
	}
	ordered := make([]Bookmark, 0, len(existing))
	placed := make(map[int64]bool, len(existing))
	for _, id := range ids {
		b, ok := byID[id]
		if !ok || placed[id] {
			continue
		}
		placed[id] = true
		ordered = append(ordered, b)
	}
	for _, b := range existing {
		if placed[b.ID] {
			continue
		}
		placed[b.ID] = true
		ordered = append(ordered, b)
	}
	for i := range ordered {
		ordered[i].Position = i
	}
	return ordered
}
