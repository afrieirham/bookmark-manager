package main

import "testing"

func TestConfigComesFromTheEnvironment(t *testing.T) {
	t.Setenv("TOKEN", "s3cret")
	t.Setenv("BASE_URL", "https://bookmarks.pi")
	t.Setenv("AUTH_DISABLED", "")

	cfg, err := configFromEnv()
	if err != nil {
		t.Fatalf("configFromEnv: %v", err)
	}
	if cfg.Token != "s3cret" {
		t.Errorf("Token = %q, want the TOKEN environment variable", cfg.Token)
	}
	if cfg.BaseURL != "https://bookmarks.pi" {
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
