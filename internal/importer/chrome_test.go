package importer_test

import (
	"strings"
	"testing"
	"time"

	"github.com/afrieirham/bookmark-manager/internal/importer"
)

func TestChromeExportPreservesHierarchyTitlesAndDates(t *testing.T) {
	// Chrome omits </DT> and </P>; this must not be parsed as an HTML5 tree.
	export := `<!DOCTYPE NETSCAPE-Bookmark-file-1>
<META HTTP-EQUIV="Content-Type" CONTENT="text/html; charset=UTF-8">
<TITLE>Bookmarks</TITLE><H1>Bookmarks</H1>
<DL><p>
  <DT><H3 PERSONAL_TOOLBAR_FOLDER="true">Bookmarks bar</H3>
  <DL><p>
    <DT><A HREF="https://example.com/?a=1&amp;b=2#section" ADD_DATE="1700000000" ICON="data:image/png;base64,ignored">A &amp; B</A>
    <DT><H3>研究</H3><DL><p>
      <DT><A HREF="https://nested.example" ADD_DATE="invalid">Nested</A>
    </DL><p>
    <DT><H3>Empty</H3><DL><p></DL><p>
    <DT><A HREF="https://after.example">After nested folder</A>
  </DL><p>
  <DT><H3>Other bookmarks</H3><DL><p>
    <DT><A HREF="http://other.example"></A>
  </DL><p>
  <DT><A HREF="https://root.example">Root</A>
</DL><p>`
	doc, err := importer.Parse(strings.NewReader(export))
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Folders) != 4 || len(doc.Bookmarks) != 5 {
		t.Fatalf("got %d folders, %d bookmarks", len(doc.Folders), len(doc.Bookmarks))
	}
	for i, parent := range []int64{0, 1, 1, 0} {
		if doc.Folders[i].ParentID != parent {
			t.Errorf("folder %d parent = %d, want %d", i, doc.Folders[i].ParentID, parent)
		}
	}
	for i, folder := range []int64{1, 2, 1, 4, 0} {
		if doc.Bookmarks[i].FolderID != folder {
			t.Errorf("bookmark %d folder = %d, want %d", i, doc.Bookmarks[i].FolderID, folder)
		}
	}
	b := doc.Bookmarks[0]
	if b.Title != "A & B" || b.URL != "https://example.com/?a=1&b=2" || !b.CreatedAt.Equal(time.Unix(1700000000, 0)) {
		t.Errorf("first bookmark = %+v", b)
	}
	if !doc.Bookmarks[1].CreatedAt.IsZero() || doc.Bookmarks[3].Title != "other.example" {
		t.Errorf("date/title fallbacks = %+v", doc.Bookmarks)
	}
}

func TestUnsupportedURLsAreCountedAndNeverImported(t *testing.T) {
	var entries strings.Builder
	for _, href := range []string{"javascript:alert(1)", "chrome://settings", "file:///tmp/private", "ftp://files.example", "", "/relative", "example.com"} {
		entries.WriteString(`<DT><A HREF="` + href + `">Unsupported</A>`)
	}
	doc, err := importer.Parse(strings.NewReader(`<!DOCTYPE NETSCAPE-Bookmark-file-1><DL>` + entries.String() + `<DT><A HREF="https://valid.example">Valid</A></DL>`))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Skipped != 7 || len(doc.Bookmarks) != 1 {
		t.Fatalf("got %+v, want 7 skipped and one bookmark", doc)
	}
}

func TestInvalidOrTruncatedFilesReturnNoPartialDocument(t *testing.T) {
	for _, contents := range []string{
		"", `<html><a href="https://example.com">A page</a></html>`,
		`{"roots":{}}`, `<!DOCTYPE NETSCAPE-Bookmark-file-1>`,
		`<!DOCTYPE NETSCAPE-Bookmark-file-1><DL><A HREF="https://example.com">Good</A>`,
		`<!DOCTYPE NETSCAPE-Bookmark-file-1><DL><H3>Missing list</H3></DL>`,
		`<!DOCTYPE NETSCAPE-Bookmark-file-1><DL><A HREF="https://example.com">Unclosed</DL>`,
		`<!DOCTYPE NETSCAPE-Bookmark-file-1><DL></DL></DL>`,
		`<!DOCTYPE NETSCAPE-Bookmark-file-1>` + strings.Repeat("<DL>", 129),
	} {
		doc, err := importer.Parse(strings.NewReader(contents))
		if err == nil || len(doc.Bookmarks) != 0 || len(doc.Folders) != 0 {
			t.Errorf("Parse(%q) = %+v, %v; want error and empty document", contents, doc, err)
		}
	}
}

func TestEmptyExportIsValid(t *testing.T) {
	doc, err := importer.Parse(strings.NewReader(`<!doctype netscape-bookmark-file-1><dl><p></dl><p>`))
	if err != nil || len(doc.Bookmarks) != 0 {
		t.Fatalf("empty export = %+v, %v", doc, err)
	}
}
