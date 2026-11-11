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
	"digital-twin-platform/api/internal/model"
	"digital-twin-platform/api/internal/realtime"
	"digital-twin-platform/api/internal/rules"
	"digital-twin-platform/api/internal/store"
)

// newP27Server builds an auth-on server with the seeded default tenant and
// returns the auth service so tests can bootstrap users for other tenants.
func newP27Server(t *testing.T) (*httptest.Server, *api.Server, *store.Memory, *auth.Service) {
	t.Helper()
	memory := store.NewMemory(2000)
	memory.Seed(store.SeedCameras(), store.SeedAreas())
	authService := auth.NewWithRepo("on", "p27-test-secret", memory)
	if err := authService.EnsureSeed(context.Background()); err != nil {
		t.Fatalf("seed users: %v", err)
	}
	server := api.New(memory, rules.New(5*time.Second), realtime.NewHub(), metrics.New(), "test", authService, "")
	ts := httptest.NewServer(server.Handler())
	t.Cleanup(ts.Close)
	return ts, server, memory, authService
}

// tenantRequest performs one request with optional bearer token and tenant header.
func tenantRequest(t *testing.T, method, url, token, tenant string, body any) (int, []byte) {
	t.Helper()
	status, payload := doJSONWithHeaders(t, method, url, body, map[string]string{
		"Authorization": "Bearer " + token,
		"X-Tenant-ID":   tenant,
	})
	return status, payload
}

// createTenantViaAPI creates a tenant as the default admin and returns its id.
func createTenantViaAPI(t *testing.T, tsURL, adminToken, name string) string {
	t.Helper()
	status, body := tenantRequest(t, http.MethodPost, tsURL+"/api/tenants", adminToken, "", map[string]any{"name": name})
	if status != http.StatusCreated {
		t.Fatalf("create tenant %s: %d %s", name, status, body)
	}
	var tenant model.Tenant
	if err := json.Unmarshal(body, &tenant); err != nil || tenant.ID == "" {
		t.Fatalf("tenant payload: %v %s", err, body)
	}
	if !strings.HasPrefix(tenant.ID, "tnt_") || !tenant.Active || tenant.Name != name {
		t.Fatalf("unexpected tenant: %+v", tenant)
	}
	return tenant.ID
}

func TestTenantCRUDAndRBAC(t *testing.T) {
	ts, _, _, _ := newP27Server(t)
	admin := login(t, ts, "admin", "Admin@123456")
	operator := login(t, ts, "operator", "Operator@123")
	viewer := login(t, ts, "viewer", "Viewer@123")

	tenantID := createTenantViaAPI(t, ts.URL, admin, "租户 B")

	status, body := tenantRequest(t, http.MethodGet, ts.URL+"/api/tenants", admin, "", nil)
	if status != http.StatusOK || !strings.Contains(string(body), `"id":"default"`) || !strings.Contains(string(body), tenantID) {
		t.Fatalf("tenant list: %d %s", status, body)
	}
	if status, _ = tenantRequest(t, http.MethodGet, ts.URL+"/api/tenants", operator, "", nil); status != http.StatusForbidden {
		t.Fatalf("operator tenant list: %d", status)
	}
	if status, _ = tenantRequest(t, http.MethodPost, ts.URL+"/api/tenants", viewer, "", map[string]any{"name": "x"}); status != http.StatusForbidden {
		t.Fatalf("viewer tenant create: %d", status)
	}
	if status, _ = tenantRequest(t, http.MethodPatch, ts.URL+"/api/tenants/"+tenantID, operator, "", map[string]any{"name": "x"}); status != http.StatusForbidden {
		t.Fatalf("operator tenant patch: %d", status)
	}

	status, body = tenantRequest(t, http.MethodPatch, ts.URL+"/api/tenants/"+tenantID, admin, "", map[string]any{"name": "改名租户"})
	if status != http.StatusOK || !strings.Contains(string(body), "改名租户") {
		t.Fatalf("tenant patch: %d %s", status, body)
	}
	if status, _ = tenantRequest(t, http.MethodPatch, ts.URL+"/api/tenants/tnt_missing", admin, "", map[string]any{"name": "x"}); status != http.StatusNotFound {
		t.Fatalf("missing tenant patch: %d", status)
	}

	// Audit is exposed for the default tenant and records both actions.
	status, body = tenantRequest(t, http.MethodGet, ts.URL+"/api/audit?limit=200", admin, "", nil)
	if status != http.StatusOK || !strings.Contains(string(body), "tenant.create") || !strings.Contains(string(body), "tenant.update") {
		t.Fatalf("tenant audit: %d %s", status, body)
	}
}

