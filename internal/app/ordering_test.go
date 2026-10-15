package app_test

import (
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/afrieirham/bookmark-manager/internal/app"
)

// gridOrder reads the Bookmark ids off the Start page, in the order the grid
// renders them.
func gridOrder(t *testing.T, page string) []string {
	t.Helper()
	var ids []string
	for _, part := range strings.Split(page, `data-id="`)[1:] {
		if end := strings.Index(part, `"`); end >= 0 {
			ids = append(ids, part[:end])
		}
	}
	return ids
}

func equalIDs(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// saveThree stores three Bookmarks and returns the handler holding them.
func saveThree(t *testing.T, h http.Handler) {
	t.Helper()
	for _, host := range []string{"a.example", "b.example", "c.example"} {
		get(t, h, "/save?u="+url.QueryEscape("https://"+host)+"&title="+url.QueryEscape(host), nil)
	}
}

func TestGridRendersBookmarksInStoredOrder(t *testing.T) {
	h := newHandler(t, filepath.Join(t.TempDir(), "db.sqlite"), stubFetcher{})
	saveThree(t, h)

	got := gridOrder(t, get(t, h, "/", nil).Body.String())
	if !equalIDs(got, []string{"1", "2", "3"}) {
		t.Errorf("grid order = %v, want the order they were saved in", got)
	}
}

func TestReorderStoresTheOrderAndItSurvivesAReload(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "db.sqlite")
	h := newHandler(t, dbPath, stubFetcher{})
	saveThree(t, h)

	rr := post(t, h, "/reorder", url.Values{"ids": {"3", "1", "2"}})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rr.Code)
	}

	if got := gridOrder(t, get(t, h, "/", nil).Body.String()); !equalIDs(got, []string{"3", "1", "2"}) {
		t.Errorf("grid order after reorder = %v, want [3 1 2]", got)
	}

	restarted := app.New(openStore(t, dbPath), stubFetcher{}, app.Config{Token: testToken}).Handler()
	if got := gridOrder(t, get(t, restarted, "/", nil).Body.String()); !equalIDs(got, []string{"3", "1", "2"}) {
		t.Errorf("grid order after a restart = %v, want [3 1 2]", got)
	}
}

func TestNewBookmarkIsAppendedAtTheEnd(t *testing.T) {
	h := newHandler(t, filepath.Join(t.TempDir(), "db.sqlite"), stubFetcher{})
	saveThree(t, h)
	post(t, h, "/reorder", url.Values{"ids": {"2", "3", "1"}})

	get(t, h, "/save?u="+url.QueryEscape("https://d.example")+"&title=D", nil)

	got := gridOrder(t, get(t, h, "/", nil).Body.String())
	if !equalIDs(got, []string{"2", "3", "1", "4"}) {
		t.Errorf("grid order = %v, want the new Bookmark last: [2 3 1 4]", got)
	}
}

func TestReorderLeavesOutNoBookmark(t *testing.T) {
	h := newHandler(t, filepath.Join(t.TempDir(), "db.sqlite"), stubFetcher{})
	saveThree(t, h)

	// 999 names no Bookmark, and 1 and 2 are left out of the list.
	post(t, h, "/reorder", url.Values{"ids": {"3", "999"}})

	got := gridOrder(t, get(t, h, "/", nil).Body.String())
	if !equalIDs(got, []string{"3", "1", "2"}) {
		t.Errorf("grid order = %v, want the named Bookmark first and the rest kept: [3 1 2]", got)
	}
}

func TestReorderIgnoresARepeatedID(t *testing.T) {
	h := newHandler(t, filepath.Join(t.TempDir(), "db.sqlite"), stubFetcher{})
	saveThree(t, h)

	post(t, h, "/reorder", url.Values{"ids": {"2", "2", "1", "3"}})

	got := gridOrder(t, get(t, h, "/", nil).Body.String())
	if !equalIDs(got, []string{"2", "1", "3"}) {
		t.Errorf("grid order = %v, want each Bookmark once: [2 1 3]", got)
	}
}

func TestReorderRunsAsOneTransaction(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "db.sqlite")
	h := newHandler(t, dbPath, stubFetcher{})
	saveThree(t, h)

	post(t, h, "/reorder", url.Values{"ids": {"3", "2", "1"}})

	// Positions are contiguous, so a half-applied reorder would show up as a
	// gap or a duplicate rather than as a different order.
	got := gridOrder(t, get(t, h, "/", nil).Body.String())
	if len(got) != 3 {
		t.Fatalf("grid has %d Bookmarks, want 3: %v", len(got), got)
	}
	for _, id := range []string{"1", "2", "3"} {
		if strings.Count(strings.Join(got, ","), id) != 1 {
			t.Errorf("Bookmark %s appears %d times in %v, want once",
				id, strings.Count(strings.Join(got, ","), id), got)
		}
	}
}
