package main

import (
	"net/http"
	"testing"
	"time"

	"github.com/afrieirham/bookmark-manager/internal/fetcher"
)

func TestConfigComesFromTheEnvironment(t *testing.T) {
	t.Setenv("TOKEN", "s3cret")
	t.Setenv("BASE_URL", "https://bookmarks.example.com")
	t.Setenv("AUTH_DISABLED", "")

	cfg, err := configFromEnv()
	if err != nil {
		t.Fatalf("configFromEnv: %v", err)
	}
	if cfg.Token != "s3cret" {
		t.Errorf("Token = %q, want the TOKEN environment variable", cfg.Token)
	}
	if cfg.BaseURL != "https://bookmarks.example.com" {
		t.Errorf("BaseURL = %q, want the BASE_URL environment variable", cfg.BaseURL)
	}
	if cfg.AuthDisabled {
		t.Error("AuthDisabled is on without AUTH_DISABLED")
	}
}

func TestConfigRefusesToStartWithoutAToken(t *testing.T) {
	t.Setenv("TOKEN", "")
	t.Setenv("AUTH_DISABLED", "")

	if _, err := configFromEnv(); err == nil {
		t.Error("a missing TOKEN was accepted while authentication was on")
	}
}

func TestAuthDisabledNeedsNoToken(t *testing.T) {
	t.Setenv("TOKEN", "")
	t.Setenv("AUTH_DISABLED", "1")

	cfg, err := configFromEnv()
	if err != nil {
		t.Fatalf("configFromEnv: %v", err)
	}
	if !cfg.AuthDisabled {
		t.Error("AUTH_DISABLED=1 did not switch authentication off")
	}
}

// TestServerHasTimeouts pins the hardening: without these the default server
// lets a client hold a connection open by sending nothing.
func TestServerHasTimeouts(t *testing.T) {
	srv := newServer(":0", http.NotFoundHandler())

	for name, d := range map[string]time.Duration{
		"ReadHeaderTimeout": srv.ReadHeaderTimeout,
		"ReadTimeout":       srv.ReadTimeout,
		"WriteTimeout":      srv.WriteTimeout,
		"IdleTimeout":       srv.IdleTimeout,
	} {
		if d <= 0 {
			t.Errorf("%s = %v, want a positive timeout", name, d)
		}
	}
	// A save waits on the fetcher before it can write its answer, so a write
	// timeout shorter than that would cut off the very request it is serving.
	if srv.WriteTimeout <= fetcher.DefaultTimeout {
		t.Errorf("WriteTimeout %v does not outlast the fetcher's %v",
			srv.WriteTimeout, fetcher.DefaultTimeout)
	}
}
