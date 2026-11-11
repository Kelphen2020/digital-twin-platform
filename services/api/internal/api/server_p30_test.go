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
	"digital-twin-platform/api/internal/metrics"
	"digital-twin-platform/api/internal/realtime"
	"digital-twin-platform/api/internal/rules"
	"digital-twin-platform/api/internal/store"
)

func newP30Server(t *testing.T) (*httptest.Server, *metrics.Metrics) {
	t.Helper()
	memory := store.NewMemory(2000)
	memory.Seed(store.SeedCameras(), store.SeedAreas())
	authService := auth.NewWithRepo("on", "test-secret", memory)
	if err := authService.EnsureSeed(context.Background()); err != nil {
		t.Fatalf("seed users: %v", err)
	}
	meters := metrics.New()
	server := api.New(memory, rules.New(5*time.Second), realtime.NewHub(), meters, "test", authService, "")
	ts := httptest.NewServer(server.Handler())
	t.Cleanup(ts.Close)
	return ts, meters
}

func TestTenantArchiveRestoreAndDelete(t *testing.T) {
	ts, _ := newP30Server(t)
	admin := login(t, ts, "admin", "Admin@123456")
	authz := "Bearer " + admin

	// Create a tenant with a seeded template + admin.
	status, body := authorized(t, http.MethodPost, ts.URL+"/api/tenants", admin, map[string]any{
		"name": "待归档租户", "template": "default",
		"admin": map[string]any{"username": "archadmin", "password": "ValidPass123"},
	})
	if status != http.StatusCreated {
		t.Fatalf("create tenant: %d %s", status, body)
	}
	var created struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(body, &created)
	if created.ID == "" {
		t.Fatalf("missing tenant id: %s", body)
	}

	// Archive.
	status, body = authorized(t, http.MethodPost, ts.URL+"/api/tenants/"+created.ID+"/archive", admin, nil)
	if status != http.StatusOK || !strings.Contains(string(body), `"archived":true`) {
		t.Fatalf("archive: %d %s", status, body)
	}
	// Archived tenant rejects requests with tenant_archived.
	status, body = authorized(t, http.MethodPost, ts.URL+"/api/auth/login", "", map[string]any{
		"username": "archadmin", "password": "ValidPass123", "tenant_id": created.ID,
	})
	if status != http.StatusForbidden || !strings.Contains(string(body), "tenant_archived") {
		t.Fatalf("archived login should be 403 tenant_archived, got %d %s", status, body)
	}
	// Default tenant cannot be archived.
	status, body = authorized(t, http.MethodPost, ts.URL+"/api/tenants/default/archive", admin, nil)
	if status != http.StatusBadRequest {
		t.Fatalf("default archive should be 400, got %d %s", status, body)
	}

	// Delete requires confirm + archived state.
	status, _ = authorized(t, http.MethodDelete, ts.URL+"/api/tenants/"+created.ID+"?confirm=wrong", admin, nil)
	if status != http.StatusBadRequest {
		t.Fatalf("bad confirm should be 400, got %d", status)
	}
	status, body = authorized(t, http.MethodDelete, ts.URL+"/api/tenants/"+created.ID+"?confirm="+created.ID, admin, nil)
	if status != http.StatusOK || !strings.Contains(string(body), `"deleted"`) {
		t.Fatalf("delete archived tenant: %d %s", status, body)
	}
	// Data is gone: the tenant is unknown now.
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/cameras", nil)
	req.Header.Set("Authorization", authz)
	req.Header.Set("X-Tenant-ID", created.ID)
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("cameras after delete: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("deleted tenant should be unknown_tenant 400, got %d", response.StatusCode)
	}

	// Delete of a non-archived tenant is 409.
	status, body = authorized(t, http.MethodPost, ts.URL+"/api/tenants", admin, map[string]any{"name": "未归档"})
	if status != http.StatusCreated {
		t.Fatalf("create tenant 2: %d %s", status, body)
	}
	var second struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(body, &second)
	status, body = authorized(t, http.MethodDelete, ts.URL+"/api/tenants/"+second.ID+"?confirm="+second.ID, admin, nil)
	if status != http.StatusConflict {
		t.Fatalf("delete without archive should be 409, got %d %s", status, body)
	}
}

