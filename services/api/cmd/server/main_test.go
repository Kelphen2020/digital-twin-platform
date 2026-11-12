package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"digital-twin-platform/api/internal/config"
)

// P34: cover the startup helpers that were previously untested.

func TestStoreMode(t *testing.T) {
	if got := storeMode(""); got != "memory" {
		t.Fatalf("empty DATABASE_URL should be memory, got %q", got)
	}
	if got := storeMode("postgres://x/y"); got != "postgres" {
		t.Fatalf("DATABASE_URL should be postgres, got %q", got)
	}
}

func TestLoadSite(t *testing.T) {
	site, err := loadSite(config.Config{})
	if err != nil || site.Name == "" {
		t.Fatalf("default site: %v %+v", err, site)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "site.json")
	if err := os.WriteFile(path, []byte(`{"name":"集成站点"}`), 0o644); err != nil {
		t.Fatalf("write site: %v", err)
	}
	site, err = loadSite(config.Config{SiteConfigFile: path})
	if err != nil || site.Name != "集成站点" {
		t.Fatalf("custom site: %v %+v", err, site)
	}

	// A nameless file falls back to the default name.
	if err := os.WriteFile(path, []byte(`{}`), 0o644); err != nil {
		t.Fatalf("write site: %v", err)
	}
	site, err = loadSite(config.Config{SiteConfigFile: path})
	if err != nil || site.Name == "" {
		t.Fatalf("nameless site should fall back: %v %+v", err, site)
	}

	broken := filepath.Join(dir, "broken.json")
	if err := os.WriteFile(broken, []byte(`{`), 0o644); err != nil {
		t.Fatalf("write broken: %v", err)
	}
	if _, err := loadSite(config.Config{SiteConfigFile: broken}); err == nil {
		t.Fatal("malformed site config should error")
	}
	if _, err := loadSite(config.Config{SiteConfigFile: filepath.Join(dir, "missing.json")}); err == nil {
		t.Fatal("missing site config should error")
	}
}

func TestOpenBus(t *testing.T) {
	mem, err := openBus(config.Config{BusDriver: "memory", BusBuffer: 10})
	if err != nil || mem == nil {
		t.Fatalf("memory bus: %v", err)
	}
	empty, err := openBus(config.Config{BusDriver: ""})
	if err != nil || empty == nil {
		t.Fatalf("empty driver should default to memory: %v", err)
	}
	dir := t.TempDir()
	jsonl, err := openBus(config.Config{BusDriver: "jsonl", BusDir: dir})
	if err != nil || jsonl == nil {
		t.Fatalf("jsonl bus: %v", err)
	}
	if status := jsonl.Status(); status.Driver != "jsonl" {
		t.Fatalf("unexpected driver: %q", status.Driver)
	}
	if _, err := openBus(config.Config{BusDriver: "kafka"}); err == nil {
		t.Fatal("unknown driver should error")
	}
}

func TestOpenStoreMemory(t *testing.T) {
	stores, closeFn, err := openStore(context.Background(), config.Config{})
	if err != nil || stores == nil {
		t.Fatalf("open memory store: %v", err)
	}
	if closeFn != nil {
		closeFn()
	}
	if _, _, err := openStore(context.Background(), config.Config{DatabaseURL: "postgres://invalid:invalid@127.0.0.1:1/none?sslmode=disable"}); err == nil {
		t.Fatal("unreachable database should error")
	}
}
