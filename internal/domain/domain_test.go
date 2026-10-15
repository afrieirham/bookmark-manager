package domain_test

import (
	"strconv"
	"testing"
	"time"

	"github.com/afrieirham/bookmark-manager/internal/domain"
)

func TestNormalizeURL(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
		ok   bool
	}{
		{"adds https when scheme missing", "example.com", "https://example.com", true},
		{"keeps an explicit scheme", "http://example.com/x", "http://example.com/x", true},
		{"strips the fragment", "https://example.com/page#section", "https://example.com/page", true},
		{"trims surrounding space", "  https://example.com/a  ", "https://example.com/a", true},
		{"keeps query intact", "https://example.com/s?q=1&r=2", "https://example.com/s?q=1&r=2", true},
		{"empty is not usable", "", "", false},
		{"garbage is not usable", "not a url", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := domain.NormalizeURL(c.raw)
			if ok != c.ok {
				t.Fatalf("ok = %v, want %v", ok, c.ok)
			}
			if got != c.want {
				t.Errorf("URL = %q, want %q", got, c.want)
			}
		})
	}
}

func TestFallbackTitle(t *testing.T) {
	cases := []struct {
		name      string
		url       string
		given     string
		fetched   string
		fetchedOK bool
		want      string
	}{
		{"given title wins", "https://example.com", "My Page", "Fetched", true, "My Page"},
		{"blank given title is ignored", "https://example.com", "   ", "Fetched", true, "Fetched"},
		{"fetched title used when no given title", "https://example.com", "", "Fetched", true, "Fetched"},
		{"host is the last resort", "https://example.com/deep/path", "", "", false, "example.com"},
		{"leading www is dropped from host", "https://www.example.com/x", "", "", false, "example.com"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := domain.FallbackTitle(c.url, c.given, c.fetched, c.fetchedOK)
			if got != c.want {
				t.Errorf("Title = %q, want %q", got, c.want)
			}
		})
	}
}

func TestSaveAppendsNewBookmarkAtTheEnd(t *testing.T) {
	existing := []domain.Bookmark{
		{ID: 1, URL: "https://a.example", Title: "A", Position: 0},
	}
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

	res, ok := domain.Save(existing, "b.example", "B", "", false, now)
	if !ok {
		t.Fatal("Save reported the URL unusable")
	}
	if res.Duplicate {
		t.Error("new URL reported as duplicate")
	}
	got := res.Bookmark
	if got.URL != "https://b.example" {
		t.Errorf("URL = %q, want %q", got.URL, "https://b.example")
	}
	if got.Title != "B" {
		t.Errorf("Title = %q, want %q", got.Title, "B")
	}
	if got.Position != 1 {
		t.Errorf("Position = %d, want 1 (appended at the end)", got.Position)
	}
	if !got.CreatedAt.Equal(now) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, now)
	}
}

func TestSaveFallsBackToHostWhenNoTitleGiven(t *testing.T) {
	now := time.Now()
	res, ok := domain.Save(nil, "https://www.example.com/x", "", "", false, now)
	if !ok {
		t.Fatal("Save reported the URL unusable")
	}
	if res.Bookmark.Title != "example.com" {
		t.Errorf("Title = %q, want %q", res.Bookmark.Title, "example.com")
	}
}

func TestSaveFindsDuplicateIgnoringFragment(t *testing.T) {
	existing := []domain.Bookmark{
		{ID: 7, URL: "https://example.com/page", Title: "Original", Position: 0},
	}
	res, ok := domain.Save(existing, "https://example.com/page#later", "Changed", "", false, time.Now())
	if !ok {
		t.Fatal("Save reported the URL unusable")
	}
	if !res.Duplicate {
		t.Fatal("expected a duplicate")
	}
	if res.Bookmark.ID != 7 {
		t.Errorf("ID = %d, want the existing 7", res.Bookmark.ID)
	}
	if res.Bookmark.Title != "Original" {
		t.Errorf("Title = %q, want the existing Title untouched", res.Bookmark.Title)
	}
}

func TestSaveRejectsUnusableURL(t *testing.T) {
	if _, ok := domain.Save(nil, "", "", "", false, time.Now()); ok {
		t.Error("empty URL reported as usable")
	}
}

func TestRenameChangesTitle(t *testing.T) {
	b := domain.Bookmark{ID: 1, URL: "https://example.com", Title: "Old"}
	got, ok := domain.Rename(b, "New")
	if !ok {
		t.Fatal("Rename did not apply a non-empty Title")
	}
	if got.Title != "New" {
		t.Errorf("Title = %q, want %q", got.Title, "New")
	}
	if got.ID != b.ID || got.URL != b.URL {
		t.Errorf("Rename changed the Bookmark's identity: %+v", got)
	}
}

func TestRenameIgnoresEmptyTitle(t *testing.T) {
	for _, title := range []string{"", "   "} {
		t.Run("title="+strconv.Quote(title), func(t *testing.T) {
			b := domain.Bookmark{ID: 1, URL: "https://example.com", Title: "Keep"}
			got, ok := domain.Rename(b, title)
			if ok {
				t.Errorf("Rename(%q) applied, want it ignored", title)
			}
			if got.Title != "Keep" {
				t.Errorf("Rename(%q) changed Title to %q, want %q", title, got.Title, "Keep")
			}
		})
	}
}

