// Package api exposes the HTTP and WebSocket surface described in docs/API.md
// and docs/API_P1.md.
package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"digital-twin-platform/api/internal/auth"
	"digital-twin-platform/api/internal/calib"
	"digital-twin-platform/api/internal/metrics"
	"digital-twin-platform/api/internal/model"
	"digital-twin-platform/api/internal/realtime"
	"digital-twin-platform/api/internal/rules"
	"digital-twin-platform/api/internal/store"
)

// Server wires the store, rule engine and WebSocket hub into HTTP handlers.
type Server struct {
	store       store.Store
	rules       *rules.Engine
	hub         *realtime.Hub
	metrics     *metrics.Metrics
	version     string
	auth        *auth.Service
	ingestToken string
	started     time.Time

	settingsMu    sync.RWMutex
	notify        model.NotificationSettings
	escalation    model.EscalationSettings
	notifyMu      sync.Mutex
	notifySilence map[string]time.Time
}

type userContextKey struct{}

// New builds the API server. A nil auth service means authentication is off.
func New(database store.Store, engine *rules.Engine, hub *realtime.Hub, meters *metrics.Metrics, version string, authService *auth.Service, ingestToken string) *Server {
	if authService == nil {
		authService = auth.New("off", "")
	}
	return &Server{
		store:         database,
		rules:         engine,
		hub:           hub,
		metrics:       meters,
		version:       version,
		auth:          authService,
		ingestToken:   ingestToken,
		started:       time.Now().UTC(),
		notify:        model.DefaultNotificationSettings(),
		escalation:    model.DefaultEscalationSettings(),
		notifySilence: make(map[string]time.Time),
	}
}

// Handler returns the router with CORS and the optional token guard.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	mux.HandleFunc("GET /api/auth/me", s.handleMe)
	mux.HandleFunc("GET /api/areas", s.handleAreas)
	mux.HandleFunc("GET /api/cameras", s.handleCameras)
	mux.HandleFunc("GET /api/cameras/{id}", s.handleCamera)
	mux.HandleFunc("PATCH /api/cameras/{id}", s.handlePatchCamera)
	mux.HandleFunc("GET /api/cameras/{id}/rules", s.handleGetRules)
	mux.HandleFunc("PUT /api/cameras/{id}/rules", s.handlePutRules)
	mux.HandleFunc("PUT /api/cameras/{id}/calibration", s.handlePutCalibration)
	mux.HandleFunc("DELETE /api/cameras/{id}/calibration", s.handleDeleteCalibration)
	mux.HandleFunc("POST /api/calibration/solve", s.handleSolveCalibration)
	mux.HandleFunc("GET /api/events", s.handleEvents)
	mux.HandleFunc("GET /api/events/{id}", s.handleGetEvent)
	mux.HandleFunc("POST /api/events/{id}/ack", s.handleAckEvent)
	mux.HandleFunc("POST /api/events/{id}/close", s.handleCloseEvent)
	mux.HandleFunc("POST /api/events/{id}/dispatch", s.handleDispatchEvent)
	mux.HandleFunc("GET /api/tracks", s.handleTracks)
	mux.HandleFunc("GET /api/settings/notifications", s.handleGetNotificationSettings)
	mux.HandleFunc("PUT /api/settings/notifications", s.handlePutNotificationSettings)
	mux.HandleFunc("GET /api/settings/escalation", s.handleGetEscalationSettings)
	mux.HandleFunc("PUT /api/settings/escalation", s.handlePutEscalationSettings)
	mux.HandleFunc("GET /api/notifications", s.handleListNotifications)
	mux.HandleFunc("GET /api/users", s.handleListUsers)
	mux.HandleFunc("GET /api/duty/groups", s.handleListDutyGroups)
	mux.HandleFunc("POST /api/duty/groups", s.handleCreateDutyGroup)
	mux.HandleFunc("PATCH /api/duty/groups/{id}", s.handleUpdateDutyGroup)
	mux.HandleFunc("DELETE /api/duty/groups/{id}", s.handleDeleteDutyGroup)
	mux.HandleFunc("GET /api/duty/current", s.handleCurrentDuty)
	mux.HandleFunc("POST /api/orders", s.handleCreateOrder)
	mux.HandleFunc("GET /api/orders", s.handleListOrders)
	mux.HandleFunc("GET /api/orders/{id}", s.handleGetOrder)
	mux.HandleFunc("PATCH /api/orders/{id}", s.handlePatchOrder)
	mux.HandleFunc("GET /api/audit", s.handleAudit)
	mux.HandleFunc("GET /api/stats/summary", s.handleStatsSummary)
	mux.HandleFunc("GET /api/stats/events.csv", s.handleStatsCSV)
	mux.HandleFunc("POST /api/ingest/detections", s.handleIngest)
	mux.HandleFunc("GET /metrics", s.handleMetrics)
	mux.HandleFunc("GET /ws", s.handleWS)
	s.hub.OnConnect = func() realtime.Frame {
		cameras, err := s.store.ListCameras(context.Background())
		count := 0
		if err == nil {
			count = len(cameras)
		}
		return realtime.Frame{Type: "hello", Data: map[string]any{
			"server_time": time.Now().UTC().Format(time.RFC3339),
			"cameras":     count,
		}}
	}
	return cors(s.authenticate(mux))
}

// RunStats broadcasts the stats frame every interval until ctx is cancelled.
func (s *Server) RunStats(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.broadcastStats()
		}
	}
}

func (s *Server) broadcastStats() {
	counts, err := s.store.Counts(context.Background())
	if err != nil {
		log.Printf("api: stats counts: %v", err)
		return
	}
	s.hub.Broadcast(realtime.Frame{Type: "stats", Data: map[string]any{
		"events_total":   counts.EventsTotal,
		"online_cameras": counts.OnlineCameras,
		"ws_clients":     s.hub.Clients(),
	}})
}

