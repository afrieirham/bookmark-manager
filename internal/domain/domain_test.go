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
	got, ok := domain.Reorder(existing, []int64{3, 1, 2})
	if !ok {
		t.Fatal("Reorder rejected the full list of ids")
	}
	wantIDs := []int64{3, 1, 2}
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
	got, ok := domain.Reorder(existing, []int64{2, 1})
	if !ok {
		t.Fatal("Reorder rejected the full list of ids")
	}
	if got[0].URL != "https://b.example" || got[0].Title != "B" {
		t.Errorf("Reorder altered the Bookmark it moved: %+v", got[0])
	}
}

func TestReorderRejectsAnythingButTheFullSet(t *testing.T) {
	existing := []domain.Bookmark{
		{ID: 1, Position: 0},
		{ID: 2, Position: 1},
		{ID: 3, Position: 2},
	}
	cases := []struct {
		name string
		ids  []int64
	}{
		{"too few", []int64{2, 1}},
		{"an unknown id", []int64{1, 2, 99}},
		{"a repeated id", []int64{1, 1, 2}},
		{"empty", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, ok := domain.Reorder(existing, c.ids); ok {
				t.Errorf("Reorder(%v) accepted a list that is not the full set", c.ids)
			}
		})
	}
}

func TestReorderAppliesTheGivenOrder(t *testing.T) {
	existing := []domain.Bookmark{
		{ID: 1, Title: "A", Position: 0},
		{ID: 2, Title: "B", Position: 1},
		{ID: 3, Title: "C", Position: 2},
	}

	got := domain.Reorder(existing, []int64{3, 1, 2})

	want := []int64{3, 1, 2}
	if len(got) != len(want) {
		t.Fatalf("got %d Bookmarks, want %d", len(got), len(want))
	}
	for i, id := range want {
		if got[i].ID != id {
			t.Errorf("position %d holds Bookmark %d, want %d", i, got[i].ID, id)
		}
		if got[i].Position != i {
			t.Errorf("Bookmark %d has Position %d, want %d", got[i].ID, got[i].Position, i)
		}
	}
}

func TestReorderIgnoresUnknownIDsAndRepeats(t *testing.T) {
	existing := []domain.Bookmark{{ID: 1}, {ID: 2}, {ID: 3}}

	got := domain.Reorder(existing, []int64{2, 999, 2})

	if len(got) != 3 {
		t.Fatalf("got %d Bookmarks, want every one kept", len(got))
	}
	if got[0].ID != 2 {
		t.Errorf("first Bookmark = %d, want the named 2", got[0].ID)
	}
}

func TestReorderKeepsBookmarksTheCallerLeftOut(t *testing.T) {
	existing := []domain.Bookmark{{ID: 1}, {ID: 2}, {ID: 3}}

	got := domain.Reorder(existing, []int64{3})

	if len(got) != 3 {
		t.Fatalf("got %d Bookmarks, want none dropped", len(got))
	}
	for i, id := range []int64{3, 1, 2} {
		if got[i].ID != id {
			t.Errorf("position %d holds Bookmark %d, want %d", i, got[i].ID, id)
		}
	}
}

func TestReorderOfNothingIsEmpty(t *testing.T) {
	if got := domain.Reorder(nil, []int64{1, 2}); len(got) != 0 {
		t.Errorf("got %v, want no Bookmarks", got)
	}
}
