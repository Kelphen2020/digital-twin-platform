// Package exports persists tenant export artifacts behind a small interface so a
// cluster of replicas can share them (local disk today, object storage later).
package exports

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ErrNotFound is returned when an artifact does not exist.
var ErrNotFound = errors.New("export artifact not found")

// Store reads and writes export artifacts by job id.
type Store interface {
	// Write stores the artifact for jobID and returns its size in bytes.
	Write(jobID string, data []byte) (int64, error)
	// Read returns an io.ReadCloser for the artifact.
	Read(jobID string) (io.ReadCloser, int64, error)
	// Remove deletes the artifact; a missing artifact is not an error.
	Remove(jobID string) error
	// Describe reports the backing location for status output.
	Describe() string
}

// safeID rejects path traversal in job ids.
func safeID(jobID string) (string, error) {
	trimmed := strings.TrimSpace(jobID)
	if trimmed == "" || strings.ContainsAny(trimmed, `/\`) || trimmed == "." || trimmed == ".." {
		return "", fmt.Errorf("invalid export id %q", jobID)
	}
	return trimmed, nil
}

// LocalDir stores artifacts as files under a directory shared by every replica
// (a local path, an NFS mount or a mounted object-storage bucket).
type LocalDir struct {
	dir string
}

// NewLocalDir creates a file-backed store rooted at dir.
func NewLocalDir(dir string) *LocalDir {
	if strings.TrimSpace(dir) == "" {
		dir = "data/exports"
	}
	return &LocalDir{dir: dir}
}

// Write persists data as <dir>/<jobID>.json.
func (s *LocalDir) Write(jobID string, data []byte) (int64, error) {
	name, err := safeID(jobID)
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return 0, err
	}
	// Write to a temporary file first so readers never observe a partial artifact.
	tmp := filepath.Join(s.dir, name+".json.tmp")
	final := filepath.Join(s.dir, name+".json")
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return 0, err
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		return 0, err
	}
	return int64(len(data)), nil
}

// Read opens an artifact for streaming.
func (s *LocalDir) Read(jobID string) (io.ReadCloser, int64, error) {
	name, err := safeID(jobID)
	if err != nil {
		return nil, 0, err
	}
	path := filepath.Join(s.dir, name+".json")
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, 0, ErrNotFound
		}
		return nil, 0, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, 0, err
	}
	return file, info.Size(), nil
}

// Remove deletes the artifact; a missing file is ignored.
func (s *LocalDir) Remove(jobID string) error {
	name, err := safeID(jobID)
	if err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(s.dir, name+".json")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Describe reports the directory backing this store.
func (s *LocalDir) Describe() string { return "dir:" + s.dir }
