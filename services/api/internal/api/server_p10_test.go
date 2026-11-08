package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
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

// newP10Server builds an auth-on server plus the handle for guard overrides.
func newP10Server(t *testing.T) (*httptest.Server, *api.Server) {
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
	return ts, server
}

func TestSecurityHeaders(t *testing.T) {
	ts := newP7Server(t)
	response, err := http.Get(ts.URL + "/api/health")
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	defer response.Body.Close()
	checks := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "no-referrer",
	}
	for name, want := range checks {
		if got := response.Header.Get(name); got != want {
			t.Fatalf("%s = %q, want %q", name, got, want)
		}
	}
}

func TestPasswordPolicy(t *testing.T) {
	ts := newP7Server(t)
	admin := login(t, ts, "admin", "Admin@123456")
	cases := []struct {
		password string
		want     int
	}{
		{"short123", http.StatusBadRequest},        // <10
		{"OnlyLettersLong", http.StatusBadRequest}, // no digit
		{"1234567890123", http.StatusBadRequest},   // no letter
		{"ValidPass123", http.StatusCreated},       // ok
	}
	for index, testCase := range cases {
		username := "p10policy" + strconv.Itoa(index)
		status, body := authorized(t, http.MethodPost, ts.URL+"/api/users", admin, map[string]any{
			"username": username, "password": testCase.password, "role": "viewer",
		})
		if status != testCase.want {
			t.Fatalf("password %q: got %d want %d (%s)", testCase.password, status, testCase.want, body)
		}
	}
}

func TestLoginLockout(t *testing.T) {
	ts, server := newP10Server(t)
	server.SetLoginGuard(2, 300*time.Millisecond)
	admin := login(t, ts, "admin", "Admin@123456")
	// Use a throwaway account so the admin key is not locked.
	status, body := authorized(t, http.MethodPost, ts.URL+"/api/users", admin, map[string]any{
		"username": "p10lock", "password": "LockUser123", "role": "viewer",
	})
	if status != http.StatusCreated {
		t.Fatalf("create lock user: %d %s", status, body)
	}
	for attempt := 0; attempt < 2; attempt++ {
		status, _ := doJSON(t, http.MethodPost, ts.URL+"/api/auth/login", map[string]string{
			"username": "p10lock", "password": "wrong-password",
		})
		if status != http.StatusUnauthorized {
			t.Fatalf("attempt %d should be 401, got %d", attempt+1, status)
		}
	}
	response, err := http.Post(ts.URL+"/api/auth/login", "application/json",
		strings.NewReader(`{"username":"p10lock","password":"wrong-password"}`))
	if err != nil {
		t.Fatalf("locked login: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected 429 while locked, got %d", response.StatusCode)
	}
	if response.Header.Get("Retry-After") == "" {
		t.Fatal("Retry-After header missing")
	}
	var payload map[string]string
	_ = json.NewDecoder(response.Body).Decode(&payload)
	if payload["error"] != "too_many_attempts" {
		t.Fatalf("unexpected error payload: %+v", payload)
	}
	// Admin login still works while another key is locked.
	login(t, ts, "admin", "Admin@123456")
	// Lock expires and the correct password works again.
	time.Sleep(400 * time.Millisecond)
	login(t, ts, "p10lock", "LockUser123")
}

func TestAuditExport(t *testing.T) {
	ts := newP7Server(t)
	admin := login(t, ts, "admin", "Admin@123456")
	operator := login(t, ts, "operator", "Operator@123")

	status, body := authorized(t, http.MethodGet, ts.URL+"/api/audit/export", admin, nil)
	if status != http.StatusOK {
		t.Fatalf("audit export: %d %s", status, body)
	}
	if !strings.HasPrefix(string(body), "id,at,actor,action,resource,detail") {
		t.Fatalf("unexpected csv header: %s", body)
	}
	status, _ = authorized(t, http.MethodGet, ts.URL+"/api/audit/export", operator, nil)
	if status != http.StatusForbidden {
		t.Fatalf("operator export should be 403, got %d", status)
	}
	status, _ = authorized(t, http.MethodGet, ts.URL+"/api/audit/export?from=not-a-date", admin, nil)
	if status != http.StatusBadRequest {
		t.Fatalf("bad from should be 400, got %d", status)
	}
}
