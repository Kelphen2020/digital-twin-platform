package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"digital-twin-platform/api/internal/api"
	"digital-twin-platform/api/internal/auth"
	"digital-twin-platform/api/internal/metrics"
	"digital-twin-platform/api/internal/realtime"
	"digital-twin-platform/api/internal/rules"
	"digital-twin-platform/api/internal/store"
)

// newP32Server builds an auth-on API with an isolated export directory.
func newP32Server(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	dir := t.TempDir()
	memory := store.NewMemory(2000)
	memory.Seed(store.SeedCameras(), store.SeedAreas())
	authService := auth.NewWithRepo("on", "test-secret", memory)
	if err := authService.EnsureSeed(context.Background()); err != nil {
		t.Fatalf("seed users: %v", err)
	}
	server := api.New(memory, rules.New(5*time.Second), realtime.NewHub(), metrics.New(), "test", authService, "")
	server.SetExportStore(dir, 2)
	ts := httptest.NewServer(server.Handler())
	t.Cleanup(ts.Close)
	return ts, dir
}

func TestExportJobsLifecycle(t *testing.T) {
	ts, dir := newP32Server(t)
	admin := login(t, ts, "admin", "Admin@123456")

	status, body := authorized(t, http.MethodPost, ts.URL+"/api/tenants/default/exports", admin, map[string]any{
		"include_audit": true,
	})
	if status != http.StatusAccepted {
		t.Fatalf("create export job: %d %s", status, body)
	}
	var created struct {
		ID       string `json:"id"`
		Status   string `json:"status"`
		TenantID string `json:"tenant_id"`
	}
	_ = json.Unmarshal(body, &created)
	if created.ID == "" || created.TenantID != "default" || created.Status != "pending" {
		t.Fatalf("unexpected job payload: %s", body)
	}

	// Poll until done.
	var done struct {
		Status    string         `json:"status"`
		SizeBytes int64          `json:"size_bytes"`
		Counts    map[string]int `json:"counts"`
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		status, body = authorized(t, http.MethodGet, ts.URL+"/api/tenants/default/exports/"+created.ID, admin, nil)
		if status == http.StatusOK {
			_ = json.Unmarshal(body, &done)
			if done.Status == "done" || done.Status == "failed" {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if done.Status != "done" {
		t.Fatalf("export job did not finish: %s", body)
	}
	if done.SizeBytes <= 0 || done.Counts["cameras"] == 0 {
		t.Fatalf("export job result incomplete: %s", body)
	}
	if _, err := os.Stat(filepath.Join(dir, created.ID+".json")); err != nil {
		t.Fatalf("artifact missing: %v", err)
	}

	// List contains the job.
	status, body = authorized(t, http.MethodGet, ts.URL+"/api/tenants/default/exports", admin, nil)
	if status != http.StatusOK || !strings.Contains(string(body), created.ID) {
		t.Fatalf("list export jobs: %d %s", status, body)
	}

	// Download returns the JSON package.
	status, body = authorized(t, http.MethodGet, ts.URL+"/api/tenants/default/exports/"+created.ID+"/download", admin, nil)
	if status != http.StatusOK || !strings.Contains(string(body), `"tenant"`) || !strings.Contains(string(body), `"counts"`) {
		t.Fatalf("download: %d %s", status, body)
	}

	// Delete removes job + artifact.
	status, _ = authorized(t, http.MethodDelete, ts.URL+"/api/tenants/default/exports/"+created.ID, admin, nil)
	if status != http.StatusNoContent {
		t.Fatalf("delete job: %d", status)
	}
	if _, err := os.Stat(filepath.Join(dir, created.ID+".json")); !os.IsNotExist(err) {
		t.Fatalf("artifact should be removed: %v", err)
	}
	status, _ = authorized(t, http.MethodGet, ts.URL+"/api/tenants/default/exports/"+created.ID, admin, nil)
	if status != http.StatusNotFound {
		t.Fatalf("deleted job should be 404, got %d", status)
	}
}

func TestExportJobGuards(t *testing.T) {
	ts, _ := newP32Server(t)
	viewer := login(t, ts, "viewer", "Viewer@123")
	admin := login(t, ts, "admin", "Admin@123456")

	status, _ := authorized(t, http.MethodPost, ts.URL+"/api/tenants/default/exports", viewer, nil)
	if status != http.StatusForbidden {
		t.Fatalf("viewer export should be 403, got %d", status)
	}
	status, _ = authorized(t, http.MethodGet, ts.URL+"/api/tenants/default/exports", viewer, nil)
	if status != http.StatusForbidden {
		t.Fatalf("viewer list should be 403, got %d", status)
	}
	status, _ = authorized(t, http.MethodGet, ts.URL+"/api/tenants/default/exports/exp_missing/download", admin, nil)
	if status != http.StatusNotFound {
		t.Fatalf("missing job download should be 404, got %d", status)
	}
	// Unknown tenant.
	status, _ = authorized(t, http.MethodPost, ts.URL+"/api/tenants/tnt_missing/exports", admin, nil)
	if status != http.StatusNotFound {
		t.Fatalf("unknown tenant export should be 404, got %d", status)
	}
	// Concurrency limit: 3 quick submissions, the third must be rejected.
	codes := make([]int, 0, 3)
	for i := 0; i < 3; i++ {
		code, _ := authorized(t, http.MethodPost, ts.URL+"/api/tenants/default/exports", admin, map[string]any{})
		codes = append(codes, code)
	}
	rejected := false
	for _, code := range codes {
		if code == http.StatusTooManyRequests {
			rejected = true
		}
	}
	if !rejected {
		t.Fatalf("expected a 429 among %v", codes)
	}
}
