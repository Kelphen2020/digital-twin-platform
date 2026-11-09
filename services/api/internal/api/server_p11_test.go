package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

type loginResult struct {
	Token            string `json:"token"`
	RefreshToken     string `json:"refresh_token"`
	RefreshExpiresAt string `json:"refresh_expires_at"`
}

func loginFull(t *testing.T, tsURL, username, password string) loginResult {
	t.Helper()
	status, body := doJSON(t, http.MethodPost, tsURL+"/api/auth/login", map[string]string{
		"username": username, "password": password,
	})
	if status != http.StatusOK {
		t.Fatalf("login %s: %d %s", username, status, body)
	}
	var result loginResult
	if err := json.Unmarshal(body, &result); err != nil || result.Token == "" || result.RefreshToken == "" {
		t.Fatalf("login payload missing tokens: %s", body)
	}
	return result
}

func refreshAccess(t *testing.T, tsURL, refreshToken string) loginResult {
	t.Helper()
	status, body := doJSON(t, http.MethodPost, tsURL+"/api/auth/refresh", map[string]string{
		"refresh_token": refreshToken,
	})
	if status != http.StatusOK {
		t.Fatalf("refresh: %d %s", status, body)
	}
	var result loginResult
	if err := json.Unmarshal(body, &result); err != nil || result.Token == "" || result.RefreshToken == "" {
		t.Fatalf("refresh payload missing tokens: %s", body)
	}
	return result
}

func TestRefreshTokenRotation(t *testing.T) {
	ts := newP7Server(t)
	session := loginFull(t, ts.URL, "admin", "Admin@123456")
	if session.RefreshExpiresAt == "" {
		t.Fatal("refresh_expires_at missing")
	}
	rotated := refreshAccess(t, ts.URL, session.RefreshToken)
	if rotated.RefreshToken == session.RefreshToken {
		t.Fatal("refresh token was not rotated")
	}
	// Reusing the old refresh token must fail.
	status, body := doJSON(t, http.MethodPost, ts.URL+"/api/auth/refresh", map[string]string{
		"refresh_token": session.RefreshToken,
	})
	if status != http.StatusUnauthorized || !strings.Contains(string(body), "invalid_refresh_token") {
		t.Fatalf("old refresh should be 401, got %d %s", status, body)
	}
	// The new access token works.
	status, _ = authorized(t, http.MethodGet, ts.URL+"/api/cameras", rotated.Token, nil)
	if status != http.StatusOK {
		t.Fatalf("refreshed access token rejected: %d", status)
	}
}

func TestLogoutRevokesRefreshAndLogoutAllKillsAccess(t *testing.T) {
	ts := newP7Server(t)
	session := loginFull(t, ts.URL, "viewer", "Viewer@123")
	status, _ := authorized(t, http.MethodPost, ts.URL+"/api/auth/logout", session.Token,
		map[string]any{"refresh_token": session.RefreshToken})
	if status != http.StatusNoContent {
		t.Fatalf("logout: %d", status)
	}
	status, _ = doJSON(t, http.MethodPost, ts.URL+"/api/auth/refresh", map[string]string{
		"refresh_token": session.RefreshToken,
	})
	if status != http.StatusUnauthorized {
		t.Fatalf("logged-out refresh should be 401, got %d", status)
	}

	second := loginFull(t, ts.URL, "viewer", "Viewer@123")
	status, _ = authorized(t, http.MethodPost, ts.URL+"/api/auth/logout-all", second.Token, nil)
	if status != http.StatusNoContent {
		t.Fatalf("logout-all: %d", status)
	}
	status, _ = authorized(t, http.MethodGet, ts.URL+"/api/cameras", second.Token, nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("access token should be invalid after logout-all, got %d", status)
	}
	// Re-login works and the old refresh token is dead.
	third := loginFull(t, ts.URL, "viewer", "Viewer@123")
	status, _ = doJSON(t, http.MethodPost, ts.URL+"/api/auth/refresh", map[string]string{
		"refresh_token": second.RefreshToken,
	})
	if status != http.StatusUnauthorized {
		t.Fatalf("revoked refresh should be 401, got %d", status)
	}
	_ = third
}

func TestPasswordChangeInvalidatesSessions(t *testing.T) {
	ts := newP7Server(t)
	admin := login(t, ts, "admin", "Admin@123456")
	status, body := authorized(t, http.MethodPost, ts.URL+"/api/users", admin, map[string]any{
		"username": "p11pass", "password": "Password123", "role": "viewer",
	})
	if status != http.StatusCreated {
		t.Fatalf("create user: %d %s", status, body)
	}
	var created struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(body, &created)
	session := loginFull(t, ts.URL, "p11pass", "Password123")

	status, body = authorized(t, http.MethodPatch, ts.URL+"/api/users/"+created.ID, admin, map[string]any{
		"password": "Password456",
	})
	if status != http.StatusOK {
		t.Fatalf("change password: %d %s", status, body)
	}
	status, _ = authorized(t, http.MethodGet, ts.URL+"/api/cameras", session.Token, nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("old access token should be invalid after password change, got %d", status)
	}
	status, _ = doJSON(t, http.MethodPost, ts.URL+"/api/auth/login", map[string]string{
		"username": "p11pass", "password": "Password123",
	})
	if status != http.StatusUnauthorized {
		t.Fatalf("old password should fail, got %d", status)
	}
	login(t, ts, "p11pass", "Password456")
}

func TestAdminForceLogoutAndSessions(t *testing.T) {
	ts := newP7Server(t)
	admin := login(t, ts, "admin", "Admin@123456")
	operator := login(t, ts, "operator", "Operator@123")

	status, body := authorized(t, http.MethodPost, ts.URL+"/api/users", admin, map[string]any{
		"username": "p11kick", "password": "KickUser123", "role": "viewer",
	})
	if status != http.StatusCreated {
		t.Fatalf("create user: %d %s", status, body)
	}
	var created struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(body, &created)
	session := loginFull(t, ts.URL, "p11kick", "KickUser123")

	status, body = authorized(t, http.MethodGet, ts.URL+"/api/users/"+created.ID+"/sessions", admin, nil)
	if status != http.StatusOK || !strings.Contains(string(body), "expires_at") {
		t.Fatalf("sessions: %d %s", status, body)
	}
	if strings.Contains(string(body), "token_hash") {
		t.Fatal("sessions response leaks token hashes")
	}

	status, _ = authorized(t, http.MethodPost, ts.URL+"/api/users/"+created.ID+"/logout", operator, nil)
	if status != http.StatusForbidden {
		t.Fatalf("operator force logout should be 403, got %d", status)
	}
	status, _ = authorized(t, http.MethodPost, ts.URL+"/api/users/"+created.ID+"/logout", admin, nil)
	if status != http.StatusNoContent {
		t.Fatalf("force logout: %d", status)
	}
	status, _ = authorized(t, http.MethodGet, ts.URL+"/api/cameras", session.Token, nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("kicked user's access should be 401, got %d", status)
	}
	status, body = authorized(t, http.MethodGet, ts.URL+"/api/users/"+created.ID+"/sessions", admin, nil)
	if status != http.StatusOK || strings.Contains(string(body), "rft_") {
		t.Fatalf("expected no live sessions after kick: %s", body)
	}
	// Self protection.
	status, body = authorized(t, http.MethodPost, ts.URL+"/api/users/usr_admin/logout", admin, nil)
	if status != http.StatusBadRequest || !strings.Contains(string(body), "self_protected") {
		t.Fatalf("self force logout: %d %s", status, body)
	}
	_ = time.Now()
}