// authenticate resolves the caller: JWT for users, an optional shared header
// for the machine ingest endpoint, and an implicit admin when auth is off.
func (s *Server) authenticate(next http.Handler) http.Handler {
	if !s.auth.Enabled() {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userContextKey{}, auth.DevUser())))
		})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch path {
		case "/api/health", "/metrics", "/api/auth/login":
			next.ServeHTTP(w, r)
			return
		case "/api/ingest/detections":
			if s.ingestToken != "" {
				presented := r.Header.Get("X-Ingest-Token")
				if subtle.ConstantTimeCompare([]byte(presented), []byte(s.ingestToken)) != 1 {
					writeError(w, http.StatusUnauthorized, "ingest_unauthorized", "a valid X-Ingest-Token is required")
					return
				}
			}
			next.ServeHTTP(w, r)
			return
		}
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if path == "/ws" {
			token = r.URL.Query().Get("token")
		}
		user, err := s.auth.Verify(token)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized", "a valid bearer token is required")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userContextKey{}, user)))
	})
}

// userOf returns the authenticated principal, or the dev admin when auth is off.
func userOf(r *http.Request) auth.User {
	if user, ok := r.Context().Value(userContextKey{}).(auth.User); ok {
		return user
	}
	return auth.DevUser()
}

// allow enforces one role permission; auth-off mode always passes.
func (s *Server) allow(w http.ResponseWriter, r *http.Request, permission string) (auth.User, bool) {
	user := userOf(r)
	if !s.auth.Enabled() {
		return user, true
	}
	if !auth.Can(user.Role, permission) {
		writeError(w, http.StatusForbidden, "forbidden", "this role lacks the "+permission+" permission")
		return user, false
	}
	return user, true
}

// allowCamera enforces a permission plus the user's area scope for a camera.
func (s *Server) allowCamera(w http.ResponseWriter, r *http.Request, permission, cameraID string) bool {
	user, ok := s.allow(w, r, permission)
	if !ok {
		return false
	}
	if !s.auth.Enabled() {
		return true
	}
	camera, err := s.store.GetCamera(r.Context(), cameraID)
	if err != nil {
		s.writeCameraError(w, err)
		return false
	}
	if !auth.InScope(user, camera.AreaID) {
		writeError(w, http.StatusForbidden, "out_of_scope", "camera is outside your assigned areas")
		return false
	}
	return true
}

// allowEventCamera enforces a permission plus the area scope of an event's camera.
func (s *Server) allowEventCamera(w http.ResponseWriter, r *http.Request, permission, eventID string) (model.Event, bool) {
	user, ok := s.allow(w, r, permission)
	if !ok {
		return model.Event{}, false
	}
	event, err := s.store.GetEvent(r.Context(), eventID)
	if err != nil {
		s.writeEventError(w, err)
		return model.Event{}, false
	}
	if s.auth.Enabled() {
		camera, err := s.store.GetCamera(r.Context(), event.CameraID)
		if err != nil {
			s.writeCameraError(w, err)
			return model.Event{}, false
		}
		if !auth.InScope(user, camera.AreaID) {
			writeError(w, http.StatusForbidden, "out_of_scope", "event camera is outside your assigned areas")
			return model.Event{}, false
		}
	}
	return event, true
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	token, expires, user, err := s.auth.Login(strings.TrimSpace(request.Username), request.Password)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "invalid username or password")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token":      token,
		"expires_at": expires.Format(time.RFC3339),
		"user":       user,
	})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, userOf(r))
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	s.hub.ServeHTTP(w, r)
}

const (
	settingNotifications = "notifications"
	settingEscalation    = "escalation"
)

// LoadSettings hydrates the cached notification/escalation settings.
func (s *Server) LoadSettings(ctx context.Context) {
	notify := model.DefaultNotificationSettings()
	if raw, ok, err := s.store.GetSetting(ctx, settingNotifications); err == nil && ok && len(raw) > 0 {
		_ = json.Unmarshal(raw, &notify)
	}
	escalation := model.DefaultEscalationSettings()
	if raw, ok, err := s.store.GetSetting(ctx, settingEscalation); err == nil && ok && len(raw) > 0 {
		_ = json.Unmarshal(raw, &escalation)
	}
	s.settingsMu.Lock()
	s.notify = notify
	s.escalation = escalation
	s.settingsMu.Unlock()
}

func (s *Server) currentNotify() model.NotificationSettings {
	s.settingsMu.RLock()
	defer s.settingsMu.RUnlock()
	return s.notify
}

func (s *Server) currentEscalation() model.EscalationSettings {
	s.settingsMu.RLock()
	defer s.settingsMu.RUnlock()
	return s.escalation
}

// RunEscalation periodically marks stale open alerts as escalated.
func (s *Server) RunEscalation(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.escalateOnce(ctx)
		}
	}
}

func (s *Server) escalateOnce(ctx context.Context) {
	cfg := s.currentEscalation()
	if !cfg.Enabled || len(cfg.Severities) == 0 {
		return
	}
	before := time.Now().UTC().Add(-time.Duration(cfg.AfterSeconds) * time.Second)
	events, err := s.store.EscalateStale(ctx, before, cfg.Severities)
	if err != nil {
		log.Printf("api: escalate: %v", err)
		return
	}
	for _, event := range events {
		s.metrics.Escalations.Add(1)
		s.hub.Broadcast(realtime.Frame{Type: "event_updated", Data: event})
		_ = s.store.AddAudit(ctx, model.AuditEntry{
			ID: newID("aud"), At: time.Now().UTC(), Actor: "system",
			Action: "event.escalate", Resource: event.ID,
			Detail: map[string]any{"severity": event.Severity, "after_seconds": cfg.AfterSeconds},
		})
		go s.deliverNotification(context.Background(), "escalation", event, nil)
		if cfg.AutoDispatch {
			s.autoDispatch(ctx, event)
		}
	}
}