func TestTenantLoginJWTAndCrossTenant(t *testing.T) {
	ts, _, _, authService := newP27Server(t)
	admin := login(t, ts, "admin", "Admin@123456")
	tenantID := createTenantViaAPI(t, ts.URL, admin, "租户 C")

	// Bootstrap a tenant admin and an operator inside tenant C.
	if _, err := authService.CreateUser(store.WithTenant(context.Background(), tenantID), auth.CreateUserInput{
		Username: "adminc", Password: "AdminC@12345", DisplayName: "C admin", Role: auth.RoleAdmin, Active: true,
	}); err != nil {
		t.Fatalf("create tenant C admin: %v", err)
	}
	if _, err := authService.CreateUser(store.WithTenant(context.Background(), tenantID), auth.CreateUserInput{
		Username: "operatorc", Password: "OperatorC@123", DisplayName: "C operator", Role: auth.RoleOperator, Active: true,
	}); err != nil {
		t.Fatalf("create tenant C operator: %v", err)
	}

	// Login with tenant_id returns the tenant and a token scoped to it.
	status, body := doJSON(t, http.MethodPost, ts.URL+"/api/auth/login", map[string]string{
		"username": "adminc", "password": "AdminC@12345", "tenant_id": tenantID,
	})
	if status != http.StatusOK || !strings.Contains(string(body), `"tenant_id":"`+tenantID+`"`) {
		t.Fatalf("tenant login: %d %s", status, body)
	}
	var loginPayload struct {
		Token    string `json:"token"`
		TenantID string `json:"tenant_id"`
		User     struct {
			TenantID string `json:"tenant_id"`
		} `json:"user"`
	}
	if err := json.Unmarshal(body, &loginPayload); err != nil || loginPayload.Token == "" {
		t.Fatalf("tenant login payload: %v %s", err, body)
	}
	if loginPayload.TenantID != tenantID || loginPayload.User.TenantID != tenantID {
		t.Fatalf("login tenant mismatch: %+v", loginPayload)
	}

	// Token + header agree: success (tenant C has no seeded cameras).
	status, body = tenantRequest(t, http.MethodGet, ts.URL+"/api/cameras", loginPayload.Token, tenantID, nil)
	if status != http.StatusOK || !strings.Contains(string(body), `"items":[]`) {
		t.Fatalf("tenant C cameras: %d %s", status, body)
	}
	// Default tenant token sent to tenant C header.
	status, body = tenantRequest(t, http.MethodGet, ts.URL+"/api/cameras", admin, tenantID, nil)
	if status != http.StatusForbidden || !strings.Contains(string(body), "cross_tenant") {
		t.Fatalf("cross-tenant token: %d %s", status, body)
	}
	// Tenant C token sent without a header (defaults to "default").
	status, body = tenantRequest(t, http.MethodGet, ts.URL+"/api/cameras", loginPayload.Token, "", nil)
	if status != http.StatusForbidden || !strings.Contains(string(body), "cross_tenant") {
		t.Fatalf("tenant token on default: %d %s", status, body)
	}

	// Unknown tenant is rejected before authentication.
	status, body = tenantRequest(t, http.MethodGet, ts.URL+"/api/cameras", admin, "tnt_unknown", nil)
	if status != http.StatusBadRequest || !strings.Contains(string(body), "unknown_tenant") {
		t.Fatalf("unknown tenant: %d %s", status, body)
	}
	status, body = doJSON(t, http.MethodPost, ts.URL+"/api/auth/login", map[string]string{
		"username": "adminc", "password": "AdminC@12345", "tenant_id": "tnt_unknown",
	})
	if status != http.StatusBadRequest || !strings.Contains(string(body), "unknown_tenant") {
		t.Fatalf("login unknown tenant: %d %s", status, body)
	}

	// Deactivating the tenant blocks all requests for it (403 tenant_inactive).
	status, body = tenantRequest(t, http.MethodPatch, ts.URL+"/api/tenants/"+tenantID, admin, "", map[string]any{"active": false})
	if status != http.StatusOK {
		t.Fatalf("deactivate tenant: %d %s", status, body)
	}
	status, body = tenantRequest(t, http.MethodGet, ts.URL+"/api/cameras", loginPayload.Token, tenantID, nil)
	if status != http.StatusForbidden || !strings.Contains(string(body), "tenant_inactive") {
		t.Fatalf("inactive tenant request: %d %s", status, body)
	}
	status, body = doJSON(t, http.MethodPost, ts.URL+"/api/auth/login", map[string]string{
		"username": "adminc", "password": "AdminC@12345", "tenant_id": tenantID,
	})
	if status != http.StatusForbidden || !strings.Contains(string(body), "tenant_inactive") {
		t.Fatalf("inactive tenant login: %d %s", status, body)
	}
}

