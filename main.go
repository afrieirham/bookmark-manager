// Command bookmark-manager is the self-hosted Start page: one Go binary with
// SQLite inside, serving the Start page and the save endpoint.
package main

import (
	"log"
	"net/http"
	"os"

	"github.com/afrieirham/bookmark-manager/internal/app"
	"github.com/afrieirham/bookmark-manager/internal/fetcher"
	"github.com/afrieirham/bookmark-manager/internal/store"
)

func main() {
	port := envOr("PORT", "8080")
	dbPath := envOr("DB_PATH", "bookmarks.sqlite")
	baseURL := os.Getenv("BASE_URL")

	st, err := store.Open(dbPath)
	if err != nil {
		log.Fatalf("open store at %s: %v", dbPath, err)
	}
	defer st.Close()

	addr := ":" + port
	log.Printf("bookmark manager listening on %s (db %s)", addr, dbPath)
	if err := http.ListenAndServe(addr, app.New(st, fetcher.New(), baseURL).Handler()); err != nil {
		log.Fatal(err)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