// deliverNotification posts one event/escalation/dispatch to the configured
// webhook, with a silence window and bounded exponential-backoff retries.
func (s *Server) deliverNotification(ctx context.Context, kind string, event model.Event, order *model.Order) {
	cfg := s.currentNotify()
	if !cfg.Enabled || strings.TrimSpace(cfg.WebhookURL) == "" {
		return
	}
	if model.SeverityRank(event.Severity) < model.SeverityRank(cfg.MinSeverity) {
		return
	}
	silenceKey := event.CameraID + "|" + event.Type + "|" + event.Severity
	s.notifyMu.Lock()
	last, seen := s.notifySilence[silenceKey]
	suppressed := cfg.SilenceSeconds > 0 && seen && time.Since(last) < time.Duration(cfg.SilenceSeconds)*time.Second
	if !suppressed {
		s.notifySilence[silenceKey] = time.Now()
	}
	s.notifyMu.Unlock()
	if suppressed {
		s.metrics.NotificationsSuppressed.Add(1)
		return
	}

	var payload []byte
	if cfg.Format == "feishu" {
		label := map[string]string{"event": "告警", "escalation": "告警升级", "dispatch": "自动派单"}[kind]
		text := fmt.Sprintf("[%s] %s · %s %s（置信度 %.2f）\n事件 %s",
			label, event.Severity, event.CameraCode, event.CameraName, event.Confidence, event.ID)
		if order != nil {
			text += fmt.Sprintf("\n工单 %s → %s", order.ID, order.Assignee)
		}
		payload, _ = json.Marshal(map[string]any{"msg_type": "text", "content": map[string]string{"text": text}})
	} else {
		body := map[string]any{"kind": kind, "event": event, "source": "digital-twin-platform"}
		if order != nil {
			body["order"] = order
		}
		payload, _ = json.Marshal(body)
	}

	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	maxAttempts := cfg.MaxAttempts
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	if maxAttempts > 5 {
		maxAttempts = 5
	}
	entry := model.NotificationLog{
		ID: newID("ntf"), At: time.Now().UTC(), Kind: kind, EventID: event.ID,
		URL: cfg.WebhookURL, Format: cfg.Format,
	}
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		entry.Attempts = attempt
		entry.Error = ""
		entry.OK = false
		entry.StatusCode = 0
		requestCtx, cancel := context.WithTimeout(ctx, timeout)
		request, buildErr := http.NewRequestWithContext(requestCtx, http.MethodPost, cfg.WebhookURL, strings.NewReader(string(payload)))
		if buildErr != nil {
			entry.Error = buildErr.Error()
		} else {
			request.Header.Set("Content-Type", "application/json")
			response, err := (&http.Client{Timeout: timeout}).Do(request)
			if err != nil {
				entry.Error = err.Error()
			} else {
				entry.StatusCode = response.StatusCode
				entry.OK = response.StatusCode >= 200 && response.StatusCode < 300
				if !entry.OK {
					entry.Error = fmt.Sprintf("webhook returned HTTP %d", response.StatusCode)
				}
				_ = response.Body.Close()
			}
		}
		cancel()
		if entry.OK {
			break
		}
		if attempt < maxAttempts {
			backoff := time.Duration(cfg.BackoffSeconds) * time.Second
			if backoff > 0 {
				select {
				case <-ctx.Done():
				case <-time.After(backoff * time.Duration(1<<(attempt-1))):
				}
			}
		}
	}
	if entry.OK {
		s.metrics.NotificationsOK.Add(1)
	} else {
		s.metrics.NotificationsFailed.Add(1)
	}
	if err := s.store.CreateNotificationLog(context.Background(), entry); err != nil {
		log.Printf("api: notification log: %v", err)
	}
}

// resolveDutyGroup finds the active group for an area, then the global fallback.
func (s *Server) resolveDutyGroup(ctx context.Context, areaID string) *model.DutyGroup {
	groups, err := s.store.ListDutyGroups(ctx)
	if err != nil {
		return nil
	}
	var global *model.DutyGroup
	for i := range groups {
		group := &groups[i]
		if !group.Active {
			continue
		}
		if len(group.Areas) == 0 {
			if global == nil {
				global = group
			}
			continue
		}
		for _, area := range group.Areas {
			if area == areaID {
				return group
			}
		}
	}
	return global
}

// autoDispatch creates an urgent work order for the area's on-duty group.
func (s *Server) autoDispatch(ctx context.Context, event model.Event) {
	camera, err := s.store.GetCamera(ctx, event.CameraID)
	if err != nil {
		return
	}
	group := s.resolveDutyGroup(ctx, camera.AreaID)
	if group == nil {
		s.metrics.DutySkipped.Add(1)
		_ = s.store.AddAudit(ctx, model.AuditEntry{
			ID: newID("aud"), At: time.Now().UTC(), Actor: "system",
			Action: "order.auto_dispatch_skipped", Resource: event.ID,
			Detail: map[string]any{"area_id": camera.AreaID},
		})
		return
	}
	now := time.Now().UTC()
	order := model.Order{
		ID: newID("ord"), EventID: event.ID, CameraID: event.CameraID,
		Title:     "告警升级 · " + event.Type + " · " + event.CameraCode + " " + event.CameraName,
		Status:    "open",
		Priority:  "urgent",
		Assignee:  group.Name,
		Note:      "系统自动派单（告警升级）",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.store.CreateOrder(ctx, order); err != nil {
		log.Printf("api: auto dispatch order: %v", err)
		return
	}
	s.metrics.Orders.Add(1)
	s.metrics.DutyDispatches.Add(1)
	_ = s.store.AddAudit(ctx, model.AuditEntry{
		ID: newID("aud"), At: now, Actor: "system",
		Action: "order.auto_dispatch", Resource: order.ID,
		Detail: map[string]any{"event_id": event.ID, "group": group.Name, "area_id": camera.AreaID},
	})
	s.hub.Broadcast(realtime.Frame{Type: "order_updated", Data: order})
	go s.deliverNotification(context.Background(), "dispatch", event, &order)
}

// visibleCameras returns the camera ids a user may read. restricted=false means
// the caller may read every camera (admin or an unrestricted account).
func (s *Server) visibleCameras(ctx context.Context, user auth.User) (ids []string, restricted bool) {
	if !s.auth.Enabled() || user.Role == auth.RoleAdmin || len(user.Areas) == 0 {
		return nil, false
	}
	cameras, err := s.store.ListCameras(ctx)
	if err != nil {
		return []string{}, true
	}
	allowed := make(map[string]struct{}, len(user.Areas))
	for _, area := range user.Areas {
		allowed[area] = struct{}{}
	}
	for _, camera := range cameras {
		if _, ok := allowed[camera.AreaID]; ok {
			ids = append(ids, camera.ID)
		}
	}
	return ids, true
}

func (s *Server) cameraVisible(ctx context.Context, user auth.User, cameraID string) (bool, error) {
	if !s.auth.Enabled() || user.Role == auth.RoleAdmin || len(user.Areas) == 0 {
		return true, nil
	}
	camera, err := s.store.GetCamera(ctx, cameraID)
	if err != nil {
		return false, err
	}
	return auth.InScope(user, camera.AreaID), nil
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"time":    time.Now().UTC().Format(time.RFC3339),
		"version": s.version,
	})
}

