// Package config loads the P0 API configuration from the environment.
package config

import (
	"os"
	"strconv"
	"time"
)

// Config is the process configuration. Every field has a safe default so the
// API can run with no environment variables at all (in-memory store).
type Config struct {
	Port                        string
	DatabaseURL                 string
	DedupeWindow                time.Duration
	MaxEvents                   int
	Version                     string
	AuthToken                   string
	AuthMode                    string
	JWTSecret                   string
	IngestToken                 string
	BusDriver                   string
	BusDir                      string
	BusBuffer                   int
	SiteConfigFile              string
	HubClientBuffer             int
	WSTrackSample               int
	IngestRateLimit             int
	IngestBurst                 int
	MaxBodyBytes                int64
	LoginMaxFailures            int
	LoginLockoutSeconds         int
	AccessTokenTTLSeconds       int
	RefreshTokenTTLSeconds      int
	PatrolTickSeconds           int
	PatrolPendingTimeoutMinutes int
	PatrolRunningTimeoutMinutes int
	IngestFallbackURL           string
	RateStore                   string
	ExportDir                   string
	ExportWorkers               int
	ExportRetention             time.Duration
	ExportMaxJobs               int
	ReplicaID                   string
	ExportHeartbeatSeconds      int
}

// Load reads the environment, falling back to P0 defaults.
func Load() Config {
	cfg := Config{
		Port:                        env("PORT", "8080"),
		DatabaseURL:                 env("DATABASE_URL", ""),
		DedupeWindow:                time.Duration(envInt("DEDUPE_WINDOW_SECONDS", 5)) * time.Second,
		MaxEvents:                   envInt("MAX_EVENTS", 20000),
		Version:                     env("VERSION", "0.1.0"),
		AuthToken:                   env("AUTH_TOKEN", ""),
		AuthMode:                    env("AUTH_MODE", "on"),
		JWTSecret:                   env("JWT_SECRET", "dev-secret-change-me"),
		IngestToken:                 env("INGEST_TOKEN", ""),
		BusDriver:                   env("BUS_DRIVER", "memory"),
		BusDir:                      env("BUS_DIR", "data/bus"),
		BusBuffer:                   envInt("BUS_BUFFER", 10000),
		SiteConfigFile:              env("SITE_CONFIG_FILE", ""),
		HubClientBuffer:             envInt("HUB_CLIENT_BUFFER", 256),
		WSTrackSample:               envInt("WS_TRACK_SAMPLE", 1),
		IngestRateLimit:             envInt("INGEST_RATE_LIMIT", 0),
		IngestBurst:                 envInt("INGEST_BURST", 200),
		MaxBodyBytes:                int64(envInt("MAX_BODY_BYTES", 1048576)),
		LoginMaxFailures:            envInt("LOGIN_MAX_FAILURES", 5),
		LoginLockoutSeconds:         envInt("LOGIN_LOCKOUT_SECONDS", 60),
		AccessTokenTTLSeconds:       envInt("ACCESS_TOKEN_TTL_SECONDS", 3600),
		RefreshTokenTTLSeconds:      envInt("REFRESH_TOKEN_TTL_SECONDS", 604800),
		PatrolTickSeconds:           envInt("PATROL_TICK_SECONDS", 15),
		PatrolPendingTimeoutMinutes: envInt("PATROL_PENDING_TIMEOUT_MINUTES", 60),
		PatrolRunningTimeoutMinutes: envInt("PATROL_RUNNING_TIMEOUT_MINUTES", 120),
		IngestFallbackURL:           env("INGEST_FALLBACK_URL", ""),
		RateStore:                   env("RATE_STORE", "memory"),
		ExportDir:                   env("EXPORT_DIR", "data/exports"),
		ExportWorkers:               envInt("EXPORT_WORKERS", 2),
		ExportRetention:             envDuration("EXPORT_RETENTION", 168*time.Hour),
		ExportMaxJobs:               envInt("EXPORT_MAX_JOBS", 200),
		ReplicaID:                   env("REPLICA_ID", ""),
		ExportHeartbeatSeconds:      envInt("EXPORT_HEARTBEAT_SECONDS", 10),
	}
	if cfg.MaxEvents < 100 {
		cfg.MaxEvents = 100
	}
	return cfg
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

// envDuration parses a Go duration ("168h", "0"); empty or invalid uses the fallback.
func envDuration(key string, fallback time.Duration) time.Duration {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envInt(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}
