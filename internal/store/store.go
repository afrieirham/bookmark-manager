// Package store persists Bookmarks in a single SQLite file.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	_ "modernc.org/sqlite"

	"github.com/afrieirham/bookmark-manager/internal/domain"
)

const schema = `
CREATE TABLE IF NOT EXISTS bookmarks (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	url        TEXT    NOT NULL UNIQUE,
	title      TEXT    NOT NULL,
	created_at TEXT    NOT NULL,
	position   INTEGER NOT NULL,
	favicon    BLOB,
	favicon_type TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS folders (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	title TEXT NOT NULL,
	parent_id INTEGER NOT NULL DEFAULT 0,
	UNIQUE(parent_id, title)
);
`

// SQLite stores Bookmarks in a SQLite file at a given path.
type SQLite struct {
	db *sql.DB
}

// Open opens (creating if needed) the SQLite file at path and applies the schema.
func Open(path string) (*SQLite, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// Keep transactions and concurrent writes on one connection, including for
	// in-memory databases. A second writer must wait rather than fail as busy.
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	if err := ensureBookmarkColumns(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate bookmark columns: %w", err)
	}
	return &SQLite{db: db}, nil
}

func ensureBookmarkColumns(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(bookmarks)`)
	if err != nil {
		return err
	}
	columns := make(map[string]bool)
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, dataType string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			rows.Close()
			return err
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if !columns["favicon"] {
		if _, err := db.Exec(`ALTER TABLE bookmarks ADD COLUMN favicon BLOB`); err != nil {
			return err
		}
	}
	if !columns["favicon_type"] {
		if _, err := db.Exec(`ALTER TABLE bookmarks ADD COLUMN favicon_type TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	if !columns["folder_id"] {
		if _, err := db.Exec(`ALTER TABLE bookmarks ADD COLUMN folder_id INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}
	return nil
}

// Close releases the database handle.
func (s *SQLite) Close() error { return s.db.Close() }

// List returns every Bookmark in grid order.
func (s *SQLite) List(ctx context.Context) ([]domain.Bookmark, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, url, title, created_at, position, folder_id,
			favicon IS NOT NULL AND length(favicon) > 0
		 FROM bookmarks ORDER BY position, id`)
	if err != nil {
		return nil, fmt.Errorf("list: %w", err)
	}
	defer rows.Close()

	var out []domain.Bookmark
	for rows.Next() {
		var (
			b          domain.Bookmark
			createdAt  string
			hasFavicon int64
		)
		if err := rows.Scan(&b.ID, &b.URL, &b.Title, &createdAt, &b.Position, &b.FolderID, &hasFavicon); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		b.HasFavicon = hasFavicon != 0
		b.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
		out = append(out, b)
	}
	return out, rows.Err()
}

// Get returns the Bookmark with id, and whether it exists.
func (s *SQLite) Get(ctx context.Context, id int64) (domain.Bookmark, bool, error) {
	var (
		b          domain.Bookmark
		createdAt  string
		hasFavicon int64
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT id, url, title, created_at, position, folder_id,
			favicon IS NOT NULL AND length(favicon) > 0
		 FROM bookmarks WHERE id = ?`, id).
		Scan(&b.ID, &b.URL, &b.Title, &createdAt, &b.Position, &b.FolderID, &hasFavicon)
	if err == sql.ErrNoRows {
		return domain.Bookmark{}, false, nil
	}
	if err != nil {
		return domain.Bookmark{}, false, fmt.Errorf("get: %w", err)
	}
	b.HasFavicon = hasFavicon != 0
	b.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	return b, true, nil
}

// Favicon returns the saved image for a Bookmark.
func (s *SQLite) Favicon(ctx context.Context, id int64) ([]byte, string, bool, error) {
	var data []byte
	var mediaType string
	err := s.db.QueryRowContext(ctx,
		`SELECT favicon, favicon_type FROM bookmarks WHERE id = ?`, id).
		Scan(&data, &mediaType)
	if err == sql.ErrNoRows {
		return nil, "", false, nil
	}
	if err != nil {
		return nil, "", false, fmt.Errorf("get favicon: %w", err)
	}
	return data, mediaType, len(data) > 0, nil
}

// Insert stores b and returns it with its assigned ID. The unique index on url
// is the last line of defence against a duplicate; callers dedupe beforehand.
func (s *SQLite) Insert(ctx context.Context, b domain.Bookmark) (domain.Bookmark, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO bookmarks (url, title, created_at, position, favicon, favicon_type, folder_id)
		 SELECT ?, ?, ?, ?, ?, ?, ? WHERE ? = 0 OR EXISTS (SELECT 1 FROM folders WHERE id = ?)`,
		b.URL, b.Title, b.CreatedAt.UTC().Format(time.RFC3339Nano), b.Position, b.Favicon, b.FaviconType, b.FolderID, b.FolderID, b.FolderID)
	if err != nil {
		return domain.Bookmark{}, fmt.Errorf("insert: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.Bookmark{}, domain.ErrFolder
	}
	id, err := res.LastInsertId()
	if err != nil {
		return domain.Bookmark{}, fmt.Errorf("last insert id: %w", err)
	}
	b.ID = id
	b.HasFavicon = len(b.Favicon) > 0
	return b, nil
}

// Rename sets a Bookmark's Title and folder, preserving its URL and favicon.
func (s *SQLite) Rename(ctx context.Context, id int64, title string, folderID int64) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE bookmarks SET title = ?, folder_id = ? WHERE id = ?
		 AND (? = 0 OR EXISTS (SELECT 1 FROM folders WHERE id = ?))`, title, folderID, id, folderID, folderID)
	if err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrFolder
	}
	return nil
}

// Update changes a Bookmark's title, URL, and folder while keeping its identity and
// position in the grid.
func (s *SQLite) Update(ctx context.Context, id int64, title, rawURL string, favicon []byte, faviconType string, folderID int64) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE bookmarks SET title = ?, url = ?, favicon = ?, favicon_type = ?, folder_id = ? WHERE id = ?
		 AND (? = 0 OR EXISTS (SELECT 1 FROM folders WHERE id = ?))`,
		title, rawURL, favicon, faviconType, folderID, id, folderID, folderID)
	if err != nil {
		return fmt.Errorf("update bookmark: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrFolder
	}
	return nil
}

func (s *SQLite) ListFolders(ctx context.Context) ([]domain.Folder, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, title, parent_id FROM folders ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var folders []domain.Folder
	for rows.Next() {
		var f domain.Folder
		if err := rows.Scan(&f.ID, &f.Title, &f.ParentID); err != nil {
			return nil, err
		}
		folders = append(folders, f)
	}
	return folders, rows.Err()
}

func (s *SQLite) CreateFolder(ctx context.Context, title string, parentID int64) (int64, error) {
	title = strings.TrimSpace(title)
	if title == "" || utf8.RuneCountInString(title) > 160 {
		return 0, domain.ErrFolderTitle
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO folders(title, parent_id)
		SELECT ?, ? WHERE (? = 0 OR EXISTS (SELECT 1 FROM folders WHERE id = ?))
		AND NOT EXISTS (SELECT 1 FROM folders WHERE parent_id = ? AND title = ?)`, title, parentID, parentID, parentID, parentID, title)
	if err != nil {
		return 0, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return 0, domain.ErrFolderTitle
	}
	return res.LastInsertId()
}

func (s *SQLite) RenameFolder(ctx context.Context, id int64, title string) error {
	title = strings.TrimSpace(title)
	if title == "" || utf8.RuneCountInString(title) > 160 {
		return domain.ErrFolderTitle
	}
	res, err := s.db.ExecContext(ctx, `UPDATE folders SET title = ? WHERE id = ? AND NOT EXISTS
		(SELECT 1 FROM folders AS other WHERE other.parent_id = folders.parent_id AND other.title = ? AND other.id != folders.id)`, title, id, title)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrFolderTitle
	}
	return nil
}

// DeleteFolder removes only the container. Its bookmarks and child folders
// move to the parent, atomically; a sibling-name conflict leaves it untouched.
func (s *SQLite) DeleteFolder(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var parentID int64
	if err := tx.QueryRowContext(ctx, `SELECT parent_id FROM folders WHERE id = ?`, id).Scan(&parentID); err != nil {
		if err == sql.ErrNoRows {
			return domain.ErrFolder
		}
		return err
	}
	var conflicts int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM folders AS child JOIN folders AS sibling
		ON child.title = sibling.title WHERE child.parent_id = ? AND sibling.parent_id = ? AND sibling.id != ?`, id, parentID, id).Scan(&conflicts); err != nil {
		return err
	}
	if conflicts > 0 {
		return domain.ErrFolderTitle
	}
	if _, err := tx.ExecContext(ctx, `UPDATE bookmarks SET folder_id = ? WHERE folder_id = ?`, parentID, id); err != nil {
		return err
	}
	// Delete first so a same-named child can take the removed folder's place.
	if _, err := tx.ExecContext(ctx, `DELETE FROM folders WHERE id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE folders SET parent_id = ? WHERE parent_id = ?`, parentID, id); err != nil {
		return err
	}
	return tx.Commit()
}

// Import merges a complete parsed export in one transaction. Existing URLs
// retain their title and folder; matching folder paths are reused on reimport.
func (s *SQLite) Import(ctx context.Context, doc domain.ImportDocument, parentID int64, now time.Time) (domain.ImportResult, error) {
	result := domain.ImportResult{Skipped: doc.Skipped}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	var valid bool
	if err := tx.QueryRowContext(ctx, `SELECT ? = 0 OR EXISTS (SELECT 1 FROM folders WHERE id = ?)`, parentID, parentID).Scan(&valid); err != nil {
		return result, err
	}
	if !valid {
		return result, domain.ErrFolder
	}
	ids := map[int64]int64{0: parentID}
	for _, f := range doc.Folders {
		parent, ok := ids[f.ParentID]
		if !ok || f.ID <= 0 {
			return result, domain.ErrFolder
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO folders(title, parent_id) VALUES (?, ?) ON CONFLICT(parent_id, title) DO NOTHING`, f.Title, parent)
		if err != nil {
			return result, err
		}
		n, _ := res.RowsAffected()
		result.Folders += int(n)
		var id int64
		if err := tx.QueryRowContext(ctx, `SELECT id FROM folders WHERE title = ? AND parent_id = ?`, f.Title, parent).Scan(&id); err != nil {
			return result, err
		}
		ids[f.ID] = id
	}
	var position int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(position) + 1, 0) FROM bookmarks`).Scan(&position); err != nil {
		return result, err
	}
	for _, b := range doc.Bookmarks {
		folder, ok := ids[b.FolderID]
		if !ok {
			return result, domain.ErrFolder
		}
		if b.CreatedAt.IsZero() {
			b.CreatedAt = now
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO bookmarks(url, title, created_at, position, folder_id)
			VALUES (?, ?, ?, ?, ?) ON CONFLICT(url) DO NOTHING`, b.URL, b.Title, b.CreatedAt.UTC().Format(time.RFC3339Nano), position, folder)
		if err != nil {
			return result, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			result.Added++
			position++
		} else {
			result.Duplicates++
		}
	}
	if err := tx.Commit(); err != nil {
		return domain.ImportResult{}, err
	}
	return result, nil
}

// Delete removes the Bookmark with id. Deleting a missing Bookmark is a no-op.
func (s *SQLite) Delete(ctx context.Context, id int64) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM bookmarks WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete: %w", err)
	}
	return nil
}

// Reorder gives each Bookmark the position of its id in ids. It runs in one
// transaction, so the grid never shows a half-applied order.
func (s *SQLite) Reorder(ctx context.Context, ids []int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("reorder: %w", err)
	}
	defer tx.Rollback()

	for i, id := range ids {
		if _, err := tx.ExecContext(ctx,
			`UPDATE bookmarks SET position = ? WHERE id = ?`, i, id); err != nil {
			return fmt.Errorf("reorder: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("reorder: %w", err)
	}
	return nil
}
