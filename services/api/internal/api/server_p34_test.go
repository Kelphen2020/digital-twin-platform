package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"digital-twin-platform/api/internal/api"
	"digital-twin-platform/api/internal/auth"
	"digital-twin-platform/api/internal/bus"
	"digital-twin-platform/api/internal/metrics"
	"digital-twin-platform/api/internal/model"
	"digital-twin-platform/api/internal/realtime"
	"digital-twin-platform/api/internal/rules"
	"digital-twin-platform/api/internal/store"
)

// newP34Server returns a server whose background loops can be started by tests.
func newP34Server(t *testing.T) (*api.Server, *httptest.Server, *store.Memory) {
	t.Helper()
	memory := store.NewMemory(2000)
	memory.Seed(store.SeedCameras(), store.SeedAreas())
	authService := auth.NewWithRepo("on", "test-secret", memory)
	if err := authService.EnsureSeed(context.Background()); err != nil {
		t.Fatalf("seed users: %v", err)
	}
	server := api.New(memory, rules.New(5*time.Second), realtime.NewHub(), metrics.New(), "test", authService, "")
	ts := httptest.NewServer(server.Handler())
	t.Cleanup(ts.Close)
	return server, ts, memory
}

// P34: background loops stop cleanly when their context is cancelled.
func TestBackgroundLoopsShutdown(t *testing.T) {
	server, ts, _ := newP34Server(t)
	admin := login(t, ts, "admin", "Admin@123456")
	if admin == "" {
		t.Fatal("admin login failed")
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{}, 2)
	go func() { server.RunStats(ctx, 10*time.Millisecond); done <- struct{}{} }()
	go func() { server.RunPatrolScheduler(ctx, 10*time.Millisecond); done <- struct{}{} }()
	server.StartExportRetention(ctx, 10*time.Millisecond)
	time.Sleep(80 * time.Millisecond)
	cancel()
	for i := 0; i < 2; i++ {
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("background loop did not stop after cancel")
		}
	}
}

// P34: export retention sweep runs against a real server instance.
func TestExportRetentionSweep(t *testing.T) {
	server, ts, _ := newP34Server(t)
	admin := login(t, ts, "admin", "Admin@123456")
	status, body := authorized(t, http.MethodPost, ts.URL+"/api/tenants/default/exports", admin, map[string]any{})
	if status != http.StatusAccepted {
		t.Fatalf("create job: %d %s", status, body)
	}
	// The server keeps its own default policy; call the internal sweep through a
	// purge endpoint instead, which exercises the same store path.
	status, purgeBody := authorized(t, http.MethodPost,
		ts.URL+"/api/tenants/default/exports/purge?before="+time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		admin, nil)
	if status != http.StatusOK {
		t.Fatalf("purge: %d %s", status, purgeBody)
	}
	_ = server
}

// P34: bus helper accessors used by admin tooling.
func TestBusHelperMetrics(t *testing.T) {
	memoryBus := bus.NewMemory(100)
	if _, err := memoryBus.Publish("event", model.Event{ID: "evt_bus_test", TenantID: "default"}, nil, "test"); err != nil {
		t.Fatalf("publish: %v", err)
	}
	memoryBus.MarkReplayed(1)
	memoryBus.AddError()
	if errs := memoryBus.Errors(); errs != 1 {
		t.Fatalf("errors: %d", errs)
	}
	if text := memoryBus.MetricsText(0); text == "" {
		t.Fatal("metrics text should not be empty")
	}
	if status := memoryBus.Status(); status.Published == 0 || status.NextOffset == 0 {
		t.Fatalf("unexpected bus status: %+v", status)
	}
}

// P34: tenant restore path through the real handler.
func TestTenantRestoreHandler(t *testing.T) {
	ts, _ := newP32Server(t)
	admin := login(t, ts, "admin", "Admin@123456")
	status, body := authorized(t, http.MethodPost, ts.URL+"/api/tenants", admin, map[string]any{"name": "恢复测试"})
	if status != http.StatusCreated {
		t.Fatalf("create tenant: %d %s", status, body)
	}
	var created struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(body, &created)
	status, _ = authorized(t, http.MethodPost, ts.URL+"/api/tenants/"+created.ID+"/archive", admin, nil)
	if status != http.StatusOK {
		t.Fatalf("archive: %d", status)
	}
	status, body = authorized(t, http.MethodPost, ts.URL+"/api/tenants/"+created.ID+"/restore", admin, nil)
	if status != http.StatusOK || !strings.Contains(string(body), `"archived":false`) {
		t.Fatalf("restore: %d %s", status, body)
	}
}

// P34: mount-time configuration helpers and export retention sweep.
func TestServerConfigHelpers(t *testing.T) {
	server, ts, _ := newP34Server(t)
	admin := login(t, ts, "admin", "Admin@123456")
	if admin == "" {
		t.Fatal("admin login failed")
	}
	// Configuration setters must not disturb a running server.
	server.SetSessionTTL(30 * time.Minute)
	server.SetSessionTTL(0)
	server.SetExportStore(t.TempDir(), 0, 0, 0)
	server.SetExportStore("", 1, time.Hour, 5)
	server.SetRateStore(nil)

	// Export retention sweeps every tenant (no jobs yet → nothing to prune).
	type sweeper interface{ SweepExportJobs(context.Context) }
	if s, ok := any(server).(sweeper); ok {
		s.SweepExportJobs(context.Background())
	}
	status, body := authorized(t, http.MethodGet, ts.URL+"/api/tenants/default/exports", admin, nil)
	if status != http.StatusOK {
		t.Fatalf("list exports after config: %d %s", status, body)
	}
}
