package api

import (
	"context"
	"encoding/json"
	"errors"
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
	if _, ok := s.allow(w, r, auth.PermTenantAdmin); !ok {
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

// tenantSeeded is the P29.0 template-copy summary returned by POST /api/tenants.
type tenantSeeded struct {
	Cameras  int    `json:"cameras"`
	Areas    int    `json:"areas"`
	Rules    int    `json:"rules"`
	Settings int    `json:"settings"`
	Admin    string `json:"admin"`
}

// handleCreateTenant is POST /api/tenants (admin). P29.0 adds optional
// `template` (copy cameras/areas/camera_rules/settings from that tenant) and
// optional `admin` (create the tenant's initial tenant_admin account).
func (s *Server) handleCreateTenant(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.allow(w, r, auth.PermTenantAdmin); !ok {
		return
	}
	var body struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Active   *bool  `json:"active"`
		Template string `json:"template"`
		Admin    *struct {
			Username string `json:"username"`
			Password string `json:"password"`
		} `json:"admin"`
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
	templateID := strings.TrimSpace(body.Template)
	if templateID != "" {
		if _, ok, err := s.store.GetTenant(r.Context(), templateID); err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		} else if !ok {
			writeError(w, http.StatusBadRequest, "bad_request", "unknown template "+templateID)
			return
		}
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
	seeded := tenantSeeded{}
	if templateID != "" {
		var err error
		seeded, err = s.seedTenantTemplate(r.Context(), templateID, tenant.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		s.audit(r, "tenant.template.copy", tenant.ID, map[string]any{
			"template": templateID, "cameras": seeded.Cameras, "areas": seeded.Areas,
			"rules": seeded.Rules, "settings": seeded.Settings,
		})
	}
	if body.Admin != nil && strings.TrimSpace(body.Admin.Username) != "" {
		adminUser := strings.TrimSpace(body.Admin.Username)
		adminCtx := store.WithTenant(r.Context(), tenant.ID)
		created, err := s.auth.CreateUser(adminCtx, auth.CreateUserInput{
			Username: adminUser, Password: body.Admin.Password, Role: auth.RoleTenantAdmin, Active: true,
		})
		if err != nil {
			switch {
			case errors.Is(err, auth.ErrUsernameTaken), store.IsConflict(err):
				writeError(w, http.StatusConflict, "conflict", "admin username already exists in this tenant")
			case errors.Is(err, auth.ErrBadInput):
				writeError(w, http.StatusBadRequest, "bad_request", err.Error())
			default:
				writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
			}
			return
		}
		seeded.Admin = created.Username
	}
	s.audit(r, "tenant.create", tenant.ID, map[string]any{
		"name": tenant.Name, "active": tenant.Active, "template": templateID, "seeded": seeded,
	})
	writeJSON(w, http.StatusCreated, struct {
		model.Tenant
		Seeded tenantSeeded `json:"seeded"`
	}{Tenant: tenant, Seeded: seeded})
}

// seedTenantTemplate copies cameras, areas, camera rules and settings from the
// template tenant into the newly created tenant. Ids are regenerated so the
// copy is an independent dataset (no events/orders/audit/users/patrol are
// copied, per docs/API_P29.md §1).
func (s *Server) seedTenantTemplate(ctx context.Context, templateID, newTenantID string) (tenantSeeded, error) {
	seeded := tenantSeeded{}
	templateCtx := store.WithTenant(ctx, templateID)
	targetCtx := store.WithTenant(ctx, newTenantID)

	areas, err := s.store.ListAreas(templateCtx)
	if err != nil {
		return seeded, err
	}
	areaIDs := make(map[string]string, len(areas))
	for _, area := range areas {
		clone := area
		clone.ID = newID("area")
		clone.TenantID = ""
		if createErr := s.store.CreateArea(targetCtx, clone); createErr != nil {
			return seeded, createErr
		}
		areaIDs[area.ID] = clone.ID
		seeded.Areas++
	}

	cameras, err := s.store.ListCameras(templateCtx)
	if err != nil {
		return seeded, err
	}
	cameraIDs := make(map[string]string, len(cameras))
	for _, camera := range cameras {
		clone := camera
		clone.ID = newID("cam")
		clone.TenantID = ""
		if mapped, ok := areaIDs[camera.AreaID]; ok {
			clone.AreaID = mapped
		}
		if camera.Calibration != nil {
			calibration := *camera.Calibration
			clone.Calibration = &calibration
		}
		if createErr := s.store.CreateCamera(targetCtx, clone); createErr != nil {
			return seeded, createErr
		}
		cameraIDs[camera.ID] = clone.ID
		seeded.Cameras++
	}

	for sourceID, targetID := range cameraIDs {
		rules, rulesErr := s.store.GetCameraRules(templateCtx, sourceID)
		if rulesErr != nil {
			rules = model.DefaultCameraRules(sourceID)
		}
		rules.CameraID = targetID
		rules.TenantID = ""
		if _, setErr := s.store.SetCameraRules(targetCtx, rules); setErr != nil {
			return seeded, setErr
		}
		seeded.Rules++
	}

	for _, key := range []string{settingNotifications, settingEscalation} {
		raw, ok, err := s.store.GetSetting(templateCtx, key)
		if err != nil {
			return seeded, err
		}
		if !ok || len(raw) == 0 {
			continue
		}
		if setErr := s.store.SetSetting(targetCtx, key, raw); setErr != nil {
			return seeded, setErr
		}
		seeded.Settings++
	}
	return seeded, nil
}

// handleUpdateTenant is PATCH /api/tenants/{id} (admin).
func (s *Server) handleUpdateTenant(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.allow(w, r, auth.PermTenantAdmin); !ok {
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