func TestReorderAppliesTheGivenOrder(t *testing.T) {
	existing := []domain.Bookmark{
		{ID: 1, URL: "https://a.example", Title: "A", Position: 0},
		{ID: 2, URL: "https://b.example", Title: "B", Position: 1},
		{ID: 3, URL: "https://c.example", Title: "C", Position: 2},
	}
	got := domain.Reorder(existing, []int64{3, 1, 2})
	wantIDs := []int64{3, 1, 2}
	if len(got) != len(wantIDs) {
		t.Fatalf("got %d Bookmarks, want %d", len(got), len(wantIDs))
	}
	for i, b := range got {
		if b.ID != wantIDs[i] {
			t.Errorf("position %d holds id %d, want %d", i, b.ID, wantIDs[i])
		}
		if b.Position != i {
			t.Errorf("Bookmark %d has Position %d, want %d", b.ID, b.Position, i)
		}
	}
}

func TestReorderKeepsEachBookmarkIntact(t *testing.T) {
	existing := []domain.Bookmark{
		{ID: 1, URL: "https://a.example", Title: "A", Position: 0},
		{ID: 2, URL: "https://b.example", Title: "B", Position: 1},
	}
	got := domain.Reorder(existing, []int64{2, 1})
	if got[0].URL != "https://b.example" || got[0].Title != "B" {
		t.Errorf("Reorder altered the Bookmark it moved: %+v", got[0])
	}
}

func TestReorderKeepsBookmarksTheListLeavesOut(t *testing.T) {
	existing := []domain.Bookmark{
		{ID: 1, Position: 0},
		{ID: 2, Position: 1},
		{ID: 3, Position: 2},
	}
	// 3 is named first; 999 names nothing; 1 and 2 are left out and follow.
	got := domain.Reorder(existing, []int64{3, 999})
	wantIDs := []int64{3, 1, 2}
	for i, b := range got {
		if b.ID != wantIDs[i] {
			t.Errorf("position %d holds id %d, want %d", i, b.ID, wantIDs[i])
		}
	}
}

func TestReorderIgnoresARepeatedID(t *testing.T) {
	existing := []domain.Bookmark{
		{ID: 1, Position: 0},
		{ID: 2, Position: 1},
		{ID: 3, Position: 2},
	}
	got := domain.Reorder(existing, []int64{2, 2, 1, 3})
	wantIDs := []int64{2, 1, 3}
	if len(got) != len(wantIDs) {
		t.Fatalf("got %d Bookmarks, want %d", len(got), len(wantIDs))
	}
	for i, b := range got {
		if b.ID != wantIDs[i] {
			t.Errorf("position %d holds id %d, want %d", i, b.ID, wantIDs[i])
		}
	}
}

func TestReorderOfNothingIsEmpty(t *testing.T) {
	if got := domain.Reorder(nil, []int64{1, 2}); len(got) != 0 {
		t.Errorf("got %v, want no Bookmarks", got)
	}
}

// TestNormalizeURLRefusesAURLWithNoHost covers the guard's own edge: a port with
// no hostname parses cleanly, so checking Host is not enough — ":8080" is not a
// host, and saving it would give a Bookmark the Title ":8080".
func TestNormalizeURLRefusesAURLWithNoHost(t *testing.T) {
	for _, raw := range []string{"https://:8080", "https://", "http:///path"} {
		t.Run(raw, func(t *testing.T) {
			if got, ok := domain.NormalizeURL(raw); ok {
				t.Errorf("NormalizeURL(%q) = %q, ok = true; want it refused", raw, got)
			}
		})
	}
}

// TestSaveAppendsAfterTheHighestPosition pins the append rule at the pure seam:
// the end of the grid is the highest position in use, not the number of
// Bookmarks. Those differ as soon as one has been deleted, because deleting
// never reindexes.
func TestSaveAppendsAfterTheHighestPosition(t *testing.T) {
	existing := []domain.Bookmark{{ID: 1, Position: 0}, {ID: 4, Position: 3}}

	res, ok := domain.Save(existing, "https://e.example", "E", "", false, time.Now())
	if !ok {
		t.Fatal("Save refused a usable URL")
	}
	if res.Bookmark.Position != 4 {
		t.Errorf("Position = %d, want 4: one past the highest position in use", res.Bookmark.Position)
	}
}

// TestSaveAppendsToAnEmptyGrid keeps the first Bookmark at position 0.
func TestSaveAppendsToAnEmptyGrid(t *testing.T) {
	res, ok := domain.Save(nil, "https://a.example", "A", "", false, time.Now())
	if !ok {
		t.Fatal("Save refused a usable URL")
	}
	if res.Bookmark.Position != 0 {
		t.Errorf("Position = %d, want 0", res.Bookmark.Position)
	}
}

// TestNormalizeURLOnlyAcceptsHTTPAndHTTPS keeps a scheme the app can neither
// fetch nor safely render out of storage. html/template neutralises a
// javascript: href, but the value should never be stored in the first place.
func TestNormalizeURLOnlyAcceptsHTTPAndHTTPS(t *testing.T) {
	for _, raw := range []string{
		"javascript://evil.example/%0Aalert(1)",
		"file://host/etc/passwd",
		"ftp://example.com/page",
	} {
		t.Run(raw, func(t *testing.T) {
			if got, ok := domain.NormalizeURL(raw); ok {
				t.Errorf("NormalizeURL(%q) = %q, ok = true; want the scheme refused", raw, got)
			}
		})
	}
}