func (s *Server) handleAreas(w http.ResponseWriter, r *http.Request) {
	areas, err := s.store.ListAreas(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": areas})
}

func (s *Server) handleCameras(w http.ResponseWriter, r *http.Request) {
	cameras, err := s.store.ListCameras(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	if _, restricted := s.visibleCameras(r.Context(), userOf(r)); restricted {
		user := userOf(r)
		filtered := make([]model.Camera, 0, len(cameras))
		for _, camera := range cameras {
			if auth.InScope(user, camera.AreaID) {
				filtered = append(filtered, camera)
			}
		}
		cameras = filtered
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": cameras})
}

func (s *Server) handleCamera(w http.ResponseWriter, r *http.Request) {
	user := userOf(r)
	visible, err := s.cameraVisible(r.Context(), user, r.PathValue("id"))
	if err != nil {
		s.writeCameraError(w, err)
		return
	}
	if !visible {
		writeError(w, http.StatusForbidden, "out_of_scope", "camera is outside your assigned areas")
		return
	}
	camera, err := s.store.GetCamera(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeCameraError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, camera)
}

func (s *Server) handlePatchCamera(w http.ResponseWriter, r *http.Request) {
	var patch model.CameraPatch
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if patch.Status != nil && !validStatus(*patch.Status) {
		writeError(w, http.StatusBadRequest, "bad_request", "status must be online, offline or degraded")
		return
	}
	if !s.allowCamera(w, r, auth.PermCameraWrite, r.PathValue("id")) {
		return
	}
	camera, err := s.store.UpdateCamera(r.Context(), r.PathValue("id"), patch)
	if err != nil {
		s.writeCameraError(w, err)
		return
	}
	s.hub.Broadcast(realtime.Frame{Type: "camera_status", Data: map[string]any{
		"id": camera.ID, "code": camera.Code, "status": camera.Status,
	}})
	s.audit(r, "camera.update", camera.ID, map[string]any{"status": camera.Status})
	writeJSON(w, http.StatusOK, camera)
}

func (s *Server) handleSolveCalibration(w http.ResponseWriter, r *http.Request) {
	var request model.SolveRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if strings.TrimSpace(request.CameraID) == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "camera_id is required")
		return
	}
	if _, err := s.store.GetCamera(r.Context(), request.CameraID); err != nil {
		s.writeCameraError(w, err)
		return
	}
	if _, ok := s.allow(w, r, auth.PermCameraCalibration); !ok {
		return
	}
	result, err := calib.Solve(request.Points)
	if err != nil {
		if errors.Is(err, calib.ErrDegenerate) {
			writeError(w, http.StatusUnprocessableEntity, "calibration_degenerate", err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	s.audit(r, "camera.calibration.solve", request.CameraID, map[string]any{
		"points": result.PointsCount, "rmse": result.RMSE,
	})
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handlePutCalibration(w http.ResponseWriter, r *http.Request) {
	var request model.Calibration
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if request.PointsCount == 0 || !validMatrix(request.Matrix) {
		writeError(w, http.StatusBadRequest, "bad_request", "matrix and points_count are required")
		return
	}
	if !s.allowCamera(w, r, auth.PermCameraCalibration, r.PathValue("id")) {
		return
	}
	camera, err := s.store.SetCameraCalibration(r.Context(), r.PathValue("id"), &request)
	if err != nil {
		s.writeCameraError(w, err)
		return
	}
	s.audit(r, "camera.calibration.put", camera.ID, map[string]any{"rmse": request.RMSE})
	writeJSON(w, http.StatusOK, camera)
}

func (s *Server) handleDeleteCalibration(w http.ResponseWriter, r *http.Request) {
	if !s.allowCamera(w, r, auth.PermCameraCalibration, r.PathValue("id")) {
		return
	}
	camera, err := s.store.SetCameraCalibration(r.Context(), r.PathValue("id"), nil)
	if err != nil {
		s.writeCameraError(w, err)
		return
	}
	s.audit(r, "camera.calibration.delete", camera.ID, nil)
	writeJSON(w, http.StatusOK, camera)
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	filter := model.EventFilter{
		Limit:  parseIntDefault(r.URL.Query().Get("limit"), 50),
		Status: r.URL.Query().Get("status"),
		Type:   r.URL.Query().Get("type"),
	}
	if filter.Limit > 500 {
		filter.Limit = 500
	}
	if raw := r.URL.Query().Get("since"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "since must be RFC3339")
			return
		}
		filter.Since = &parsed
	}
	if ids, restricted := s.visibleCameras(r.Context(), userOf(r)); restricted {
		if len(ids) == 0 {
			writeJSON(w, http.StatusOK, map[string]any{"items": []model.Event{}, "total": 0})
			return
		}
		filter.CameraIDs = ids
	}
	events, err := s.store.ListEvents(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	counts, err := s.store.Counts(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": events, "total": counts.EventsTotal})
}

func (s *Server) handleGetEvent(w http.ResponseWriter, r *http.Request) {
	event, err := s.store.GetEvent(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeEventError(w, err)
		return
	}
	if visible, err := s.cameraVisible(r.Context(), userOf(r), event.CameraID); err != nil || !visible {
		writeError(w, http.StatusForbidden, "out_of_scope", "event camera is outside your assigned areas")
		return
	}
	actions, err := s.store.ListEventActions(r.Context(), event.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	if actions == nil {
		actions = []model.EventAction{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"event": event, "actions": actions})
}

func (s *Server) handleAckEvent(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.allowEventCamera(w, r, auth.PermEventAction, r.PathValue("id")); !ok {
		return
	}
	event, err := s.store.AckEvent(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeEventError(w, err)
		return
	}
	s.recordAction(r, event, "ack", "", "")
	s.hub.Broadcast(realtime.Frame{Type: "event_updated", Data: event})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "event": event})
}

func (s *Server) handleCloseEvent(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.allowEventCamera(w, r, auth.PermEventAction, r.PathValue("id")); !ok {
		return
	}
	var body struct {
		Note string `json:"note"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	event, err := s.store.CloseEvent(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeEventError(w, err)
		return
	}
	s.recordAction(r, event, "close", "", body.Note)
	s.hub.Broadcast(realtime.Frame{Type: "event_updated", Data: event})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "event": event})
}

func (s *Server) handleDispatchEvent(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Target string `json:"target"`
		Note   string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if strings.TrimSpace(body.Target) == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "target is required")
		return
	}
	event, ok := s.allowEventCamera(w, r, auth.PermEventAction, r.PathValue("id"))
	if !ok {
		return
	}
	action := s.recordAction(r, event, "dispatch", body.Target, body.Note)
	s.hub.Broadcast(realtime.Frame{Type: "event_updated", Data: event})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "event": event, "action": action})
}

func (s *Server) handleTracks(w http.ResponseWriter, r *http.Request) {
	filter := model.TrackFilter{
		CameraID: r.URL.Query().Get("camera_id"),
		Limit:    parseIntDefault(r.URL.Query().Get("limit"), 500),
	}
	if filter.Limit > 5000 {
		filter.Limit = 5000
	}
	if raw := r.URL.Query().Get("since"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "since must be RFC3339")
			return
		}
		filter.Since = &parsed
	}
	if ids, restricted := s.visibleCameras(r.Context(), userOf(r)); restricted {
		if len(ids) == 0 {
			writeJSON(w, http.StatusOK, map[string]any{"items": []model.TrackPoint{}})
			return
		}
		if filter.CameraID != "" {
			allowed := false
			for _, id := range ids {
				if id == filter.CameraID {
					allowed = true
					break
				}
			}
			if !allowed {
				writeError(w, http.StatusForbidden, "out_of_scope", "camera is outside your assigned areas")
				return
			}
		}
		filter.CameraIDs = ids
	}
	points, err := s.store.ListTrackPoints(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": points})
}

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.allow(w, r, auth.PermAdmin); !ok {
		return
	}
	filter := model.AuditFilter{
		Limit:  parseIntDefault(r.URL.Query().Get("limit"), 100),
		Action: r.URL.Query().Get("action"),
		Actor:  r.URL.Query().Get("actor"),
	}
	if filter.Limit > 1000 {
		filter.Limit = 1000
	}
	entries, err := s.store.ListAudit(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": entries})
}

func (s *Server) handleStatsSummary(w http.ResponseWriter, r *http.Request) {
	from, to, err := parseRange(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	cameraIDs, _ := s.visibleCameras(r.Context(), userOf(r))
	summary, err := s.store.Stats(r.Context(), from, to, cameraIDs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	if summary.ByCamera == nil {
		summary.ByCamera = []model.CameraStat{}
	}
	if summary.ByHour == nil {
		summary.ByHour = []model.HourStat{}
	}
	writeJSON(w, http.StatusOK, summary)
}

func (s *Server) handleStatsCSV(w http.ResponseWriter, r *http.Request) {
	from, to, err := parseRange(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	cameraIDs, restricted := s.visibleCameras(r.Context(), userOf(r))
	csvFilter := model.EventFilter{Limit: 5000, Since: &from}
	if restricted {
		csvFilter.CameraIDs = cameraIDs
	}
	events, err := s.store.ListEvents(r.Context(), csvFilter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="events.csv"`)
	writer := csv.NewWriter(w)
	_ = writer.Write([]string{"id", "camera_code", "camera_name", "type", "severity", "confidence", "x", "y", "z", "occurred_at", "status"})
	for _, event := range events {
		if event.OccurredAt.After(to) {
			continue
		}
		_ = writer.Write([]string{
			event.ID, event.CameraCode, event.CameraName, event.Type, event.Severity,
			fmt.Sprintf("%.3f", event.Confidence), fmt.Sprintf("%.2f", event.X), fmt.Sprintf("%.2f", event.Y),
			fmt.Sprintf("%.2f", event.Z), event.OccurredAt.Format(time.RFC3339), event.Status,
		})
	}
	writer.Flush()
}

func (s *Server) handleIngest(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	var request model.IngestRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	request.CameraCode = strings.TrimSpace(request.CameraCode)
	if request.CameraCode == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "camera_code is required")
		return
	}
	camera, err := s.store.GetCameraByCode(r.Context(), request.CameraCode)
	if err != nil {
		s.writeCameraError(w, err)
		return
	}
	occurredAt := time.Now().UTC()
	if request.TS != nil {
		occurredAt = request.TS.UTC()
	}
	cameraRules, rulesErr := s.store.GetCameraRules(r.Context(), camera.ID)
	if rulesErr != nil {
		cameraRules = model.DefaultCameraRules(camera.ID)
	}
	result := s.rules.Evaluate(camera, cameraRules, request.Detections, occurredAt)
	s.metrics.Detections.Add(int64(len(request.Detections)))
	s.metrics.Dropped.Add(int64(result.Dropped))
	for _, event := range result.Events {
		s.metrics.Event(event.Severity)
	}
	if err := s.store.CreateEvents(r.Context(), result.Events); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	// P1: persist and broadcast track points for detections that carry a track
	// id and a real position (explicit world or camera calibration).
	points := make([]model.TrackPoint, 0, len(request.Detections))
	for _, detection := range request.Detections {
		if strings.TrimSpace(detection.TrackID) == "" {
			continue
		}
		x, y, z, source := rules.ProjectToWorld(camera, detection)
		if source == "fallback" {
			continue
		}
		points = append(points, model.TrackPoint{
			TrackID: detection.TrackID, CameraID: camera.ID, Class: detection.Class,
			X: x, Y: y, Z: z, TS: occurredAt,
		})
	}
	if err := s.store.CreateTrackPoints(r.Context(), points); err != nil {
		log.Printf("api: persist track points: %v", err)
	}
	for _, point := range points {
		s.hub.Broadcast(realtime.Frame{Type: "track_point", Data: point})
	}
	for _, event := range result.Events {
		s.hub.Broadcast(realtime.Frame{Type: "event", Data: event})
		go s.deliverNotification(context.Background(), "event", event, nil)
	}
	s.metrics.ObserveIngest(time.Since(started).Microseconds())
	events := result.Events
	if events == nil {
		events = []model.Event{}
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"accepted": len(events),
		"dropped":  result.Dropped,
		"events":   events,
		"tracks":   len(points),
	})
}

func (s *Server) handleMetrics(w http.ResponseWriter, _ *http.Request) {
	counts, err := s.store.Counts(context.Background())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(s.metrics.Text(s.hub.Clients(), int(counts.OnlineCameras))))
}

