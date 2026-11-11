package api

import (
	"encoding/json"
	"net/http"
	"time"

	"digital-twin-platform/api/internal/auth"
	"digital-twin-platform/api/internal/model"
	"digital-twin-platform/api/internal/store"
)

// tenantExport is the P31.0 data package returned by GET /api/tenants/{id}/export.
type tenantExport struct {
	Tenant     model.Tenant     `json:"tenant"`
	ExportedAt time.Time        `json:"exported_at"`
	Counts     map[string]int   `json:"counts"`
	Data       tenantExportData `json:"data"`
}

// tenantExportData carries the tenant's records without secrets.
type tenantExportData struct {
	Areas            []model.Area               `json:"areas"`
	Cameras          []model.Camera             `json:"cameras"`
	CameraRules      []model.CameraRules        `json:"camera_rules"`
	Settings         map[string]json.RawMessage `json:"settings"`
	Users            []model.UserAccount        `json:"users"`
	Events           []model.Event              `json:"events"`
	Orders           []model.Order              `json:"orders"`
	NotificationLog  []model.NotificationLog    `json:"notification_log"`
	PatrolRoutes     []model.PatrolRoute        `json:"patrol_routes"`
	PatrolSchedules  []model.PatrolSchedule     `json:"patrol_schedules"`
	PatrolExecutions []model.PatrolExecution    `json:"patrol_executions"`
	Tracks           []model.TrackPoint         `json:"tracks,omitempty"`
	Audit            []model.AuditEntry         `json:"audit,omitempty"`
}

const exportTrackLimit = 1000
const exportAuditLimit = 1000

// handleExportTenant is GET /api/tenants/{id}/export (admin, P31.0).
func (s *Server) handleExportTenant(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.allow(w, r, auth.PermTenantAdmin); !ok {
		return
	}
	tenant, ok := s.loadTenant(w, r)
	if !ok {
		return
	}
	ctx := store.WithTenant(r.Context(), tenant.ID)
	query := r.URL.Query()
	includeTracks := query.Get("include_tracks") == "1" || query.Get("include_tracks") == "true"
	includeAudit := query.Get("include_audit") == "1" || query.Get("include_audit") == "true"

	data := tenantExportData{
		Areas:    []model.Area{},
		Cameras:  []model.Camera{},
		Settings: map[string]json.RawMessage{},
		Users:    []model.UserAccount{},
		Events:   []model.Event{},
		Orders:   []model.Order{},
	}

	if areas, err := s.store.ListAreas(ctx); err == nil && areas != nil {
		data.Areas = areas
	}
	cameras, err := s.store.ListCameras(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	if cameras != nil {
		data.Cameras = cameras
	}
	for _, camera := range data.Cameras {
		rules, rulesErr := s.store.GetCameraRules(ctx, camera.ID)
		if rulesErr != nil {
			continue
		}
		data.CameraRules = append(data.CameraRules, rules)
	}

	for _, key := range []string{settingNotifications, settingEscalation} {
		if raw, ok, err := s.store.GetSetting(ctx, key); err == nil && ok && len(raw) > 0 {
			data.Settings[key] = json.RawMessage(raw)
		}
	}

	// Users are exported without password hashes.
	if accounts, err := s.store.ListUserAccounts(ctx); err == nil {
		for _, account := range accounts {
			account.PasswordHash = ""
			data.Users = append(data.Users, account)
		}
	}

	if events, err := s.store.ListEvents(ctx, model.EventFilter{Limit: 10000}); err == nil && events != nil {
		data.Events = events
	}
	if orders, err := s.store.ListOrders(ctx, model.OrderFilter{Limit: 10000}); err == nil && orders != nil {
		data.Orders = orders
	}
	if logs, err := s.store.ListNotificationLog(ctx, 1000); err == nil && logs != nil {
		data.NotificationLog = logs
	}
	if routes, err := s.store.ListPatrolRoutes(ctx); err == nil && routes != nil {
		data.PatrolRoutes = routes
	}
	if schedules, err := s.store.ListPatrolSchedules(ctx); err == nil && schedules != nil {
		data.PatrolSchedules = schedules
	}
	if executions, err := s.store.ListPatrolExecutions(ctx, model.PatrolExecutionFilter{Limit: 10000}); err == nil && executions != nil {
		data.PatrolExecutions = executions
	}
	if includeTracks {
		if tracks, err := s.store.ListTrackPoints(ctx, model.TrackFilter{Limit: exportTrackLimit}); err == nil && tracks != nil {
			data.Tracks = tracks
		}
	}
	if includeAudit {
		if entries, err := s.store.ListAudit(ctx, model.AuditFilter{Limit: exportAuditLimit}); err == nil && entries != nil {
			data.Audit = entries
		}
	}

	counts := map[string]int{
		"areas":             len(data.Areas),
		"cameras":           len(data.Cameras),
		"camera_rules":      len(data.CameraRules),
		"settings":          len(data.Settings),
		"users":             len(data.Users),
		"events":            len(data.Events),
		"orders":            len(data.Orders),
		"notification_log":  len(data.NotificationLog),
		"patrol_routes":     len(data.PatrolRoutes),
		"patrol_schedules":  len(data.PatrolSchedules),
		"patrol_executions": len(data.PatrolExecutions),
		"tracks":            len(data.Tracks),
		"audit":             len(data.Audit),
	}
	payload := tenantExport{
		Tenant: tenant, ExportedAt: time.Now().UTC(), Counts: counts, Data: data,
	}
	s.audit(r, "tenant.export", tenant.ID, map[string]any{
		"include_tracks": includeTracks, "include_audit": includeAudit, "counts": counts,
	})
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="tenant-`+tenant.ID+`.json"`)
	writeJSON(w, http.StatusOK, payload)
}
