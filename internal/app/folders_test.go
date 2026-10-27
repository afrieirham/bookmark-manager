package app_test

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/afrieirham/bookmark-manager/internal/app"
)

func upload(t *testing.T, h http.Handler, contents, folder string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	file, err := form.CreateFormFile("file", "bookmarks.html")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte(contents)); err != nil {
		t.Fatal(err)
	}
	if err := form.WriteField("folder", folder); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	return serve(t, h, request(t, http.MethodPost, "/import", map[string]string{"Content-Type": form.FormDataContentType()}, &body))
}

func createFolder(t *testing.T, h http.Handler, title, parent string) {
	t.Helper()
	rr := post(t, h, "/folders/create", url.Values{"title": {title}, "parent": {parent}})
	if rr.Code != http.StatusSeeOther || strings.Contains(rr.Header().Get("Location"), "error") {
		t.Fatalf("create folder = %d, %s, %s", rr.Code, rr.Header().Get("Location"), rr.Body.String())
	}
}

func TestFoldersNavigateAndBookmarkMovesPersist(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "db.sqlite")
	h := newHandler(t, dbPath, stubFetcher{})
	createFolder(t, h, "Work", "0")
	createFolder(t, h, "Research", "1")
	get(t, h, "/save?u=https://root.example&title=Root", nil)
	get(t, h, "/save?u=https://work.example&title=Work&folder=1", nil)
	get(t, h, "/save?u=https://nested.example&title=Nested&folder=2", nil)
	for _, tc := range []struct{ location, want string }{{"/", "1"}, {"/?folder=1", "2"}, {"/?folder=2", "3"}} {
		page := get(t, h, tc.location, nil)
		if page.Code != http.StatusOK || !equalIDs(gridOrder(t, page.Body.String()), []string{tc.want}) {
			t.Fatalf("%s = %d, ids %v", tc.location, page.Code, gridOrder(t, page.Body.String()))
		}
	}
	page := get(t, h, "/?saved=3", nil).Body.String()
	if !strings.Contains(page, `Work / Research`) || !strings.Contains(page, `value="2" selected`) {
		t.Fatalf("edit dialog lacks the selected nested folder: %s", page)
	}
	rr := post(t, h, "/rename", url.Values{"id": {"3"}, "title": {"Moved"}, "u": {"https://nested.example"}, "folder": {"1"}})
	if rr.Header().Get("Location") != "/?folder=1" {
		t.Fatalf("move redirect = %s", rr.Header().Get("Location"))
	}
	restarted := app.New(openStore(t, dbPath), stubFetcher{}, app.Config{Token: testToken}).Handler()
	if got := gridOrder(t, get(t, restarted, "/?folder=1", nil).Body.String()); !equalIDs(got, []string{"2", "3"}) {
		t.Fatalf("moved bookmark not persisted: %v", got)
	}
	if got := gridOrder(t, get(t, restarted, "/?folder=2", nil).Body.String()); len(got) != 0 {
		t.Fatalf("old folder still contains moved bookmark: %v", got)
	}
}

func TestFolderRenameAndRemovalKeepContents(t *testing.T) {
	h := newHandler(t, filepath.Join(t.TempDir(), "db.sqlite"), stubFetcher{})
	createFolder(t, h, "Work", "0")
	createFolder(t, h, "Research", "1")
	get(t, h, "/save?u=https://saved.example&title=Saved&folder=1", nil)
	post(t, h, "/folders/rename", url.Values{"id": {"1"}, "title": {"Projects"}})
	if page := get(t, h, "/", nil).Body.String(); !strings.Contains(page, "Open folder Projects") || strings.Contains(page, "Open folder Work") {
		t.Fatal("folder rename not shown on Start page")
	}
	rr := post(t, h, "/folders/delete", url.Values{"id": {"1"}})
	page := get(t, h, rr.Header().Get("Location"), nil).Body.String()
	if !equalIDs(gridOrder(t, page), []string{"1"}) || !strings.Contains(page, "Open folder Research") {
		t.Fatal("removing folder did not preserve its contents")
	}
	if rr := get(t, h, "/?folder=1", nil); rr.Code != http.StatusNotFound {
		t.Fatalf("removed folder status = %d", rr.Code)
	}
}

func TestFolderValidationDoesNotChangeBookmarks(t *testing.T) {
	h := newHandler(t, filepath.Join(t.TempDir(), "db.sqlite"), stubFetcher{})
	get(t, h, "/save?u=https://saved.example&title=Keep", nil)
	for _, folder := range []string{"-1", "nonsense", "999"} {
		t.Run(folder, func(t *testing.T) {
			rr := get(t, h, "/save?u=https://new.example&folder="+folder, jsonClient())
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("save into missing folder status = %d", rr.Code)
			}
			rr = post(t, h, "/rename", url.Values{"id": {"1"}, "title": {"Changed"}, "folder": {folder}})
			page := get(t, h, "/", nil).Body.String()
			if !strings.Contains(rr.Header().Get("Location"), "error=folder") || !strings.Contains(page, `class="label">Keep</span>`) || len(gridOrder(t, page)) != 1 {
				t.Fatal("invalid destination changed a bookmark")
			}
		})
	}
}

func TestReorderingOneFolderCannotChangeAnotherFolder(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "db.sqlite")
	h := newHandler(t, dbPath, stubFetcher{})
	createFolder(t, h, "Work", "0")
	for i, folder := range []int{0, 1, 0, 1} {
		get(t, h, fmt.Sprintf("/save?u=https://%d.example&title=Saved&folder=%d", i, folder), nil)
	}
	post(t, h, "/reorder", url.Values{"folder": {"1"}, "ids": {"4", "3", "2", "1"}})
	if got := gridOrder(t, get(t, h, "/?folder=1", nil).Body.String()); !equalIDs(got, []string{"4", "2"}) {
		t.Fatalf("folder order = %v", got)
	}
	if got := gridOrder(t, get(t, h, "/", nil).Body.String()); !equalIDs(got, []string{"1", "3"}) {
		t.Fatalf("root order changed: %v", got)
	}
}

