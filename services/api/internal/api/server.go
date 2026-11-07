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

	"digital-twin-platform/api/internal/calib"
	"digital-twin-platform/api/internal/metrics"
	"digital-twin-platform/api/internal/model"
	"digital-twin-platform/api/internal/realtime"
	"digital-twin-platform/api/internal/rules"
	"digital-twin-platform/api/internal/store"
)

// Server wires the store, rule engine and WebSocket hub into HTTP handlers.
type Server struct {
	store     store.Store
	rules     *rules.Engine
	hub       *realtime.Hub
	metrics   *metrics.Metrics
	version   string
	authToken string
	started   time.Time
}

// New builds the API server. authToken enables the optional bearer-token guard.
func New(database store.Store, engine *rules.Engine, hub *realtime.Hub, meters *metrics.Metrics, version, authToken string) *Server {
	return &Server{
		store:     database,
		rules:     engine,
		hub:       hub,
		metrics:   meters,
		version:   version,
		authToken: authToken,
		started:   time.Now().UTC(),
	}
}

// Handler returns the router with CORS and the optional token guard.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/areas", s.handleAreas)
	mux.HandleFunc("GET /api/cameras", s.handleCameras)
	mux.HandleFunc("GET /api/cameras/{id}", s.handleCamera)
	mux.HandleFunc("PATCH /api/cameras/{id}", s.handlePatchCamera)
	mux.HandleFunc("PUT /api/cameras/{id}/calibration", s.handlePutCalibration)
	mux.HandleFunc("DELETE /api/cameras/{id}/calibration", s.handleDeleteCalibration)
	mux.HandleFunc("POST /api/calibration/solve", s.handleSolveCalibration)
	mux.HandleFunc("GET /api/events", s.handleEvents)
	mux.HandleFunc("GET /api/events/{id}", s.handleGetEvent)
	mux.HandleFunc("POST /api/events/{id}/ack", s.handleAckEvent)
	mux.HandleFunc("POST /api/events/{id}/close", s.handleCloseEvent)
	mux.HandleFunc("POST /api/events/{id}/dispatch", s.handleDispatchEvent)
	mux.HandleFunc("GET /api/tracks", s.handleTracks)
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
	return cors(s.auth(mux))
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

// auth enforces the optional bearer token. Health, metrics and assets stay open.
func (s *Server) auth(next http.Handler) http.Handler {
	if s.authToken == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/health" || r.URL.Path == "/metrics" {
			next.ServeHTTP(w, r)
			return
		}
		presented := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if r.URL.Path == "/ws" {
			presented = r.URL.Query().Get("token")
		}
		if subtle.ConstantTimeCompare([]byte(presented), []byte(s.authToken)) != 1 {
			writeError(w, http.StatusUnauthorized, "unauthorized", "a valid bearer token is required")
			return
		}
		next.ServeHTTP(w, r)
	})
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
	camera, err := s.store.SetCameraCalibration(r.Context(), r.PathValue("id"), &request)
	if err != nil {
		s.writeCameraError(w, err)
		return
	}
	s.audit(r, "camera.calibration.put", camera.ID, map[string]any{"rmse": request.RMSE})
	writeJSON(w, http.StatusOK, camera)
}

func (s *Server) handleDeleteCalibration(w http.ResponseWriter, r *http.Request) {
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
	event, err := s.store.GetEvent(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeEventError(w, err)
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
	result := s.rules.Evaluate(camera, request.Detections, occurredAt)
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
	if actor := strings.TrimSpace(r.Header.Get("X-Actor")); actor != "" {
		return actor
	}
	return "web"
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
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Actor")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
