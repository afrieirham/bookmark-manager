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
	position   INTEGER NOT NULL
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
	return &SQLite{db: db}, nil
}

// Close releases the database handle.
func (s *SQLite) Close() error { return s.db.Close() }

// List returns every Bookmark in grid order.
func (s *SQLite) List(ctx context.Context) ([]domain.Bookmark, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, url, title, created_at, position FROM bookmarks ORDER BY position, id`)
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
		if err := rows.Scan(&b.ID, &b.URL, &b.Title, &createdAt, &b.Position); err != nil {
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
		`SELECT id, url, title, created_at, position FROM bookmarks WHERE id = ?`, id).
		Scan(&b.ID, &b.URL, &b.Title, &createdAt, &b.Position)
	if err == sql.ErrNoRows {
		return domain.Bookmark{}, false, nil
	}
	if err != nil {
		return domain.Bookmark{}, false, fmt.Errorf("get: %w", err)
	}
	b.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	return b, true, nil
}

// Insert stores b and returns it with its assigned ID. The unique index on url
// is the last line of defence against a duplicate; callers dedupe beforehand.
func (s *SQLite) Insert(ctx context.Context, b domain.Bookmark) (domain.Bookmark, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO bookmarks (url, title, created_at, position) VALUES (?, ?, ?, ?)`,
		b.URL, b.Title, b.CreatedAt.UTC().Format(time.RFC3339Nano), b.Position)
	if err != nil {
		return domain.Bookmark{}, fmt.Errorf("insert: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return domain.Bookmark{}, fmt.Errorf("last insert id: %w", err)
	}
	b.ID = id
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
