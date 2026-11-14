package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// P37: the overview panel aggregates per-tenant operations data.
func TestTenantOverview(t *testing.T) {
	ts, _ := newP30Server(t)
	admin := login(t, ts, "admin", "Admin@123456")

	// Generate one event so the totals are non-trivial.
	ingestEvent(t, ts, "CAM-001", "intrusion")

	status, body := authorized(t, http.MethodGet, ts.URL+"/api/tenants/default/overview", admin, nil)
	if status != http.StatusOK {
		t.Fatalf("overview: %d %s", status, body)
	}
	var overview struct {
		Tenant string `json:"tenant"`
		Events struct {
			Total      int64            `json:"total"`
			Last24h    int64            `json:"last_24h"`
			BySeverity map[string]int64 `json:"by_severity"`
		} `json:"events"`
		Cameras struct {
			Total   int64 `json:"total"`
			Online  int64 `json:"online"`
			Offline int64 `json:"offline"`
		} `json:"cameras"`
		Exports struct {
			Jobs int64 `json:"jobs"`
		} `json:"exports"`
		Health struct {
			StoreMode string `json:"store_mode"`
			BusDriver string `json:"bus_driver"`
		} `json:"health"`
		Ingest struct {
			Accepted int64 `json:"accepted"`
		} `json:"ingest"`
		RecentHourly []json.RawMessage `json:"recent_hourly"`
	}
	if err := json.Unmarshal(body, &overview); err != nil {
		t.Fatalf("decode overview: %v", err)
	}
	if overview.Tenant != "default" {
		t.Fatalf("tenant: %q", overview.Tenant)
	}
	if overview.Cameras.Total != 6 {
		t.Fatalf("expected 6 seeded cameras, got %d", overview.Cameras.Total)
	}
	if overview.Cameras.Offline < 0 {
		t.Fatalf("offline cameras must not be negative: %d", overview.Cameras.Offline)
	}
	if overview.Events.Total == 0 || overview.Events.Last24h == 0 {
		t.Fatalf("events should be counted: %+v", overview.Events)
	}
	if len(overview.Events.BySeverity) == 0 {
		t.Fatalf("severity breakdown missing: %+v", overview.Events.BySeverity)
	}
	if overview.Health.StoreMode != "memory" {
		t.Fatalf("store mode: %q", overview.Health.StoreMode)
	}
	if overview.Health.BusDriver == "" {
		t.Fatal("bus driver should be reported")
	}
	if overview.Ingest.Accepted == 0 {
		t.Fatal("ingest accepted count should be non-zero")
	}
	if overview.RecentHourly == nil {
		t.Fatal("recent_hourly must be an array, not null")
	}
}

func TestTenantOverviewGuards(t *testing.T) {
	ts, _ := newP30Server(t)
	viewer := login(t, ts, "viewer", "Viewer@123")
	admin := login(t, ts, "admin", "Admin@123456")

	status, _ := authorized(t, http.MethodGet, ts.URL+"/api/tenants/default/overview", viewer, nil)
	if status != http.StatusForbidden {
		t.Fatalf("viewer overview should be 403, got %d", status)
	}
	status, body := authorized(t, http.MethodGet, ts.URL+"/api/tenants/tnt_missing/overview", admin, nil)
	if status != http.StatusNotFound || !strings.Contains(string(body), "not_found") {
		t.Fatalf("unknown tenant overview should be 404, got %d %s", status, body)
	}

	// New tenant: all counters empty but the payload must still be well formed.
	status, body = authorized(t, http.MethodPost, ts.URL+"/api/tenants", admin, map[string]any{"name": "空租户"})
	if status != http.StatusCreated {
		t.Fatalf("create tenant: %d %s", status, body)
	}
	var created struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(body, &created)
	status, body = authorized(t, http.MethodGet, ts.URL+"/api/tenants/"+created.ID+"/overview", admin, nil)
	if status != http.StatusOK {
		t.Fatalf("empty tenant overview: %d %s", status, body)
	}
	if !strings.Contains(string(body), `"cameras":{"total":0`) {
		t.Fatalf("empty tenant should report zero cameras: %s", body)
	}
}
