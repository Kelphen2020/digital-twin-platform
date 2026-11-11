package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"digital-twin-platform/api/internal/auth"
	"digital-twin-platform/api/internal/bus"
	"digital-twin-platform/api/internal/store"
)

// createTenantWithOptions creates a tenant as the default admin and returns the
// response body so tests can assert `seeded`.
func createTenantWithOptions(t *testing.T, tsURL, adminToken string, body map[string]any) (int, []byte) {
	t.Helper()
	return tenantRequest(t, http.MethodPost, tsURL+"/api/tenants", adminToken, "", body)
}

func decodeTenantSeeded(t *testing.T, body []byte) (string, map[string]any) {
	t.Helper()
	var response struct {
		ID     string `json:"id"`
		Seeded struct {
			Cameras  int    `json:"cameras"`
			Areas    int    `json:"areas"`
			Rules    int    `json:"rules"`
			Settings int    `json:"settings"`
			Admin    string `json:"admin"`
		} `json:"seeded"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("tenant response: %v %s", err, body)
	}
	return response.ID, map[string]any{
		"cameras": response.Seeded.Cameras, "areas": response.Seeded.Areas,
		"rules": response.Seeded.Rules, "settings": response.Seeded.Settings,
		"admin": response.Seeded.Admin,
	}
}

func TestTenantTemplateCopyAndInitialAdmin(t *testing.T) {
	ts, _, _, _ := newP27Server(t)
	admin := login(t, ts, "admin", "Admin@123456")

	// Persist both default-tenant settings so the template copy has two.
	if status, body := tenantRequest(t, http.MethodPut, ts.URL+"/api/settings/notifications", admin, "", map[string]any{
		"enabled": false, "format": "generic", "min_severity": "warn",
		"timeout_seconds": 5, "max_attempts": 3, "backoff_seconds": 2, "silence_seconds": 30,
	}); status != http.StatusOK {
		t.Fatalf("default notifications put: %d %s", status, body)
	}
	if status, body := tenantRequest(t, http.MethodPut, ts.URL+"/api/settings/escalation", admin, "", map[string]any{
		"enabled": true, "after_seconds": 300, "severities": []string{"warn", "critical"},
		"auto_dispatch": true, "second_after_seconds": 0, "auto_patrol": false,
	}); status != http.StatusOK {
		t.Fatalf("default escalation put: %d %s", status, body)
	}

	status, body := createTenantWithOptions(t, ts.URL, admin, map[string]any{
		"name": "模板租户", "template": "default",
		"admin": map[string]any{"username": "tpladmin", "password": "TplAdmin@12345"},
	})
	if status != http.StatusCreated {
		t.Fatalf("create templated tenant: %d %s", status, body)
	}
	tenantID, seeded := decodeTenantSeeded(t, body)
	if tenantID == "" || !strings.HasPrefix(tenantID, "tnt_") {
		t.Fatalf("templated tenant id: %s", body)
	}
	want := map[string]any{"cameras": 6, "areas": 4, "rules": 6, "settings": 2, "admin": "tpladmin"}
	for key, expected := range want {
		if seeded[key] != expected {
			t.Fatalf("seeded.%s = %v, want %v (%s)", key, seeded[key], expected, body)
		}
	}
	if !strings.Contains(string(body), `"seeded"`) {
		t.Fatalf("response missing seeded: %s", body)
	}

	// The initial admin is a tenant_admin and can log into the new tenant.
	status, loginBody := doJSON(t, http.MethodPost, ts.URL+"/api/auth/login", map[string]string{
		"username": "tpladmin", "password": "TplAdmin@12345", "tenant_id": tenantID,
	})
	if status != http.StatusOK || !strings.Contains(string(loginBody), `"tenant_id":"`+tenantID+`"`) {
		t.Fatalf("templated tenant admin login: %d %s", status, loginBody)
	}
	var loginPayload struct {
		Token string `json:"token"`
		User  struct {
			Role string `json:"role"`
		} `json:"user"`
	}
	_ = json.Unmarshal(loginBody, &loginPayload)
	if loginPayload.User.Role != auth.RoleTenantAdmin {
		t.Fatalf("initial admin role = %q, want tenant_admin", loginPayload.User.Role)
	}
	status, camerasBody := tenantRequest(t, http.MethodGet, ts.URL+"/api/cameras", loginPayload.Token, tenantID, nil)
	if status != http.StatusOK || strings.Count(string(camerasBody), `"code"`) != 6 {
		t.Fatalf("templated tenant cameras: %d %s", status, camerasBody)
	}
	// Copied cameras are independent rows (no users/patrol copied).
	status, usersBody := tenantRequest(t, http.MethodGet, ts.URL+"/api/users", loginPayload.Token, tenantID, nil)
	if status != http.StatusOK || !strings.Contains(string(usersBody), "tpladmin") ||
		strings.Contains(string(usersBody), `"username":"admin"`) {
		t.Fatalf("templated tenant users: %d %s", status, usersBody)
	}
	status, routesBody := tenantRequest(t, http.MethodGet, ts.URL+"/api/patrol/routes", loginPayload.Token, tenantID, nil)
	if status != http.StatusOK || !strings.Contains(string(routesBody), `"items":[]`) {
		t.Fatalf("templated tenant should not copy patrol routes: %d %s", status, routesBody)
	}

	// Unknown template is rejected.
	status, body = createTenantWithOptions(t, ts.URL, admin, map[string]any{
		"name": "坏模板", "template": "tnt_does_not_exist",
	})
	if status != http.StatusBadRequest || !strings.Contains(string(body), "unknown template") {
		t.Fatalf("unknown template: %d %s", status, body)
	}
	// Explicit id conflict -> 409.
	status, body = createTenantWithOptions(t, ts.URL, admin, map[string]any{"id": "tnt_dup_check", "name": "重复"})
	if status != http.StatusCreated {
		t.Fatalf("seed duplicate-check tenant: %d %s", status, body)
	}
	status, body = createTenantWithOptions(t, ts.URL, admin, map[string]any{"id": "tnt_dup_check", "name": "重复"})
	if status != http.StatusConflict {
		t.Fatalf("duplicate tenant id: %d %s", status, body)
	}
	// Admin password policy is enforced for the initial admin.
	status, body = createTenantWithOptions(t, ts.URL, admin, map[string]any{
		"name": "弱密码", "admin": map[string]any{"username": "weakadmin", "password": "short"},
	})
	if status != http.StatusBadRequest || !strings.Contains(string(body), "password") {
		t.Fatalf("initial admin weak password: %d %s", status, body)
	}
}

func TestTenantAdminRoleRBAC(t *testing.T) {
	ts, _, _, _ := newP27Server(t)
	admin := login(t, ts, "admin", "Admin@123456")
	status, body := createTenantWithOptions(t, ts.URL, admin, map[string]any{
		"name": "租户管理测试", "template": "default",
		"admin": map[string]any{"username": "taadmin", "password": "TaAdmin@12345"},
	})
	if status != http.StatusCreated {
		t.Fatalf("create tenant for tenant_admin: %d %s", status, body)
	}
	tenantID, _ := decodeTenantSeeded(t, body)
	status, loginBody := doJSON(t, http.MethodPost, ts.URL+"/api/auth/login", map[string]string{
		"username": "taadmin", "password": "TaAdmin@12345", "tenant_id": tenantID,
	})
	if status != http.StatusOK {
		t.Fatalf("tenant admin login: %d %s", status, loginBody)
	}
	var loginPayload struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(loginBody, &loginPayload)
	ta := loginPayload.Token

	// Own-tenant user management + audit are allowed.
	status, usersBody := tenantRequest(t, http.MethodGet, ts.URL+"/api/users", ta, tenantID, nil)
	if status != http.StatusOK || !strings.Contains(string(usersBody), "taadmin") {
		t.Fatalf("tenant_admin list users: %d %s", status, usersBody)
	}
	status, createBody := tenantRequest(t, http.MethodPost, ts.URL+"/api/users", ta, tenantID, map[string]any{
		"username": "taviewer", "password": "TaViewer@123", "role": "viewer",
	})
	if status != http.StatusCreated {
		t.Fatalf("tenant_admin create user: %d %s", status, createBody)
	}
	var viewer struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(createBody, &viewer)
	if viewer.ID == "" {
		t.Fatalf("created user id missing: %s", createBody)
	}
	if status, body = tenantRequest(t, http.MethodPatch, ts.URL+"/api/users/"+viewer.ID, ta, tenantID, map[string]any{
		"display_name": "改名观察员",
	}); status != http.StatusOK {
		t.Fatalf("tenant_admin patch user: %d %s", status, body)
	}
	if status, body = tenantRequest(t, http.MethodGet, ts.URL+"/api/audit?limit=100", ta, tenantID, nil); status != http.StatusOK {
		t.Fatalf("tenant_admin audit: %d %s", status, body)
	}
	if status, body = tenantRequest(t, http.MethodGet, ts.URL+"/api/audit/export", ta, tenantID, nil); status != http.StatusOK {
		t.Fatalf("tenant_admin audit export: %d %s", status, body)
	}
	if status, body = tenantRequest(t, http.MethodPost, ts.URL+"/api/users/"+viewer.ID+"/logout", ta, tenantID, nil); status != http.StatusNoContent {
		t.Fatalf("tenant_admin force logout: %d %s", status, body)
	}
	if status, body = tenantRequest(t, http.MethodDelete, ts.URL+"/api/users/"+viewer.ID, ta, tenantID, nil); status != http.StatusNoContent {
		t.Fatalf("tenant_admin delete user: %d %s", status, body)
	}

	// Operator-level permissions still apply inside the tenant.
	if status, body = tenantRequest(t, http.MethodPost, ts.URL+"/api/patrol/routes", ta, tenantID, map[string]any{
		"name": "ta 巡检", "waypoints": []map[string]any{{"x": 0, "y": 0, "z": 10}, {"x": 10, "y": 0, "z": 10}},
		"camera_ids": []string{}, "interval_seconds": 2,
	}); status != http.StatusCreated {
		t.Fatalf("tenant_admin patrol write: %d %s", status, body)
	}

	// Tenant CRUD and global admin endpoints stay admin-only.
	if status, _ = tenantRequest(t, http.MethodGet, ts.URL+"/api/tenants", ta, "", nil); status != http.StatusForbidden {
		t.Fatalf("tenant_admin list tenants: %d", status)
	}
	if status, _ = tenantRequest(t, http.MethodPost, ts.URL+"/api/tenants", ta, "", map[string]any{"name": "x"}); status != http.StatusForbidden {
		t.Fatalf("tenant_admin create tenant: %d", status)
	}
	if status, _ = tenantRequest(t, http.MethodPatch, ts.URL+"/api/tenants/"+tenantID, ta, "", map[string]any{"name": "x"}); status != http.StatusForbidden {
		t.Fatalf("tenant_admin patch tenant: %d", status)
	}
	if status, _ = tenantRequest(t, http.MethodGet, ts.URL+"/api/settings/escalation", ta, tenantID, nil); status != http.StatusForbidden {
		t.Fatalf("tenant_admin settings should stay admin-only: %d", status)
	}
	// Cross-tenant data access is blocked by the token tenant.
	if status, _ = tenantRequest(t, http.MethodGet, ts.URL+"/api/users", ta, "", nil); status != http.StatusForbidden {
		t.Fatalf("tenant_admin cross-tenant read: %d", status)
	}

	// Admin can promote/demote users to tenant_admin (role validation accepts it).
	seedViewer := login(t, ts, "viewer", "Viewer@123")
	var viewerList struct {
		Items []struct {
			ID       string `json:"id"`
			Username string `json:"username"`
		} `json:"items"`
	}
	_, viewersBody := tenantRequest(t, http.MethodGet, ts.URL+"/api/users", admin, "", nil)
	_ = json.Unmarshal(viewersBody, &viewerList)
	viewerID := ""
	for _, account := range viewerList.Items {
		if account.Username == "viewer" {
			viewerID = account.ID
		}
	}
	if viewerID == "" {
		t.Fatalf("seeded viewer missing: %s", viewersBody)
	}
	if status, body = tenantRequest(t, http.MethodPatch, ts.URL+"/api/users/"+viewerID, admin, "", map[string]any{
		"role": auth.RoleTenantAdmin,
	}); status != http.StatusOK {
		t.Fatalf("promote viewer to tenant_admin: %d %s", status, body)
	}
	// A promoted tenant_admin can read the audit log of the default tenant
	// (role changes take effect on the next request; the token carries the role
	// only as a hint and the store account is authoritative).
	if status, body = tenantRequest(t, http.MethodGet, ts.URL+"/api/audit?limit=10", seedViewer, "", nil); status != http.StatusOK {
		t.Fatalf("promoted tenant_admin audit read: %d %s", status, body)
	}
	if status, body = tenantRequest(t, http.MethodPatch, ts.URL+"/api/users/"+viewerID, admin, "", map[string]any{
		"role": auth.RoleViewer,
	}); status != http.StatusOK {
		t.Fatalf("demote viewer back: %d %s", status, body)
	}
}

func TestBusReplayTenantIsolation(t *testing.T) {
	ts, server, _, authService := newP27Server(t)
	eventBus := bus.NewMemory(100)
	server.AttachBus(eventBus)

	admin := login(t, ts, "admin", "Admin@123456")
	// Template-copy cameras so tenant B can ingest events too.
	status, body := createTenantWithOptions(t, ts.URL, admin, map[string]any{
		"name": "总线租户", "template": "default",
	})
	if status != http.StatusCreated {
		t.Fatalf("create bus tenant: %d %s", status, body)
	}
	tenantID, _ := decodeTenantSeeded(t, body)

	// Tenant B needs an admin-role user to call the admin-only replay endpoint.
	if _, err := authService.CreateUser(store.WithTenant(context.Background(), tenantID), auth.CreateUserInput{
		Username: "busadmin", Password: "BusAdmin@12345", Role: auth.RoleAdmin, Active: true,
	}); err != nil {
		t.Fatalf("create tenant B admin: %v", err)
	}
	status, loginBody := doJSON(t, http.MethodPost, ts.URL+"/api/auth/login", map[string]string{
		"username": "busadmin", "password": "BusAdmin@12345", "tenant_id": tenantID,
	})
	if status != http.StatusOK {
		t.Fatalf("tenant B admin login: %d %s", status, loginBody)
	}
	var loginPayload struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(loginBody, &loginPayload)
	tenantBAdmin := loginPayload.Token

	ingest := func(tenant string, confidence float64) {
		t.Helper()
		payload := map[string]any{
			"camera_code": "CAM-001",
			"detections":  []map[string]any{{"class": "intrusion", "confidence": confidence, "bbox": []float64{0.4, 0.3, 0.6, 0.8}}},
		}
		status, body := tenantRequest(t, http.MethodPost, ts.URL+"/api/ingest/detections", "", tenant, payload)
		if status != http.StatusAccepted {
			t.Fatalf("ingest status %d: %s", status, body)
		}
	}
	ingest("", 0.995)      // default tenant event
	ingest(tenantID, 0.99) // tenant B event
	ingest("", 0.995)      // default tenant event
	ingest(tenantID, 0.99) // tenant B event

	// Status counts by tenant.
	status, statusBody := tenantRequest(t, http.MethodGet, ts.URL+"/api/bus/status", admin, "", nil)
	if status != http.StatusOK || !strings.Contains(string(statusBody), `"tenants"`) {
		t.Fatalf("bus status tenants: %d %s", status, statusBody)
	}

	replay := func(token, tenant string, allTenants bool) (int, []byte) {
		return tenantRequest(t, http.MethodPost, ts.URL+"/api/bus/replay", token, tenant, map[string]any{
			"from_offset": 0, "notify": false, "limit": 100, "all_tenants": allTenants,
		})
	}
	status, replayBody := replay(admin, "", false)
	var replayed struct {
		OK       bool  `json:"ok"`
		Replayed int   `json:"replayed"`
		Items    []struct {
			TenantID string `json:"tenant_id"`
			EventID  string `json:"event_id"`
		} `json:"items"`
		TenantID string `json:"tenant_id"`
	}
	if status != http.StatusOK {
		t.Fatalf("default replay: %d %s", status, replayBody)
	}
	_ = json.Unmarshal(replayBody, &replayed)
	if replayed.Replayed != 1 || len(replayed.Items) != 1 || replayed.Items[0].TenantID != "default" {
		t.Fatalf("default replay must only include default envelopes: %s", replayBody)
	}

	status, replayBody = replay(tenantBAdmin, tenantID, false)
	if status != http.StatusOK {
		t.Fatalf("tenant B replay: %d %s", status, replayBody)
	}
	var tenantBReplay struct {
		Replayed int `json:"replayed"`
		Items    []struct {
			TenantID string `json:"tenant_id"`
		} `json:"items"`
	}
	_ = json.Unmarshal(replayBody, &tenantBReplay)
	if tenantBReplay.Replayed != 1 || tenantBReplay.Items[0].TenantID != tenantID {
		t.Fatalf("tenant B replay leaked other tenants: %s", replayBody)
	}

	status, replayBody = replay(admin, "", true)
	if status != http.StatusOK {
		t.Fatalf("all_tenants replay: %d %s", status, replayBody)
	}
	var allReplay struct {
		Replayed int `json:"replayed"`
		Items    []struct {
			TenantID string `json:"tenant_id"`
		} `json:"items"`
	}
	_ = json.Unmarshal(replayBody, &allReplay)
	if allReplay.Replayed != 2 {
		t.Fatalf("all_tenants replay count: %s", replayBody)
	}
	seen := map[string]bool{}
	for _, item := range allReplay.Items {
		seen[item.TenantID] = true
	}
	if !seen["default"] || !seen[tenantID] {
		t.Fatalf("all_tenants replay tenant ids: %s", replayBody)
	}

	// Cross-tenant admin token is rejected before replay.
	if status, _ = replay(admin, tenantID, false); status != http.StatusForbidden {
		t.Fatalf("cross-tenant replay: %d", status)
	}
	_ = eventBus
}
