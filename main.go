// Command bookmark-manager is the self-hosted Start page: one Go binary with
// SQLite inside, serving the Start page and the save endpoint.
package main

import (
	"errors"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/afrieirham/bookmark-manager/internal/app"
	"github.com/afrieirham/bookmark-manager/internal/fetcher"
	"github.com/afrieirham/bookmark-manager/internal/store"
)

func main() {
	port := envOr("PORT", "8080")
	dbPath := envOr("DB_PATH", "bookmarks.sqlite")

	cfg, err := configFromEnv()
	if err != nil {
		log.Fatal(err)
	}

	st, err := store.Open(dbPath)
	if err != nil {
		log.Fatalf("open store at %s: %v", dbPath, err)
	}
	defer st.Close()

	addr := ":" + port
	log.Printf("bookmark manager listening on %s (db %s, auth %s)", addr, dbPath, authMode(cfg))
	if err := http.ListenAndServe(addr, app.New(st, fetcher.New(), cfg).Handler()); err != nil {
		log.Fatal(err)
	}
}

// configFromEnv reads the operator's settings. Authentication is on by default,
// so a missing TOKEN is a misconfiguration rather than a reason to serve every
// Bookmark in the clear.
func configFromEnv() (app.Config, error) {
	cfg := app.Config{
		BaseURL:      os.Getenv("BASE_URL"),
		Token:        os.Getenv("TOKEN"),
		AuthDisabled: truthy(os.Getenv("AUTH_DISABLED")),
	}
	if cfg.Token == "" && !cfg.AuthDisabled {
		return cfg, errors.New("TOKEN is not set: give it your shared secret, or set AUTH_DISABLED=1 to run without authentication on a trusted network")
	}
	return cfg, nil
}

func authMode(cfg app.Config) string {
	if cfg.AuthDisabled {
		return "disabled"
	}
	return "on"
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// truthy reads a boolean environment variable the way an operator writes one.
func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