func (s *Server) recordAction(r *http.Request, event model.Event, action, target, note string) model.EventAction {
	entry := model.EventAction{
		ID: newID("act"), EventID: event.ID, Action: action, Actor: actorOf(r),
		Target: target, Note: note, At: time.Now().UTC(),
	}
	if err := s.store.CreateEventAction(r.Context(), entry); err != nil {
		log.Printf("api: record event action: %v", err)
	}
	s.audit(r, "event."+action, event.ID, map[string]any{"target": target, "note": note})
	return entry
}

func (s *Server) audit(r *http.Request, action, resource string, detail map[string]any) {
	if detail == nil {
		detail = map[string]any{}
	}
	entry := model.AuditEntry{
		ID: newID("aud"), At: time.Now().UTC(), Actor: actorOf(r),
		Action: action, Resource: resource, Detail: detail,
	}
	if err := s.store.AddAudit(r.Context(), entry); err != nil {
		log.Printf("api: add audit: %v", err)
	}
}

func actorOf(r *http.Request) string {
	if r == nil {
		return "system"
	}
	if user := userOf(r); user.Username != "" {
		return user.Username
	}
	return "system"
}

func (s *Server) handleGetRules(w http.ResponseWriter, r *http.Request) {
	cameraID := r.PathValue("id")
	visible, err := s.cameraVisible(r.Context(), userOf(r), cameraID)
	if err != nil {
		s.writeCameraError(w, err)
		return
	}
	if !visible {
		writeError(w, http.StatusForbidden, "out_of_scope", "camera is outside your assigned areas")
		return
	}
	if _, err := s.store.GetCamera(r.Context(), cameraID); err != nil {
		s.writeCameraError(w, err)
		return
	}
	rules, err := s.store.GetCameraRules(r.Context(), cameraID)
	if err != nil {
		rules = model.DefaultCameraRules(cameraID)
	}
	if rules.EnabledClasses == nil {
		rules.EnabledClasses = []string{}
	}
	writeJSON(w, http.StatusOK, rules)
}

