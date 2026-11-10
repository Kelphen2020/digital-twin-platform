package api

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"time"

	"digital-twin-platform/api/internal/auth"
	"digital-twin-platform/api/internal/model"
	"digital-twin-platform/api/internal/store"
)

// resolveTenant validates the tenant id carried by a request (header or login
// body) and returns a context scoped to it. It writes the P27.0 error
// responses (400 unknown_tenant / 403 tenant_inactive) on failure.
func (s *Server) resolveTenant(w http.ResponseWriter, r *http.Request, requested string) (context.Context, string, bool) {
	tenantID := strings.TrimSpace(requested)
	if tenantID == "" {
		tenantID = model.DefaultTenantID
	}
	tenant, ok, err := s.store.GetTenant(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return nil, "", false
	}
	if !ok {
		writeError(w, http.StatusBadRequest, "unknown_tenant", "unknown tenant "+tenantID)
		return nil, "", false
	}
	if !tenant.Active {
		writeError(w, http.StatusForbidden, "tenant_inactive", "tenant "+tenantID+" is inactive")
		return nil, "", false
	}
	return store.WithTenant(r.Context(), tenant.ID), tenant.ID, true
}

// requestTenant returns the tenant id from the X-Tenant-ID header (default).
func requestTenant(r *http.Request) string {
	if tenantID := strings.TrimSpace(r.Header.Get("X-Tenant-ID")); tenantID != "" {
		return tenantID
	}
	return model.DefaultTenantID
}

// handleListTenants is GET /api/tenants (admin).
func (s *Server) handleListTenants(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.allow(w, r, auth.PermAdmin); !ok {
		return
	}
	tenants, err := s.store.ListTenants(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	if tenants == nil {
		tenants = []model.Tenant{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": tenants})
}

// handleCreateTenant is POST /api/tenants (admin).
func (s *Server) handleCreateTenant(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.allow(w, r, auth.PermAdmin); !ok {
		return
	}
	var body struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Active *bool  `json:"active"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "name is required")
		return
	}
	if len([]rune(name)) > 64 {
		writeError(w, http.StatusBadRequest, "bad_request", "name must be at most 64 characters")
		return
	}
	tenantID := strings.TrimSpace(body.ID)
	if tenantID == "" {
		tenantID = newID("tnt")
	} else if !tenantIDPattern.MatchString(tenantID) {
		writeError(w, http.StatusBadRequest, "bad_request", "id must be 3-64 characters (letters, digits, _ . -)")
		return
	}
	active := true
	if body.Active != nil {
		active = *body.Active
	}
	now := time.Now().UTC()
	tenant := model.Tenant{ID: tenantID, Name: name, Active: active, CreatedAt: now, UpdatedAt: now}
	if err := s.store.CreateTenant(r.Context(), tenant); err != nil {
		if store.IsConflict(err) {
			writeError(w, http.StatusConflict, "conflict", "tenant already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	s.audit(r, "tenant.create", tenant.ID, map[string]any{"name": tenant.Name, "active": tenant.Active})
	writeJSON(w, http.StatusCreated, tenant)
}

// handleUpdateTenant is PATCH /api/tenants/{id} (admin).
func (s *Server) handleUpdateTenant(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.allow(w, r, auth.PermAdmin); !ok {
		return
	}
	id := r.PathValue("id")
	current, ok, err := s.store.GetTenant(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "tenant not found")
		return
	}
	var body struct {
		Name   *string `json:"name"`
		Active *bool   `json:"active"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if body.Name != nil {
		name := strings.TrimSpace(*body.Name)
		if name == "" {
			writeError(w, http.StatusBadRequest, "bad_request", "name must not be empty")
			return
		}
		if len([]rune(name)) > 64 {
			writeError(w, http.StatusBadRequest, "bad_request", "name must be at most 64 characters")
			return
		}
		current.Name = name
	}
	if body.Active != nil {
		current.Active = *body.Active
	}
	current.UpdatedAt = time.Now().UTC()
	if err := s.store.UpdateTenant(r.Context(), current); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	s.audit(r, "tenant.update", current.ID, map[string]any{"name": current.Name, "active": current.Active})
	writeJSON(w, http.StatusOK, current)
}

// loadNotificationSettings reads the caller tenant's notification settings.
func (s *Server) loadNotificationSettings(ctx context.Context) model.NotificationSettings {
	notify := model.DefaultNotificationSettings()
	if raw, ok, err := s.store.GetSetting(ctx, settingNotifications); err == nil && ok && len(raw) > 0 {
		_ = json.Unmarshal(raw, &notify)
	}
	return notify
}

// loadEscalationSettings reads the caller tenant's escalation settings.
func (s *Server) loadEscalationSettings(ctx context.Context) model.EscalationSettings {
	escalation := model.DefaultEscalationSettings()
	if raw, ok, err := s.store.GetSetting(ctx, settingEscalation); err == nil && ok && len(raw) > 0 {
		_ = json.Unmarshal(raw, &escalation)
	}
	return escalation
}

// tenantName returns the resolved tenant id for the request context.
func tenantName(ctx context.Context) string { return store.TenantFrom(ctx) }

var tenantIDPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{3,64}$`)
