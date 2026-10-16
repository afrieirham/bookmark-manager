// Package store persists Bookmarks in a single SQLite file.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

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
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	if err := ensureFaviconColumns(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate favicon columns: %w", err)
	}
	return &SQLite{db: db}, nil
}

func ensureFaviconColumns(db *sql.DB) error {
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
	return nil
}

// Close releases the database handle.
func (s *SQLite) Close() error { return s.db.Close() }

// List returns every Bookmark in grid order.
func (s *SQLite) List(ctx context.Context) ([]domain.Bookmark, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, url, title, created_at, position,
			favicon IS NOT NULL AND length(favicon) > 0
		 FROM bookmarks ORDER BY position, id`)
	if err != nil {
		return nil, fmt.Errorf("list: %w", err)
	}
	defer rows.Close()

	var out []domain.Bookmark
	for rows.Next() {
		var (
			b         domain.Bookmark
			createdAt string
		)
		if err := rows.Scan(&b.ID, &b.URL, &b.Title, &createdAt, &b.Position, &b.HasFavicon); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		b.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
		out = append(out, b)
	}
	return out, rows.Err()
}

// Get returns the Bookmark with id, and whether it exists.
func (s *SQLite) Get(ctx context.Context, id int64) (domain.Bookmark, bool, error) {
	var (
		b         domain.Bookmark
		createdAt string
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT id, url, title, created_at, position,
			favicon IS NOT NULL AND length(favicon) > 0
		 FROM bookmarks WHERE id = ?`, id).
		Scan(&b.ID, &b.URL, &b.Title, &createdAt, &b.Position, &b.HasFavicon)
	if err == sql.ErrNoRows {
		return domain.Bookmark{}, false, nil
	}
	if err != nil {
		return domain.Bookmark{}, false, fmt.Errorf("get: %w", err)
	}
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
		`INSERT INTO bookmarks (url, title, created_at, position, favicon, favicon_type)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		b.URL, b.Title, b.CreatedAt.UTC().Format(time.RFC3339Nano), b.Position, b.Favicon, b.FaviconType)
	if err != nil {
		return domain.Bookmark{}, fmt.Errorf("insert: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return domain.Bookmark{}, fmt.Errorf("last insert id: %w", err)
	}
	b.ID = id
	b.HasFavicon = len(b.Favicon) > 0
	return b, nil
}

// Rename sets the Title of the Bookmark with id. Renaming a missing Bookmark is
// a no-op; callers that care read it first.
func (s *SQLite) Rename(ctx context.Context, id int64, title string) error {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE bookmarks SET title = ? WHERE id = ?`, title, id); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

// Update changes a Bookmark's title and URL while keeping its identity and
// position in the grid.
func (s *SQLite) Update(ctx context.Context, id int64, title, rawURL string, favicon []byte, faviconType string) error {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE bookmarks SET title = ?, url = ?, favicon = ?, favicon_type = ? WHERE id = ?`,
		title, rawURL, favicon, faviconType, id); err != nil {
		return fmt.Errorf("update bookmark: %w", err)
	}
	return nil
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