func (s *Server) handlePutRules(w http.ResponseWriter, r *http.Request) {
	cameraID := r.PathValue("id")
	var rules model.CameraRules
	if err := json.NewDecoder(r.Body).Decode(&rules); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if err := validateRules(&rules); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if !s.allowCamera(w, r, auth.PermCameraRules, cameraID) {
		return
	}
	if _, err := s.store.GetCamera(r.Context(), cameraID); err != nil {
		s.writeCameraError(w, err)
		return
	}
	rules.CameraID = cameraID
	saved, err := s.store.SetCameraRules(r.Context(), rules)
	if err != nil {
		s.writeCameraError(w, err)
		return
	}
	s.audit(r, "camera.rules.put", cameraID, map[string]any{
		"confidence_threshold": saved.ConfidenceThreshold,
		"dedupe_seconds":       saved.DedupeSeconds,
	})
	writeJSON(w, http.StatusOK, saved)
}

func (s *Server) handleCreateOrder(w http.ResponseWriter, r *http.Request) {
	var body struct {
		EventID  string `json:"event_id"`
		Title    string `json:"title"`
		Priority string `json:"priority"`
		Assignee string `json:"assignee"`
		Note     string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if strings.TrimSpace(body.EventID) == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "event_id is required")
		return
	}
	event, ok := s.allowEventCamera(w, r, auth.PermOrderWrite, body.EventID)
	if !ok {
		return
	}
	priority := strings.TrimSpace(body.Priority)
	if priority == "" {
		priority = "normal"
	}
	if !validPriority(priority) {
		writeError(w, http.StatusBadRequest, "bad_request", "priority must be low, normal, high or urgent")
		return
	}
	title := strings.TrimSpace(body.Title)
	if title == "" {
		title = fmt.Sprintf("%s · %s %s", event.Type, event.CameraCode, event.CameraName)
	}
	now := time.Now().UTC()
	order := model.Order{
		ID: newID("ord"), EventID: event.ID, CameraID: event.CameraID, Title: title,
		Status: "open", Priority: priority, Assignee: strings.TrimSpace(body.Assignee),
		Note: strings.TrimSpace(body.Note), CreatedAt: now, UpdatedAt: now,
	}
	if err := s.store.CreateOrder(r.Context(), order); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	s.metrics.Orders.Add(1)
	s.audit(r, "order.create", order.ID, map[string]any{"event_id": order.EventID, "priority": order.Priority})
	s.hub.Broadcast(realtime.Frame{Type: "order_updated", Data: order})
	writeJSON(w, http.StatusCreated, order)
}