func TestTenantQuotas(t *testing.T) {
	ts, meters := newP30Server(t)
	admin := login(t, ts, "admin", "Admin@123456")

	// Create tenant B with a user quota of 1 in a single PATCH after creation.
	status, body := authorized(t, http.MethodPost, ts.URL+"/api/tenants", admin, map[string]any{"name": "配额租户"})
	if status != http.StatusCreated {
		t.Fatalf("create tenant: %d %s", status, body)
	}
	var tenant struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(body, &tenant)
	status, body = authorized(t, http.MethodPatch, ts.URL+"/api/tenants/"+tenant.ID, admin, map[string]any{
		"limits": map[string]int{"max_users": 1, "max_events": 1, "ingest_qps": 2},
	})
	if status != http.StatusOK || !strings.Contains(string(body), `"max_users":1`) {
		t.Fatalf("patch limits: %d %s", status, body)
	}
	// Negative limit rejected.
	status, _ = authorized(t, http.MethodPatch, ts.URL+"/api/tenants/"+tenant.ID, admin, map[string]any{
		"limits": map[string]int{"max_users": -1},
	})
	if status != http.StatusBadRequest {
		t.Fatalf("negative limit should be 400, got %d", status)
	}

	// Bootstrap an admin inside tenant B using the default admin token + header?
	// The seeded users live in the default tenant, so log in there and switch
	// the header is blocked by cross_tenant; instead create the tenant admin
	// through a second create with template+admin.
	status, body = authorized(t, http.MethodPost, ts.URL+"/api/tenants", admin, map[string]any{
		"name": "配额租户2", "template": "default",
		"admin": map[string]any{"username": "quotaadmin", "password": "ValidPass123"},
	})
	if status != http.StatusCreated {
		t.Fatalf("create tenant 2: %d %s", status, body)
	}
	var tenant2 struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(body, &tenant2)

	// User quota: tenant2 has 1 user (quotaadmin); set max_users=1 then add → 429.
	status, body = authorized(t, http.MethodPatch, ts.URL+"/api/tenants/"+tenant2.ID, admin, map[string]any{
		"limits": map[string]int{"max_users": 1},
	})
	if status != http.StatusOK {
		t.Fatalf("patch limits 2: %d %s", status, body)
	}
	quotaAdmin := loginTenant(t, ts, tenant2.ID, "quotaadmin", "ValidPass123")
	status, body = authorizedTenant(t, http.MethodPost, ts.URL+"/api/users", quotaAdmin, tenant2.ID, map[string]any{
		"username": "extra_user", "password": "ValidPass123", "role": "viewer",
	})
	if status != http.StatusTooManyRequests || !strings.Contains(string(body), "quota_exceeded") {
		t.Fatalf("user quota should be 429, got %d %s", status, body)
	}

	// Event quota: max_events=1 with tenant2 already holding seeded cameras.
	status, body = authorized(t, http.MethodPatch, ts.URL+"/api/tenants/"+tenant2.ID, admin, map[string]any{
		"limits": map[string]int{"max_events": 1, "ingest_qps": 0},
	})
	if status != http.StatusOK {
		t.Fatalf("patch event limit: %d %s", status, body)
	}
	for attempt := 0; attempt < 2; attempt++ {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/ingest/detections", strings.NewReader(
			`{"camera_code":"CAM-001","detections":[{"class":"intrusion","confidence":0.95}]}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Tenant-ID", tenant2.ID)
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("ingest: %v", err)
		}
		_ = response.Body.Close()
		if attempt == 0 && response.StatusCode != http.StatusAccepted {
			t.Fatalf("first ingest should pass, got %d", response.StatusCode)
		}
		if attempt == 1 && response.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("second ingest should hit max_events quota, got %d", response.StatusCode)
		}
	}

	if len(meters.TenantQuotaExceeded()) == 0 {
		t.Fatal("quota metric was not recorded")
	}
}

// authorizedTenant sends a bearer token plus the matching X-Tenant-ID header.
func authorizedTenant(t *testing.T, method, url, token, tenantID string, body any) (int, []byte) {
	t.Helper()
	return doJSONWithHeaders(t, method, url, body, map[string]string{
		"Authorization": "Bearer " + token,
		"X-Tenant-ID":   tenantID,
	})
}

// loginTenant performs a tenant-scoped login.
func loginTenant(t *testing.T, ts *httptest.Server, tenantID, username, password string) string {
	t.Helper()
	status, body := doJSON(t, http.MethodPost, ts.URL+"/api/auth/login", map[string]any{
		"username": username, "password": password, "tenant_id": tenantID,
	})
	if status != http.StatusOK {
		t.Fatalf("tenant login (%s): %d %s", tenantID, status, body)
	}
	var payload struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(body, &payload)
	return payload.Token
}
