package exports_test

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"digital-twin-platform/api/internal/exports"
)

func TestLocalDirRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store := exports.NewLocalDir(dir)
	payload := []byte(`{"tenant":"default","counts":{"cameras":6}}`)

	size, err := store.Write("exp_abc", payload)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if size != int64(len(payload)) {
		t.Fatalf("size: got %d want %d", size, len(payload))
	}

	reader, readSize, err := store.Read("exp_abc")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	defer reader.Close()
	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(got) != string(payload) || readSize != int64(len(payload)) {
		t.Fatalf("round trip mismatch: %q size=%d", got, readSize)
	}
	if _, err := os.Stat(filepath.Join(dir, "exp_abc.json")); err != nil {
		t.Fatalf("artifact file missing: %v", err)
	}
	// Temp files must not linger after an atomic write.
	if _, err := os.Stat(filepath.Join(dir, "exp_abc.json.tmp")); !os.IsNotExist(err) {
		t.Fatalf("temp file left behind: %v", err)
	}

	if err := store.Remove("exp_abc"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, _, err := store.Read("exp_abc"); !errors.Is(err, exports.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	// Removing a missing artifact is not an error.
	if err := store.Remove("exp_abc"); err != nil {
		t.Fatalf("remove missing: %v", err)
	}
}

func TestLocalDirRejectsTraversal(t *testing.T) {
	store := exports.NewLocalDir(t.TempDir())
	for _, id := range []string{"", "../etc/passwd", "a/b", `a\b`, "..", "."} {
		if _, err := store.Write(id, []byte("x")); err == nil {
			t.Fatalf("id %q should be rejected", id)
		}
		if _, _, err := store.Read(id); err == nil {
			t.Fatalf("read id %q should be rejected", id)
		}
	}
}

func TestLocalDirDescribe(t *testing.T) {
	dir := t.TempDir()
	if got := exports.NewLocalDir(dir).Describe(); !strings.Contains(got, dir) {
		t.Fatalf("describe should mention the dir, got %q", got)
	}
}
