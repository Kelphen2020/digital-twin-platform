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
	Port         string
	DatabaseURL  string
	DedupeWindow time.Duration
	MaxEvents    int
	Version      string
}

// Load reads the environment, falling back to P0 defaults.
func Load() Config {
	cfg := Config{
		Port:         env("PORT", "8080"),
		DatabaseURL:  env("DATABASE_URL", ""),
		DedupeWindow: time.Duration(envInt("DEDUPE_WINDOW_SECONDS", 5)) * time.Second,
		MaxEvents:    envInt("MAX_EVENTS", 20000),
		Version:      env("VERSION", "0.1.0"),
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