func (s *Server) handleListOrders(w http.ResponseWriter, r *http.Request) {
	filter := model.OrderFilter{
		Status:   r.URL.Query().Get("status"),
		Assignee: r.URL.Query().Get("assignee"),
		Limit:    parseIntDefault(r.URL.Query().Get("limit"), 100),
	}
	if filter.Limit > 500 {
		filter.Limit = 500
	}
	if ids, restricted := s.visibleCameras(r.Context(), userOf(r)); restricted {
		if len(ids) == 0 {
			writeJSON(w, http.StatusOK, map[string]any{"items": []model.Order{}})
			return
		}
		filter.CameraIDs = ids
	}
	orders, err := s.store.ListOrders(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	if orders == nil {
		orders = []model.Order{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": orders})
}

func (s *Server) handleGetOrder(w http.ResponseWriter, r *http.Request) {
	order, err := s.store.GetOrder(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeOrderError(w, err)
		return
	}
	if visible, err := s.cameraVisible(r.Context(), userOf(r), order.CameraID); err != nil || !visible {
		writeError(w, http.StatusForbidden, "out_of_scope", "order camera is outside your assigned areas")
		return
	}
	writeJSON(w, http.StatusOK, order)
}

func (s *Server) handlePatchOrder(w http.ResponseWriter, r *http.Request) {
	var update model.OrderUpdate
	if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if update.Status != nil && !validOrderStatus(*update.Status) {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid order status")
		return
	}
	existing, err := s.store.GetOrder(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeOrderError(w, err)
		return
	}
	if !s.allowCamera(w, r, auth.PermOrderWrite, existing.CameraID) {
		return
	}
	order, err := s.store.UpdateOrder(r.Context(), existing.ID, update)
	if err != nil {
		s.writeOrderError(w, err)
		return
	}
	s.audit(r, "order.update", order.ID, map[string]any{"status": order.Status, "assignee": order.Assignee})
	s.hub.Broadcast(realtime.Frame{Type: "order_updated", Data: order})
	writeJSON(w, http.StatusOK, order)
}

func (s *Server) writeOrderError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "order_not_found", "order not found")
		return
	}
	writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
}

func validateRules(rules *model.CameraRules) error {
	if rules.ConfidenceThreshold < 0 || rules.ConfidenceThreshold > 1 {
		return errors.New("confidence_threshold must be within [0,1]")
	}
	if rules.DedupeSeconds < 0 || rules.DedupeSeconds > 300 {
		return errors.New("dedupe_seconds must be within [0,300]")
	}
	if len(rules.ROI) > 0 && len(rules.ROI) < 3 {
		return errors.New("roi needs at least 3 points")
	}
	for _, point := range rules.ROI {
		if point[0] < 0 || point[0] > 1 || point[1] < 0 || point[1] > 1 {
			return errors.New("roi points must be normalized to [0,1]")
		}
	}
	for _, class := range rules.EnabledClasses {
		switch class {
		case "person", "vehicle", "intrusion", "smoke", "no_helmet", "other":
		default:
			return fmt.Errorf("unknown enabled class %q", class)
		}
	}
	return nil
}

func validPriority(priority string) bool {
	switch priority {
	case "low", "normal", "high", "urgent":
		return true
	}
	return false
}

func validOrderStatus(status string) bool {
	switch status {
	case "open", "assigned", "processing", "done", "cancelled":
		return true
	}
	return false
}

func (s *Server) handleGetNotificationSettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.allow(w, r, auth.PermAdmin); !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.currentNotify())
}

func (s *Server) handlePutNotificationSettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.allow(w, r, auth.PermAdmin); !ok {
		return
	}
	var cfg model.NotificationSettings
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	cfg.WebhookURL = strings.TrimSpace(cfg.WebhookURL)
	if cfg.Format == "" {
		cfg.Format = "generic"
	}
	if cfg.MinSeverity == "" {
		cfg.MinSeverity = "warn"
	}
	if cfg.TimeoutSeconds == 0 {
		cfg.TimeoutSeconds = 5
	}
	if cfg.Format != "generic" && cfg.Format != "feishu" {
		writeError(w, http.StatusBadRequest, "bad_request", "format must be generic or feishu")
		return
	}
	if !validSeverity(cfg.MinSeverity) {
		writeError(w, http.StatusBadRequest, "bad_request", "min_severity must be info, warn or critical")
		return
	}
	if cfg.TimeoutSeconds < 1 || cfg.TimeoutSeconds > 30 {
		writeError(w, http.StatusBadRequest, "bad_request", "timeout_seconds must be within [1,30]")
		return
	}
	if cfg.MaxAttempts == 0 {
		cfg.MaxAttempts = 3
	}
	if cfg.MaxAttempts < 1 || cfg.MaxAttempts > 5 {
		writeError(w, http.StatusBadRequest, "bad_request", "max_attempts must be within [1,5]")
		return
	}
	if cfg.BackoffSeconds < 0 || cfg.BackoffSeconds > 60 {
		writeError(w, http.StatusBadRequest, "bad_request", "backoff_seconds must be within [0,60]")
		return
	}
	if cfg.SilenceSeconds < 0 || cfg.SilenceSeconds > 3600 {
		writeError(w, http.StatusBadRequest, "bad_request", "silence_seconds must be within [0,3600]")
		return
	}
	if cfg.WebhookURL != "" && !strings.HasPrefix(cfg.WebhookURL, "http://") && !strings.HasPrefix(cfg.WebhookURL, "https://") {
		writeError(w, http.StatusBadRequest, "bad_request", "webhook_url must be http(s)")
		return
	}
	if cfg.Enabled && cfg.WebhookURL == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "webhook_url is required when notifications are enabled")
		return
	}
	raw, _ := json.Marshal(cfg)
	if err := s.store.SetSetting(r.Context(), settingNotifications, raw); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	s.settingsMu.Lock()
	s.notify = cfg
	s.settingsMu.Unlock()
	s.audit(r, "settings.notifications.put", settingNotifications, map[string]any{"enabled": cfg.Enabled, "format": cfg.Format})
	writeJSON(w, http.StatusOK, cfg)
}

func (s *Server) handleGetEscalationSettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.allow(w, r, auth.PermAdmin); !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.currentEscalation())
}

func (s *Server) handlePutEscalationSettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.allow(w, r, auth.PermAdmin); !ok {
		return
	}
	var cfg model.EscalationSettings
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if cfg.AfterSeconds < 0 || cfg.AfterSeconds > 86400 {
		writeError(w, http.StatusBadRequest, "bad_request", "after_seconds must be within [0,86400]")
		return
	}
	for _, severity := range cfg.Severities {
		if !validSeverity(severity) {
			writeError(w, http.StatusBadRequest, "bad_request", "severities must be info, warn or critical")
			return
		}
	}
	if cfg.Severities == nil {
		cfg.Severities = []string{}
	}
	raw, _ := json.Marshal(cfg)
	if err := s.store.SetSetting(r.Context(), settingEscalation, raw); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	s.settingsMu.Lock()
	s.escalation = cfg
	s.settingsMu.Unlock()
	s.audit(r, "settings.escalation.put", settingEscalation, map[string]any{
		"enabled": cfg.Enabled, "after_seconds": cfg.AfterSeconds,
	})
	writeJSON(w, http.StatusOK, cfg)
}

