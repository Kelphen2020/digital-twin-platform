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
	server.SetExportStore(dir, 2, 0, 0)
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
	// Fast jobs may finish between submissions (they do not count as active), so
	// keep submitting until the concurrency guard trips or a deadline passes.
	rejected := false
	deadline := time.Now().Add(5 * time.Second)
	for attempt := 0; attempt < 60 && time.Now().Before(deadline); attempt++ {
		code, _ := authorized(t, http.MethodPost, ts.URL+"/api/tenants/default/exports", admin, map[string]any{
			"include_tracks": true,
		})
		if code == http.StatusTooManyRequests {
			rejected = true
			break
		}
	}
	if !rejected {
		t.Fatal("expected a 429 from the export concurrency guard")
	}
}

// P33: export jobs survive an API restart because they are persisted in the store.
func TestExportJobsSurviveRestart(t *testing.T) {
	dir := t.TempDir()
	memory := store.NewMemory(2000)
	memory.Seed(store.SeedCameras(), store.SeedAreas())
	authService := auth.NewWithRepo("on", "test-secret", memory)
	if err := authService.EnsureSeed(context.Background()); err != nil {
		t.Fatalf("seed users: %v", err)
	}
	build := func() *httptest.Server {
		server := api.New(memory, rules.New(5*time.Second), realtime.NewHub(), metrics.New(), "test", authService, "")
		server.SetExportStore(dir, 2, 0, 0)
		return httptest.NewServer(server.Handler())
	}

	first := build()
	admin := login(t, first, "admin", "Admin@123456")
	status, body := authorized(t, http.MethodPost, first.URL+"/api/tenants/default/exports", admin, map[string]any{})
	if status != http.StatusAccepted {
		t.Fatalf("create job: %d %s", status, body)
	}
	var created struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(body, &created)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		status, body = authorized(t, http.MethodGet, first.URL+"/api/tenants/default/exports/"+created.ID, admin, nil)
		if status == http.StatusOK && strings.Contains(string(body), `"done"`) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	first.Close()

	// "Restart": a fresh server sharing only the store must still list the job.
	second := build()
	defer second.Close()
	admin2 := login(t, second, "admin", "Admin@123456")
	status, body = authorized(t, http.MethodGet, second.URL+"/api/tenants/default/exports", admin2, nil)
	if status != http.StatusOK || !strings.Contains(string(body), created.ID) {
		t.Fatalf("job did not survive restart: %d %s", status, body)
	}
	status, body = authorized(t, http.MethodGet, second.URL+"/api/tenants/default/exports/"+created.ID+"/download", admin2, nil)
	if status != http.StatusOK || !strings.Contains(string(body), `"counts"`) {
		t.Fatalf("download after restart: %d", status)
	}
}

// P33: purge removes finished jobs older than the cutoff.
func TestExportJobPurgeEndpoint(t *testing.T) {
	ts, dir := newP32Server(t)
	admin := login(t, ts, "admin", "Admin@123456")

	status, body := authorized(t, http.MethodPost, ts.URL+"/api/tenants/default/exports", admin, map[string]any{})
	if status != http.StatusAccepted {
		t.Fatalf("create job: %d %s", status, body)
	}
	var created struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(body, &created)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		status, body = authorized(t, http.MethodGet, ts.URL+"/api/tenants/default/exports/"+created.ID, admin, nil)
		if status == http.StatusOK && strings.Contains(string(body), `"done"`) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if _, err := os.Stat(filepath.Join(dir, created.ID+".json")); err != nil {
		t.Fatalf("artifact missing before purge: %v", err)
	}

	// A future cutoff purges everything finished.
	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	status, body = authorized(t, http.MethodPost, ts.URL+"/api/tenants/default/exports/purge?before="+future, admin, nil)
	if status != http.StatusOK || !strings.Contains(string(body), `"purged":1`) {
		t.Fatalf("purge: %d %s", status, body)
	}
	if _, err := os.Stat(filepath.Join(dir, created.ID+".json")); !os.IsNotExist(err) {
		t.Fatalf("artifact should be removed by purge: %v", err)
	}
	status, _ = authorized(t, http.MethodPost, ts.URL+"/api/tenants/default/exports/purge?before=nope", admin, nil)
	if status != http.StatusBadRequest {
		t.Fatalf("bad before should be 400, got %d", status)
	}
	viewer := login(t, ts, "viewer", "Viewer@123")
	status, _ = authorized(t, http.MethodPost, ts.URL+"/api/tenants/default/exports/purge", viewer, nil)
	if status != http.StatusForbidden {
		t.Fatalf("viewer purge should be 403, got %d", status)
	}
}
