package config_test

import (
	"testing"
	"time"

	"digital-twin-platform/api/internal/config"
)

func clearEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"PORT", "DATABASE_URL", "DEDUPE_WINDOW_SECONDS", "MAX_EVENTS", "VERSION",
		"AUTH_MODE", "JWT_SECRET", "INGEST_TOKEN", "BUS_DRIVER", "BUS_DIR",
		"HUB_CLIENT_BUFFER", "WS_TRACK_SAMPLE", "INGEST_RATE_LIMIT", "INGEST_BURST",
		"MAX_BODY_BYTES", "LOGIN_MAX_FAILURES", "LOGIN_LOCKOUT_SECONDS",
		"ACCESS_TOKEN_TTL_SECONDS", "REFRESH_TOKEN_TTL_SECONDS", "PATROL_TICK_SECONDS",
		"PATROL_PENDING_TIMEOUT_MINUTES", "PATROL_RUNNING_TIMEOUT_MINUTES",
		"INGEST_FALLBACK_URL",
	} {
		t.Setenv(key, "")
	}
}

func TestLoadDefaults(t *testing.T) {
	clearEnv(t)
	cfg := config.Load()

	checks := map[string]string{
		"Port":           cfg.Port,
		"AuthMode":       cfg.AuthMode,
		"BusDriver":      cfg.BusDriver,
		"JWTSecret":      cfg.JWTSecret,
		"IngestFallback": cfg.IngestFallbackURL,
	}
	want := map[string]string{
		"Port":           "8080",
		"AuthMode":       "on",
		"BusDriver":      "memory",
		"JWTSecret":      "dev-secret-change-me",
		"IngestFallback": "",
	}
	for key, got := range checks {
		if got != want[key] {
			t.Fatalf("%s = %q, want %q", key, got, want[key])
		}
	}
	if cfg.DedupeWindow != 5*time.Second {
		t.Fatalf("DedupeWindow = %v", cfg.DedupeWindow)
	}
	if cfg.MaxEvents != 20000 {
		t.Fatalf("MaxEvents = %d", cfg.MaxEvents)
	}
	if cfg.HubClientBuffer != 256 || cfg.WSTrackSample != 1 {
		t.Fatalf("hub defaults: buffer=%d sample=%d", cfg.HubClientBuffer, cfg.WSTrackSample)
	}
	if cfg.IngestRateLimit != 0 || cfg.IngestBurst != 200 || cfg.MaxBodyBytes != 1048576 {
		t.Fatalf("ingest defaults: rate=%d burst=%d body=%d", cfg.IngestRateLimit, cfg.IngestBurst, cfg.MaxBodyBytes)
	}
	if cfg.LoginMaxFailures != 5 || cfg.LoginLockoutSeconds != 60 {
		t.Fatalf("login defaults: max=%d lockout=%d", cfg.LoginMaxFailures, cfg.LoginLockoutSeconds)
	}
	if cfg.AccessTokenTTLSeconds != 3600 || cfg.RefreshTokenTTLSeconds != 604800 {
		t.Fatalf("token TTL defaults: access=%d refresh=%d", cfg.AccessTokenTTLSeconds, cfg.RefreshTokenTTLSeconds)
	}
	if cfg.PatrolTickSeconds != 15 || cfg.PatrolPendingTimeoutMinutes != 60 || cfg.PatrolRunningTimeoutMinutes != 120 {
		t.Fatalf("patrol defaults: tick=%d pending=%d running=%d", cfg.PatrolTickSeconds, cfg.PatrolPendingTimeoutMinutes, cfg.PatrolRunningTimeoutMinutes)
	}
}

func TestLoadOverridesAndSanitisation(t *testing.T) {
	clearEnv(t)
	t.Setenv("PORT", "9090")
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost/db")
	t.Setenv("DEDUPE_WINDOW_SECONDS", "9")
	t.Setenv("MAX_EVENTS", "10") // below the floor
	t.Setenv("AUTH_MODE", "off")
	t.Setenv("INGEST_RATE_LIMIT", "25")
	t.Setenv("MAX_BODY_BYTES", "2048")
	t.Setenv("LOGIN_MAX_FAILURES", "not-a-number") // falls back to the default
	t.Setenv("INGEST_FALLBACK_URL", "http://primary:8080")
	t.Setenv("BUS_DRIVER", "jsonl")

	cfg := config.Load()
	if cfg.Port != "9090" || cfg.DatabaseURL == "" || cfg.BusDriver != "jsonl" {
		t.Fatalf("string overrides not applied: %+v", cfg)
	}
	if cfg.DedupeWindow != 9*time.Second {
		t.Fatalf("dedupe window = %v", cfg.DedupeWindow)
	}
	if cfg.MaxEvents != 100 {
		t.Fatalf("MaxEvents floor not applied: %d", cfg.MaxEvents)
	}
	if cfg.AuthMode != "off" {
		t.Fatalf("AuthMode = %q", cfg.AuthMode)
	}
	if cfg.IngestRateLimit != 25 || cfg.MaxBodyBytes != 2048 {
		t.Fatalf("ingest overrides: rate=%d body=%d", cfg.IngestRateLimit, cfg.MaxBodyBytes)
	}
	if cfg.LoginMaxFailures != 5 {
		t.Fatalf("invalid int should fall back to default, got %d", cfg.LoginMaxFailures)
	}
	if cfg.IngestFallbackURL != "http://primary:8080" {
		t.Fatalf("IngestFallbackURL = %q", cfg.IngestFallbackURL)
	}
}