func (s *Server) handleListNotifications(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.allow(w, r, auth.PermAdmin); !ok {
		return
	}
	limit := parseIntDefault(r.URL.Query().Get("limit"), 50)
	if limit > 200 {
		limit = 200
	}
	entries, err := s.store.ListNotificationLog(r.Context(), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	if entries == nil {
		entries = []model.NotificationLog{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": entries})
}

func validSeverity(severity string) bool {
	return severity == "info" || severity == "warn" || severity == "critical"
}

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.allow(w, r, auth.PermAdmin); !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": s.auth.Users()})
}

func (s *Server) handleListDutyGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := s.store.ListDutyGroups(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	if groups == nil {
		groups = []model.DutyGroup{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": groups})
}

type dutyGroupBody struct {
	Name      string    `json:"name"`
	Areas     *[]string `json:"areas,omitempty"`
	Operators *[]string `json:"operators,omitempty"`
	Active    *bool     `json:"active,omitempty"`
}

func (s *Server) handleCreateDutyGroup(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.allow(w, r, auth.PermAdmin); !ok {
		return
	}
	var body dutyGroupBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "name is required")
		return
	}
	if err := s.validateDutyGroups(r.Context(), body.Areas, body.Operators); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	active := true
	if body.Active != nil {
		active = *body.Active
	}
	now := time.Now().UTC()
	group := model.DutyGroup{
		ID: newID("duty"), Name: body.Name, Active: active,
		Areas: []string{}, Operators: []string{}, CreatedAt: now, UpdatedAt: now,
	}
	if body.Areas != nil {
		group.Areas = *body.Areas
	}
	if body.Operators != nil {
		group.Operators = *body.Operators
	}
	if err := s.store.CreateDutyGroup(r.Context(), group); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	s.audit(r, "duty.create", group.ID, map[string]any{"name": group.Name, "areas": group.Areas})
	writeJSON(w, http.StatusCreated, group)
}

func (s *Server) handleUpdateDutyGroup(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.allow(w, r, auth.PermAdmin); !ok {
		return
	}
	existing, err := s.store.GetDutyGroup(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeDutyError(w, err)
		return
	}
	var body dutyGroupBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if err := s.validateDutyGroups(r.Context(), body.Areas, body.Operators); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if body.Name != "" {
		existing.Name = strings.TrimSpace(body.Name)
	}
	if body.Areas != nil {
		existing.Areas = *body.Areas
	}
	if body.Operators != nil {
		existing.Operators = *body.Operators
	}
	if body.Active != nil {
		existing.Active = *body.Active
	}
	updated, err := s.store.UpdateDutyGroup(r.Context(), existing.ID, existing)
	if err != nil {
		s.writeDutyError(w, err)
		return
	}
	s.audit(r, "duty.update", updated.ID, map[string]any{"active": updated.Active, "areas": updated.Areas})
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleDeleteDutyGroup(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.allow(w, r, auth.PermAdmin); !ok {
		return
	}
	id := r.PathValue("id")
	if err := s.store.DeleteDutyGroup(r.Context(), id); err != nil {
		s.writeDutyError(w, err)
		return
	}
	s.audit(r, "duty.delete", id, nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleCurrentDuty(w http.ResponseWriter, r *http.Request) {
	areaID := r.URL.Query().Get("area_id")
	if areaID == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "area_id is required")
		return
	}
	group := s.resolveDutyGroup(r.Context(), areaID)
	if group == nil {
		writeJSON(w, http.StatusOK, map[string]any{"area_id": areaID, "group": nil, "operators": []string{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"area_id": areaID, "group": group, "operators": group.Operators})
}

func (s *Server) validateDutyGroups(ctx context.Context, areas, operators *[]string) error {
	if areas != nil {
		known, err := s.store.ListAreas(ctx)
		if err != nil {
			return err
		}
		valid := make(map[string]struct{}, len(known))
		for _, area := range known {
			valid[area.ID] = struct{}{}
		}
		for _, area := range *areas {
			if _, ok := valid[area]; !ok {
				return fmt.Errorf("unknown area %q", area)
			}
		}
	}
	if operators != nil {
		for _, username := range *operators {
			if !s.auth.UserExists(username) {
				return fmt.Errorf("unknown operator %q", username)
			}
		}
	}
	return nil
}

func (s *Server) writeDutyError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "duty_not_found", "duty group not found")
		return
	}
	writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
}

func (s *Server) writeCameraError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "camera_not_found", "camera not found")
		return
	}
	writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
}

func (s *Server) writeEventError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "event_not_found", "event not found")
		return
	}
	writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
}

func validStatus(status string) bool {
	return status == "online" || status == "offline" || status == "degraded"
}

func validMatrix(matrix [3][3]float64) bool {
	for _, row := range matrix {
		for _, value := range row {
			if value != value {
				return false
			}
		}
	}
	return matrix[2][2] != 0
}

func parseIntDefault(raw string, fallback int) int {
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

// parseRange reads optional from/to RFC3339 query parameters (default 24h).
func parseRange(r *http.Request) (time.Time, time.Time, error) {
	now := time.Now().UTC()
	from := now.Add(-24 * time.Hour)
	to := now
	if raw := r.URL.Query().Get("from"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return from, to, errors.New("from must be RFC3339")
		}
		from = parsed
	}
	if raw := r.URL.Query().Get("to"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return from, to, errors.New("to must be RFC3339")
		}
		to = parsed
	}
	if to.Before(from) {
		return from, to, errors.New("to must not be before from")
	}
	return from, to, nil
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		log.Printf("api: write response: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"error": code, "message": message})
}

func newID(prefix string) string {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return prefix + "_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return prefix + "_" + hex.EncodeToString(raw[:])
}

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Ingest-Token")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
