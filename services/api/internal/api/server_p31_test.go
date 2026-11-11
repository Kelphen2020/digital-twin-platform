package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestTenantExport(t *testing.T) {
	ts, _ := newP30Server(t)
	admin := login(t, ts, "admin", "Admin@123456")

	// Seed one event so the export has non-empty collections.
	ingestEvent(t, ts, "CAM-001", "intrusion")

	status, body := authorized(t, http.MethodGet, ts.URL+"/api/tenants/default/export", admin, nil)
	if status != http.StatusOK {
		t.Fatalf("export: %d %s", status, body)
	}
	var payload struct {
		Tenant struct {
			ID string `json:"id"`
		} `json:"tenant"`
		ExportedAt string         `json:"exported_at"`
		Counts     map[string]int `json:"counts"`
		Data       struct {
			Cameras []struct {
				ID string `json:"id"`
			} `json:"cameras"`
			Users []struct {
				Username     string `json:"username"`
				PasswordHash string `json:"password_hash"`
			} `json:"users"`
			Events []struct {
				ID string `json:"id"`
			} `json:"events"`
			Settings map[string]json.RawMessage `json:"settings"`
			Tracks   []json.RawMessage          `json:"tracks"`
			Audit    []json.RawMessage          `json:"audit"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode export: %v", err)
	}
	if payload.Tenant.ID != "default" || payload.ExportedAt == "" {
		t.Fatalf("unexpected export envelope: %+v", payload.Tenant)
	}
	if payload.Counts["cameras"] != len(payload.Data.Cameras) || len(payload.Data.Cameras) != 6 {
		t.Fatalf("camera count mismatch: %+v (%d)", payload.Counts, len(payload.Data.Cameras))
	}
	if payload.Counts["users"] != 3 || len(payload.Data.Users) != 3 {
		t.Fatalf("user count mismatch: %+v", payload.Counts)
	}
	for _, user := range payload.Data.Users {
		if user.PasswordHash != "" {
			t.Fatalf("password hash leaked for %s", user.Username)
		}
	}
	if len(payload.Data.Events) == 0 || payload.Counts["events"] != len(payload.Data.Events) {
		t.Fatalf("event count mismatch: %+v", payload.Counts)
	}
	if strings.Contains(string(body), "password_hash") {
		t.Fatal("export payload must not contain password_hash")
	}
	if strings.Contains(string(body), "token_hash") {
		t.Fatal("export payload must not contain refresh token hashes")
	}
	if len(payload.Data.Tracks) != 0 || len(payload.Data.Audit) != 0 {
		t.Fatal("tracks/audit must be absent unless requested")
	}

	// Flags include tracks and audit.
	status, body = authorized(t, http.MethodGet, ts.URL+"/api/tenants/default/export?include_tracks=1&include_audit=1", admin, nil)
	if status != http.StatusOK {
		t.Fatalf("export with flags: %d %s", status, body)
	}
	var withFlags struct {
		Data struct {
			Audit []json.RawMessage `json:"audit"`
		} `json:"data"`
	}
	_ = json.Unmarshal(body, &withFlags)
	if len(withFlags.Data.Audit) == 0 {
		t.Fatal("include_audit=1 should add audit entries")
	}

	// Unknown tenant → 404; non-admin → 403.
	status, _ = authorized(t, http.MethodGet, ts.URL+"/api/tenants/tnt_missing/export", admin, nil)
	if status != http.StatusNotFound {
		t.Fatalf("unknown tenant export should be 404, got %d", status)
	}
	viewer := login(t, ts, "viewer", "Viewer@123")
	status, _ = authorized(t, http.MethodGet, ts.URL+"/api/tenants/default/export", viewer, nil)
	if status != http.StatusForbidden {
		t.Fatalf("viewer export should be 403, got %d", status)
	}
}

func TestTenantExportOfSecondaryTenant(t *testing.T) {
	ts, _ := newP30Server(t)
	admin := login(t, ts, "admin", "Admin@123456")
	status, body := authorized(t, http.MethodPost, ts.URL+"/api/tenants", admin, map[string]any{
		"name": "导出测试", "template": "default",
		"admin": map[string]any{"username": "exportadmin", "password": "ValidPass123"},
	})
	if status != http.StatusCreated {
		t.Fatalf("create tenant: %d %s", status, body)
	}
	var created struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(body, &created)

	status, body = authorized(t, http.MethodGet, ts.URL+"/api/tenants/"+created.ID+"/export", admin, nil)
	if status != http.StatusOK {
		t.Fatalf("export secondary tenant: %d %s", status, body)
	}
	var payload struct {
		Tenant struct {
			ID string `json:"id"`
		} `json:"tenant"`
		Counts map[string]int `json:"counts"`
	}
	_ = json.Unmarshal(body, &payload)
	if payload.Tenant.ID != created.ID || payload.Counts["cameras"] != 6 || payload.Counts["users"] != 1 {
		t.Fatalf("unexpected secondary export: %+v", payload)
	}
}
