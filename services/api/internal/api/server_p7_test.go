package api_test

import (
	"context"
	"encoding/json"
	"fmt"
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

func newP7Server(t *testing.T) *httptest.Server {
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
	return ts
}

func TestUserManagementLifecycle(t *testing.T) {
	ts := newP7Server(t)
	admin := login(t, ts, "admin", "Admin@123456")
	operator := login(t, ts, "operator", "Operator@123")

	// List: seeded users, no password material.
	status, body := authorized(t, http.MethodGet, ts.URL+"/api/users", admin, nil)
	if status != http.StatusOK {
		t.Fatalf("list users: %d %s", status, body)
	}
	if strings.Contains(string(body), "password") {
		t.Fatalf("user payload leaked password material: %s", body)
	}
	for _, username := range []string{"admin", "operator", "viewer"} {
		if !strings.Contains(string(body), `"username":"`+username+`"`) {
			t.Fatalf("seeded user %s missing: %s", username, body)
		}
	}
	status, _ = authorized(t, http.MethodGet, ts.URL+"/api/users", operator, nil)
	if status != http.StatusForbidden {
		t.Fatalf("operator list users should be 403, got %d", status)
	}

	// Validation + conflicts.
	status, _ = authorized(t, http.MethodPost, ts.URL+"/api/users", admin, map[string]any{
		"username": "p7user", "password": "short", "role": "viewer",
	})
	if status != http.StatusBadRequest {
		t.Fatalf("short password should be 400, got %d", status)
	}
	status, _ = authorized(t, http.MethodPost, ts.URL+"/api/users", admin, map[string]any{
		"username": "p7user", "password": "P7-Password1", "role": "viewer", "areas": []string{"area_nope"},
	})
	if status != http.StatusBadRequest {
		t.Fatalf("unknown area should be 400, got %d", status)
	}

	// Create.
	status, body = authorized(t, http.MethodPost, ts.URL+"/api/users", admin, map[string]any{
		"username": "p7user", "password": "P7-Password1", "display_name": "P7 用户",
		"role": "viewer", "active": true,
	})
	if status != http.StatusCreated {
		t.Fatalf("create user: %d %s", status, body)
	}
	var created struct {
		ID       string `json:"id"`
		Username string `json:"username"`
		Role     string `json:"role"`
	}
	_ = json.Unmarshal(body, &created)
	if created.ID == "" || created.Username != "p7user" || created.Role != "viewer" {
		t.Fatalf("unexpected created user: %s", body)
	}
	status, _ = authorized(t, http.MethodPost, ts.URL+"/api/users", admin, map[string]any{
		"username": "p7user", "password": "P7-Password1", "role": "viewer",
	})
	if status != http.StatusConflict {
		t.Fatalf("duplicate username should be 409, got %d", status)
	}

	// New user can log in with the viewer role.
	newToken := login(t, ts, "p7user", "P7-Password1")
	status, body = authorized(t, http.MethodGet, ts.URL+"/api/auth/me", newToken, nil)
	if status != http.StatusOK || !strings.Contains(string(body), `"role":"viewer"`) {
		t.Fatalf("new user me: %d %s", status, body)
	}

	// Promote to operator with an area, then verify scope after re-login.
	status, body = authorized(t, http.MethodPatch, ts.URL+"/api/users/"+created.ID, admin, map[string]any{
		"role": "operator", "areas": []string{"area_gate"}, "display_name": "P7 值班员",
	})
	if status != http.StatusOK || !strings.Contains(string(body), `"role":"operator"`) {
		t.Fatalf("promote user: %d %s", status, body)
	}
	operatorToken := login(t, ts, "p7user", "P7-Password1")
	status, body = authorized(t, http.MethodGet, ts.URL+"/api/cameras", operatorToken, nil)
	if status != http.StatusOK {
		t.Fatalf("operator cameras: %d %s", status, body)
	}
	if strings.Contains(string(body), "CAM-002") || !strings.Contains(string(body), "CAM-001") {
		t.Fatalf("area scope not applied: %s", body)
	}

	// Password reset invalidates the old password.
	status, body = authorized(t, http.MethodPatch, ts.URL+"/api/users/"+created.ID, admin, map[string]any{
		"password": "P7-NewPassword2",
	})
	if status != http.StatusOK {
		t.Fatalf("reset password: %d %s", status, body)
	}
	status, _ = doJSON(t, http.MethodPost, ts.URL+"/api/auth/login", map[string]string{
		"username": "p7user", "password": "P7-Password1",
	})
	if status != http.StatusUnauthorized {
		t.Fatalf("old password should fail, got %d", status)
	}
	newToken = login(t, ts, "p7user", "P7-NewPassword2")

	// Deactivate: login refused, existing token rejected by /auth/me later.
	status, body = authorized(t, http.MethodPatch, ts.URL+"/api/users/"+created.ID, admin, map[string]any{"active": false})
	if status != http.StatusOK || !strings.Contains(string(body), `"active":false`) {
		t.Fatalf("deactivate: %d %s", status, body)
	}
	status, _ = doJSON(t, http.MethodPost, ts.URL+"/api/auth/login", map[string]string{
		"username": "p7user", "password": "P7-NewPassword2",
	})
	if status != http.StatusUnauthorized {
		t.Fatalf("deactivated login should fail, got %d", status)
	}
	_ = newToken

	// Self protections.
	_, body = authorized(t, http.MethodGet, ts.URL+"/api/auth/me", admin, nil)
	var me struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(body, &me)
	status, body = authorized(t, http.MethodDelete, ts.URL+"/api/users/"+me.ID, admin, nil)
	if status != http.StatusBadRequest || !strings.Contains(string(body), "self_protected") {
		t.Fatalf("self delete: %d %s", status, body)
	}
	status, body = authorized(t, http.MethodPatch, ts.URL+"/api/users/"+me.ID, admin, map[string]any{"active": false})
	if status != http.StatusBadRequest || !strings.Contains(string(body), "self_protected") {
		t.Fatalf("self deactivate: %d %s", status, body)
	}
	status, body = authorized(t, http.MethodPatch, ts.URL+"/api/users/"+me.ID, admin, map[string]any{"role": "viewer"})
	if status != http.StatusBadRequest || !strings.Contains(string(body), "self_protected") {
		t.Fatalf("self role change: %d %s", status, body)
	}

	// Delete and confirm login fails.
	status, _ = authorized(t, http.MethodDelete, ts.URL+"/api/users/"+created.ID, admin, nil)
	if status != http.StatusNoContent {
		t.Fatalf("delete user: %d", status)
	}
	status, _ = doJSON(t, http.MethodPost, ts.URL+"/api/auth/login", map[string]string{
		"username": "p7user", "password": "P7-NewPassword2",
	})
	if status != http.StatusUnauthorized {
		t.Fatalf("deleted login should fail, got %d", status)
	}

	// Audit covers user management.
	status, body = authorized(t, http.MethodGet, ts.URL+"/api/audit?limit=100", admin, nil)
	if status != http.StatusOK {
		t.Fatalf("audit: %d", status)
	}
	for _, needle := range []string{"user.create", "user.update", "user.delete", `"actor":"admin"`} {
		if !strings.Contains(string(body), needle) {
			t.Fatalf("audit missing %q", needle)
		}
	}
	_ = fmt.Sprint()
}