func TestTenantDataIsolationAndDefaultCompatibility(t *testing.T) {
	ts, _, _, authService := newP27Server(t)
	admin := login(t, ts, "admin", "Admin@123456")
	tenantID := createTenantViaAPI(t, ts.URL, admin, "租户 D")

	if _, err := authService.CreateUser(store.WithTenant(context.Background(), tenantID), auth.CreateUserInput{
		Username: "admind", Password: "AdminD@12345", Role: auth.RoleAdmin, Active: true,
	}); err != nil {
		t.Fatalf("create tenant D admin: %v", err)
	}
	status, body := doJSON(t, http.MethodPost, ts.URL+"/api/auth/login", map[string]string{
		"username": "admind", "password": "AdminD@12345", "tenant_id": tenantID,
	})
	if status != http.StatusOK {
		t.Fatalf("tenant D login: %d %s", status, body)
	}
	var loginPayload struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(body, &loginPayload)
	tenantD := loginPayload.Token

	// Default admin creates a user in its own tenant; tenant D users are isolated.
	status, body = tenantRequest(t, http.MethodPost, ts.URL+"/api/users", admin, "", map[string]any{
		"username": "defaultuser", "password": "Default@12345", "role": "viewer",
	})
	if status != http.StatusCreated {
		t.Fatalf("default user create: %d %s", status, body)
	}
	status, body = tenantRequest(t, http.MethodPost, ts.URL+"/api/users", tenantD, tenantID, map[string]any{
		"username": "tenantduser", "password": "TenantD@12345", "role": "viewer",
	})
	if status != http.StatusCreated {
		t.Fatalf("tenant D user create: %d %s", status, body)
	}

	_, body = tenantRequest(t, http.MethodGet, ts.URL+"/api/users", admin, "", nil)
	if strings.Contains(string(body), "tenantduser") {
		t.Fatalf("default users leaked tenant D user: %s", body)
	}
	_, body = tenantRequest(t, http.MethodGet, ts.URL+"/api/users", tenantD, tenantID, nil)
	if strings.Contains(string(body), `"username":"admin"`) || strings.Contains(string(body), `"username":"operator"`) ||
		strings.Contains(string(body), "defaultuser") {
		t.Fatalf("tenant D users contain default users: %s", body)
	}
	if !strings.Contains(string(body), "tenantduser") || !strings.Contains(string(body), "admind") {
		t.Fatalf("tenant D users missing its own accounts: %s", body)
	}

	// Settings are per tenant: default PUT does not change tenant D behaviour.
	status, body = tenantRequest(t, http.MethodPut, ts.URL+"/api/settings/escalation", admin, "", map[string]any{
		"enabled": true, "after_seconds": 7, "severities": []string{"warn", "critical"},
		"auto_dispatch": false, "second_after_seconds": 0, "auto_patrol": false,
	})
	if status != http.StatusOK {
		t.Fatalf("default escalation put: %d %s", status, body)
	}
	_, body = tenantRequest(t, http.MethodGet, ts.URL+"/api/settings/escalation", tenantD, tenantID, nil)
	if !strings.Contains(string(body), `"after_seconds":300`) {
		t.Fatalf("tenant D escalation should keep defaults: %s", body)
	}
	_, body = tenantRequest(t, http.MethodGet, ts.URL+"/api/settings/escalation", admin, "", nil)
	if !strings.Contains(string(body), `"after_seconds":7`) {
		t.Fatalf("default escalation not persisted: %s", body)
	}

	// Audit isolation: tenant.create happened in default, not tenant D view.
	_, defaultAudit := tenantRequest(t, http.MethodGet, ts.URL+"/api/audit?limit=200", admin, "", nil)
	if !strings.Contains(string(defaultAudit), "tenant.create") {
		t.Fatalf("default audit missing tenant.create: %s", defaultAudit)
	}
	_, tenantAudit := tenantRequest(t, http.MethodGet, ts.URL+"/api/audit?limit=200", tenantD, tenantID, nil)
	if strings.Contains(string(tenantAudit), "tenant.create") {
		t.Fatalf("tenant D audit leaked default tenant audit: %s", tenantAudit)
	}

	// No-header compatibility for the default tenant (smoke path).
	status, body = tenantRequest(t, http.MethodGet, ts.URL+"/api/cameras", admin, "", nil)
	if status != http.StatusOK {
		t.Fatalf("no-header cameras: %d %s", status, body)
	}
	var cameras struct {
		Items []struct {
			Code string `json:"code"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &cameras); err != nil || len(cameras.Items) != 6 {
		t.Fatalf("default cameras per no header: %v %s", err, body)
	}
	status, body = doJSON(t, http.MethodPost, ts.URL+"/api/auth/login", map[string]string{
		"username": "admin", "password": "Admin@123456",
	})
	if status != http.StatusOK || !strings.Contains(string(body), `"tenant_id":"default"`) {
		t.Fatalf("default login: %d %s", status, body)
	}

	// Tenant request metric is exported per tenant.
	status, body = tenantRequest(t, http.MethodGet, ts.URL+"/metrics", admin, "", nil)
	if status != http.StatusOK || !strings.Contains(string(body), `dsh_twin_tenant_requests_total{tenant="default"}`) ||
		!strings.Contains(string(body), tenantID) {
		t.Fatalf("tenant request metric: %d %s", status, body)
	}
}

func TestTenantSharedModeAPI(t *testing.T) {
	t.Setenv("TENANT_SHARED", "1")
	ts, _, _, _ := newP27Server(t)
	admin := login(t, ts, "admin", "Admin@123456")
	tenantID := createTenantViaAPI(t, ts.URL, admin, "共享租户")

	// In shared mode the default admin can log into the tenant view; the token
	// is scoped to the requested tenant and sees the default data.
	status, body := doJSON(t, http.MethodPost, ts.URL+"/api/auth/login", map[string]string{
		"username": "admin", "password": "Admin@123456", "tenant_id": tenantID,
	})
	if status != http.StatusOK || !strings.Contains(string(body), `"tenant_id":"`+tenantID+`"`) {
		t.Fatalf("shared tenant login: %d %s", status, body)
	}
	var payload struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(body, &payload)
	status, body = tenantRequest(t, http.MethodGet, ts.URL+"/api/cameras", payload.Token, tenantID, nil)
	if status != http.StatusOK {
		t.Fatalf("shared tenant cameras: %d %s", status, body)
	}
	var cameras struct {
		Items []struct {
			Code string `json:"code"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &cameras); err != nil || len(cameras.Items) != 6 {
		t.Fatalf("shared mode should expose default cameras: %v %s", err, body)
	}
}