const chromeExport = `<!DOCTYPE NETSCAPE-Bookmark-file-1><TITLE>Bookmarks</TITLE><H1>Bookmarks</H1><DL><p>
<DT><H3 PERSONAL_TOOLBAR_FOLDER="true">Bookmarks bar</H3><DL><p>
<DT><A HREF="https://existing.example">Do not overwrite</A>
<DT><H3>Research &amp; reading</H3><DL><p>
<DT><A HREF="https://new.example">Imported &amp; escaped</A>
<DT><A HREF="https://new.example#duplicate">Duplicate in file</A>
<DT><A HREF="javascript:alert(1)">Bookmarklet</A>
</DL><p></DL><p></DL><p>`

func TestChromeImportWorksEndToEndWithoutFetchingSites(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "db.sqlite")
	store := openStore(t, dbPath)
	h := app.New(store, app.FetcherFunc(func(context.Context, string) (string, bool) {
		t.Fatal("import must not fetch bookmarked sites")
		return "", false
	}), app.Config{Token: testToken}).Handler()
	get(t, h, "/save?u=https://existing.example&title=My+title", nil)
	createFolder(t, h, "Imports", "0")
	rr := upload(t, h, chromeExport, "1")
	loc := rr.Header().Get("Location")
	if rr.Code != http.StatusSeeOther || !strings.Contains(loc, "added=1&folders=2&duplicates=2&skipped=1") {
		t.Fatalf("import = %d, %s, %s", rr.Code, loc, rr.Body.String())
	}
	if page := get(t, h, loc, nil).Body.String(); !strings.Contains(page, "Import complete: 1 bookmarks added") || !strings.Contains(page, "Open folder Bookmarks bar") {
		t.Fatal("import result is not shown")
	}
	bookmarks, err := store.List(context.Background())
	if err != nil || len(bookmarks) != 2 || bookmarks[0].Title != "My title" || bookmarks[0].FolderID != 0 || bookmarks[1].FolderID != 3 {
		t.Fatalf("imported bookmarks = %+v, %v", bookmarks, err)
	}
	rr = upload(t, h, chromeExport, "1")
	if !strings.Contains(rr.Header().Get("Location"), "added=0&folders=0&duplicates=3&skipped=1") {
		t.Fatalf("repeat import = %s", rr.Header().Get("Location"))
	}
	restarted := app.New(openStore(t, dbPath), stubFetcher{}, app.Config{Token: testToken}).Handler()
	if page := get(t, restarted, "/?folder=3", nil).Body.String(); !strings.Contains(page, "Imported &amp; escaped") {
		t.Fatal("import not persisted")
	}
}

func TestInvalidImportsDoNotLeavePartialData(t *testing.T) {
	for _, contents := range []string{"not an export", strings.TrimSuffix(chromeExport, "</DL><p>"), `<html><a href="https://example.com">A webpage</a></html>`} {
		h := newHandler(t, filepath.Join(t.TempDir(), "db.sqlite"), stubFetcher{})
		rr := upload(t, h, contents, "0")
		if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "Choose a bookmarks HTML file") {
			t.Fatalf("invalid import = %d, %s", rr.Code, rr.Body.String())
		}
		page := get(t, h, "/", nil).Body.String()
		if len(gridOrder(t, page)) != 0 || strings.Contains(page, "Open folder Bookmarks bar") {
			t.Fatal("invalid import left partial data")
		}
	}
}

func TestImportRejectsOversizedUpload(t *testing.T) {
	h := newHandler(t, filepath.Join(t.TempDir(), "db.sqlite"), stubFetcher{})
	rr := upload(t, h, strings.Repeat("x", (20<<20)+1), "0")
	if rr.Code != http.StatusRequestEntityTooLarge || !strings.Contains(rr.Body.String(), "That file is too large") {
		t.Fatalf("oversized import status = %d", rr.Code)
	}
}

func TestImportErrorKeepsDestinationAndMissingDestinationWritesNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.sqlite")
	h := newHandler(t, path, stubFetcher{})
	createFolder(t, h, "Destination", "0")
	rr := upload(t, h, "not bookmarks", "1")
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), `<option value="1" selected>Destination</option>`) {
		t.Fatal("invalid file did not keep the chosen destination")
	}
	rr = upload(t, h, chromeExport, "999")
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "destination folder no longer exists") {
		t.Fatalf("missing destination status = %d", rr.Code)
	}
	page := get(t, h, "/", nil).Body.String()
	if len(gridOrder(t, page)) != 0 || strings.Contains(page, "Open folder Bookmarks bar") {
		t.Fatal("missing destination left partial imported data")
	}
}

func TestFolderAndImportRoutesRequireAuthentication(t *testing.T) {
	h := newHandler(t, filepath.Join(t.TempDir(), "db.sqlite"), stubFetcher{})
	for _, route := range []string{"/import", "/folders/create", "/folders/rename", "/folders/delete"} {
		req := httptest.NewRequest(http.MethodPost, route, strings.NewReader(""))
		if rr := serve(t, h, req); rr.Code != http.StatusUnauthorized {
			t.Errorf("unauthenticated POST %s = %d", route, rr.Code)
		}
		if rr := get(t, h, route, nil); rr.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", route, rr.Code)
		}
	}
}
