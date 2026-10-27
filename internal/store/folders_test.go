package store_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/afrieirham/bookmark-manager/internal/domain"
	"github.com/afrieirham/bookmark-manager/internal/importer"
	"github.com/afrieirham/bookmark-manager/internal/store"
)

func open(t *testing.T, path string) *store.SQLite {
	t.Helper()
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestExistingDatabaseMigratesWithoutLosingBookmarks(t *testing.T) {
	for _, faviconColumns := range []bool{false, true} {
		t.Run(map[bool]string{false: "original", true: "with favicons"}[faviconColumns], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "old.sqlite")
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			columns := ""
			if faviconColumns {
				columns = ", favicon BLOB, favicon_type TEXT NOT NULL DEFAULT ''"
			}
			_, err = db.Exec(`CREATE TABLE bookmarks (id INTEGER PRIMARY KEY AUTOINCREMENT, url TEXT NOT NULL UNIQUE,
				title TEXT NOT NULL, created_at TEXT NOT NULL, position INTEGER NOT NULL` + columns + `);
				INSERT INTO bookmarks(id, url, title, created_at, position) VALUES(7, 'https://saved.example', 'Keep me', '2026-01-01T00:00:00Z', 9)`)
			if err != nil {
				t.Fatal(err)
			}
			if faviconColumns {
				if _, err := db.Exec(`UPDATE bookmarks SET favicon = X'010203', favicon_type = 'image/png'`); err != nil {
					t.Fatal(err)
				}
			}
			db.Close()
			s := open(t, path)
			b, found, err := s.Get(context.Background(), 7)
			if err != nil || !found || b.Title != "Keep me" || b.Position != 9 || b.FolderID != 0 || b.HasFavicon != faviconColumns {
				t.Fatalf("migrated bookmark = %+v, found %v, error %v", b, found, err)
			}
			// Opening again must leave the already-migrated schema usable.
			restarted := open(t, path)
			if _, err := restarted.CreateFolder(context.Background(), "New folder", 0); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestImportReusesFolderPathsAndKeepsExistingBookmarks(t *testing.T) {
	ctx := context.Background()
	s := open(t, filepath.Join(t.TempDir(), "db.sqlite"))
	parent, err := s.CreateFolder(ctx, "Imports", 0)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := importer.Parse(strings.NewReader(`<!DOCTYPE NETSCAPE-Bookmark-file-1><DL>
	<DT><H3>Bookmarks bar</H3><DL>
	<DT><A HREF="https://example.com" ADD_DATE="1700000000">Original</A>
	<DT><H3>Nested</H3><DL><DT><A HREF="https://nested.example">Nested</A></DL>
	<DT><A HREF="https://example.com#duplicate">Duplicate</A>
	<DT><A HREF="chrome://settings">Settings</A></DL></DL>`))
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.Import(ctx, doc, parent, time.Now())
	if err != nil || result != (domain.ImportResult{Added: 2, Folders: 2, Duplicates: 1, Skipped: 1}) {
		t.Fatalf("first import = %+v, %v", result, err)
	}
	before, _ := s.List(ctx)
	if before[0].Title != "Original" || !before[0].CreatedAt.Equal(time.Unix(1700000000, 0)) {
		t.Fatalf("bookmark = %+v", before[0])
	}
	// A duplicate must preserve the user's later title and chosen folder.
	if err := s.Rename(ctx, before[0].ID, "My title", 0); err != nil {
		t.Fatal(err)
	}
	result, err = s.Import(ctx, doc, parent, time.Now())
	if err != nil || result != (domain.ImportResult{Duplicates: 3, Skipped: 1}) {
		t.Fatalf("second import = %+v, %v", result, err)
	}
	after, _ := s.List(ctx)
	if len(after) != 2 || after[0].Title != "My title" || after[0].FolderID != 0 || after[0].Position != before[0].Position {
		t.Fatalf("reimport changed existing bookmarks: %+v", after)
	}
	folders, _ := s.ListFolders(ctx)
	if len(folders) != 3 || folders[1].ParentID != parent || folders[2].ParentID != folders[1].ID {
		t.Fatalf("folders = %+v", folders)
	}
}

func TestImportRollsBackFoldersAndEarlierBookmarksOnFailure(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "db.sqlite")
	s := open(t, path)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TRIGGER fail_import BEFORE INSERT ON bookmarks WHEN NEW.title = 'Fail'
		BEGIN SELECT RAISE(ABORT, 'simulated write failure'); END`); err != nil {
		t.Fatal(err)
	}
	doc := domain.ImportDocument{
		Folders:   []domain.Folder{{ID: 1, Title: "Must roll back"}},
		Bookmarks: []domain.Bookmark{{URL: "https://first.example", Title: "First", FolderID: 1}, {URL: "https://second.example", Title: "Fail", FolderID: 1}},
	}
	if _, err := s.Import(ctx, doc, 0, time.Now()); err == nil {
		t.Fatal("import succeeded despite write failure")
	}
	bookmarks, _ := s.List(ctx)
	folders, _ := s.ListFolders(ctx)
	if len(bookmarks) != 0 || len(folders) != 0 {
		t.Fatalf("partial import remains: %+v, %+v", bookmarks, folders)
	}
}

func TestRemovingFolderKeepsBookmarksAndSubfolders(t *testing.T) {
	ctx := context.Background()
	s := open(t, filepath.Join(t.TempDir(), "db.sqlite"))
	parent, _ := s.CreateFolder(ctx, "Parent", 0)
	child, _ := s.CreateFolder(ctx, "Child", parent)
	grandchild, _ := s.CreateFolder(ctx, "Grandchild", child)
	b, err := s.Insert(ctx, domain.Bookmark{URL: "https://keep.example", Title: "Keep", FolderID: child, CreatedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteFolder(ctx, child); err != nil {
		t.Fatal(err)
	}
	saved, found, err := s.Get(ctx, b.ID)
	if err != nil || !found || saved.FolderID != parent {
		t.Fatalf("bookmark after removal = %+v, %v, %v", saved, found, err)
	}
	folders, _ := s.ListFolders(ctx)
	if len(folders) != 2 || folders[1].ID != grandchild || folders[1].ParentID != parent {
		t.Fatalf("subfolder not preserved: %+v", folders)
	}
}

func TestFolderNameConflictLeavesRemovalUntouched(t *testing.T) {
	ctx := context.Background()
	s := open(t, filepath.Join(t.TempDir(), "db.sqlite"))
	parent, _ := s.CreateFolder(ctx, "Parent", 0)
	s.CreateFolder(ctx, "Same", 0)
	s.CreateFolder(ctx, "Same", parent)
	if err := s.DeleteFolder(ctx, parent); !errors.Is(err, domain.ErrFolderTitle) {
		t.Fatalf("delete conflict = %v", err)
	}
	folders, _ := s.ListFolders(ctx)
	if len(folders) != 3 {
		t.Fatalf("conflicting removal lost folders: %+v", folders)
	}
}

func TestMissingFolderCannotReceiveBookmarksOrImport(t *testing.T) {
	ctx := context.Background()
	s := open(t, filepath.Join(t.TempDir(), "db.sqlite"))
	if _, err := s.Insert(ctx, domain.Bookmark{URL: "https://example.com", FolderID: 999}); !errors.Is(err, domain.ErrFolder) {
		t.Fatalf("insert into missing folder = %v", err)
	}
	if _, err := s.Import(ctx, domain.ImportDocument{Folders: []domain.Folder{{ID: 1, Title: "Orphan"}}}, 999, time.Now()); !errors.Is(err, domain.ErrFolder) {
		t.Fatalf("import into missing folder = %v", err)
	}
}
