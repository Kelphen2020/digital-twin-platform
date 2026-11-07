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
}

type userContextKey struct{}

// New builds the API server. A nil auth service means authentication is off.
func New(database store.Store, engine *rules.Engine, hub *realtime.Hub, meters *metrics.Metrics, version string, authService *auth.Service, ingestToken string) *Server {
	if authService == nil {
		authService = auth.New("off", "")
	}
	return &Server{
		store:       database,
		rules:       engine,
		hub:         hub,
		metrics:     meters,
		version:     version,
		auth:        authService,
		ingestToken: ingestToken,
		started:     time.Now().UTC(),
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
	writeJSON(w, http.StatusOK, map[string]any{"items": cameras})
}

func (s *Server) handleCamera(w http.ResponseWriter, r *http.Request) {
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
	points, err := s.store.ListTrackPoints(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": points})
}

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
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
	summary, err := s.store.Stats(r.Context(), from, to)
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
	events, err := s.store.ListEvents(r.Context(), model.EventFilter{Limit: 5000, Since: &from})
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
