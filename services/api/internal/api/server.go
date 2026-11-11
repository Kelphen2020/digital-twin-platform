// Package api exposes the HTTP and WebSocket surface described in docs/API.md
// and docs/API_P1.md.
package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"digital-twin-platform/api/internal/auth"
	"digital-twin-platform/api/internal/bus"
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

	settingsMu           sync.RWMutex
	notify               model.NotificationSettings
	escalation           model.EscalationSettings
	notifyMu             sync.Mutex
	notifySilence        map[string]time.Time
	rotationMu           sync.Mutex
	rotation             map[string]int
	bus                  *bus.Bus
	site                 model.Site
	trackSample          int
	loginMu              sync.Mutex
	loginFailures        map[string]loginFailure
	loginMax             int
	loginLockout         time.Duration
	refreshTTL           time.Duration
	ingestRateMu         sync.Mutex
	ingestRates          map[string]*RateLimiter
	rateStore            store.RateStore
	ingestFallbackURL    string
	ingestClient         *http.Client
	patrolPendingTimeout time.Duration
	patrolRunningTimeout time.Duration
	trackSequence        atomic.Uint64
	ingestLimiter        *RateLimiter
	maxBodyBytes         int64
}

type userContextKey struct{}

// New builds the API server. A nil auth service means authentication is off.
func New(database store.Store, engine *rules.Engine, hub *realtime.Hub, meters *metrics.Metrics, version string, authService *auth.Service, ingestToken string) *Server {
	if authService == nil {
		authService = auth.New("off", "")
	}
	return &Server{
		store:                database,
		rules:                engine,
		hub:                  hub,
		metrics:              meters,
		version:              version,
		auth:                 authService,
		ingestToken:          ingestToken,
		started:              time.Now().UTC(),
		notify:               model.DefaultNotificationSettings(),
		escalation:           model.DefaultEscalationSettings(),
		notifySilence:        make(map[string]time.Time),
		rotation:             make(map[string]int),
		site:                 model.DefaultSite(),
		trackSample:          1,
		loginFailures:        make(map[string]loginFailure),
		loginMax:             5,
		loginLockout:         60 * time.Second,
		refreshTTL:           7 * 24 * time.Hour,
		patrolPendingTimeout: 60 * time.Minute,
		patrolRunningTimeout: 120 * time.Minute,
	}
}

// Handler returns the router with CORS and the optional token guard.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	mux.HandleFunc("GET /api/auth/me", s.handleMe)
	mux.HandleFunc("POST /api/auth/refresh", s.handleRefresh)
	mux.HandleFunc("POST /api/auth/logout", s.handleLogout)
	mux.HandleFunc("POST /api/auth/logout-all", s.handleLogoutAll)
	mux.HandleFunc("GET /api/tenants", s.handleListTenants)
	mux.HandleFunc("POST /api/tenants", s.handleCreateTenant)
	mux.HandleFunc("PATCH /api/tenants/{id}", s.handleUpdateTenant)
	mux.HandleFunc("POST /api/tenants/{id}/archive", s.handleArchiveTenant)
	mux.HandleFunc("POST /api/tenants/{id}/restore", s.handleRestoreTenant)
	mux.HandleFunc("DELETE /api/tenants/{id}", s.handleDeleteTenant)
	mux.HandleFunc("GET /api/tenants/{id}/export", s.handleExportTenant)
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
	mux.HandleFunc("POST /api/users", s.handleCreateUser)
	mux.HandleFunc("PATCH /api/users/{id}", s.handleUpdateUser)
	mux.HandleFunc("DELETE /api/users/{id}", s.handleDeleteUser)
	mux.HandleFunc("GET /api/users/{id}/sessions", s.handleUserSessions)
	mux.HandleFunc("POST /api/users/{id}/logout", s.handleForceLogout)
	mux.HandleFunc("GET /api/duty/groups", s.handleListDutyGroups)
	mux.HandleFunc("POST /api/duty/groups", s.handleCreateDutyGroup)
	mux.HandleFunc("PATCH /api/duty/groups/{id}", s.handleUpdateDutyGroup)
	mux.HandleFunc("DELETE /api/duty/groups/{id}", s.handleDeleteDutyGroup)
	mux.HandleFunc("GET /api/duty/current", s.handleCurrentDuty)
	mux.HandleFunc("GET /api/patrol/routes", s.handleListPatrolRoutes)
	mux.HandleFunc("POST /api/patrol/routes", s.handleCreatePatrolRoute)
	mux.HandleFunc("PATCH /api/patrol/routes/{id}", s.handleUpdatePatrolRoute)
	mux.HandleFunc("DELETE /api/patrol/routes/{id}", s.handleDeletePatrolRoute)
	mux.HandleFunc("GET /api/patrol/schedules", s.handleListPatrolSchedules)
	mux.HandleFunc("POST /api/patrol/schedules", s.handleCreatePatrolSchedule)
	mux.HandleFunc("PATCH /api/patrol/schedules/{id}", s.handleUpdatePatrolSchedule)
	mux.HandleFunc("DELETE /api/patrol/schedules/{id}", s.handleDeletePatrolSchedule)
	mux.HandleFunc("POST /api/patrol/schedules/{id}/run", s.handleRunPatrolSchedule)
	mux.HandleFunc("GET /api/patrol/executions", s.handleListPatrolExecutions)
	mux.HandleFunc("POST /api/patrol/executions/{id}/claim", s.handleClaimPatrolExecution)
	mux.HandleFunc("POST /api/patrol/executions/{id}/complete", s.handleCompletePatrolExecution)
	mux.HandleFunc("POST /api/patrol/executions/{id}/cancel", s.handleCancelPatrolExecution)
	mux.HandleFunc("GET /api/patrol/stats", s.handlePatrolStats)
	mux.HandleFunc("GET /api/patrol/executions/export", s.handlePatrolExecutionsExport)
	mux.HandleFunc("DELETE /api/patrol/executions", s.handlePurgePatrolExecutions)
	mux.HandleFunc("GET /api/site", s.handleSite)
	mux.HandleFunc("GET /api/bus/status", s.handleBusStatus)
	mux.HandleFunc("POST /api/bus/replay", s.handleBusReplay)
	mux.HandleFunc("POST /api/orders", s.handleCreateOrder)
	mux.HandleFunc("GET /api/orders", s.handleListOrders)
	mux.HandleFunc("GET /api/orders/{id}", s.handleGetOrder)
	mux.HandleFunc("PATCH /api/orders/{id}", s.handlePatchOrder)
	mux.HandleFunc("GET /api/audit", s.handleAudit)
	mux.HandleFunc("GET /api/audit/export", s.handleAuditExport)
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
	return cors(securityHeaders(s.authenticate(mux)))
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
			ctx, tenantID, ok := s.resolveTenant(w, r, requestTenant(r))
			if !ok {
				return
			}
			s.metrics.RecordTenantRequest(tenantID)
			ctx = context.WithValue(ctx, userContextKey{}, auth.DevUser())
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, tenantID, ok := s.resolveTenant(w, r, requestTenant(r))
		if !ok {
			return
		}
		s.metrics.RecordTenantRequest(tenantID)
		r = r.WithContext(ctx)
		path := r.URL.Path
		switch path {
		case "/api/health", "/metrics", "/api/auth/login", "/api/auth/refresh", "/api/auth/logout":
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
		if user.TenantID != "" && user.TenantID != tenantID {
			writeError(w, http.StatusForbidden, "cross_tenant", "this token belongs to another tenant")
			return
		}
		account, ok, err := s.auth.GetUser(r.Context(), user.ID)
		if err != nil || !ok || !account.Active || account.TokenVersion != user.TokenVersion {
			writeError(w, http.StatusUnauthorized, "unauthorized", "this session is no longer valid; sign in again")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userContextKey{}, account)))
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

type loginFailure struct {
	count       int
	lockedUntil time.Time
}

// SetLoginGuard configures the brute-force lockout (0 max disables it).
func (s *Server) SetLoginGuard(maxFailures int, lockout time.Duration) {
	if maxFailures < 0 {
		maxFailures = 0
	}
	if lockout <= 0 {
		lockout = time.Minute
	}
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	s.loginMax = maxFailures
	s.loginLockout = lockout
}

// loginLocked reports the remaining lock seconds for a username+IP key.
func (s *Server) loginLocked(key string) (int, bool) {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	entry, exists := s.loginFailures[key]
	if !exists || entry.lockedUntil.IsZero() {
		return 0, false
	}
	remaining := time.Until(entry.lockedUntil)
	if remaining <= 0 {
		delete(s.loginFailures, key)
		return 0, false
	}
	return int(remaining.Seconds()) + 1, true
}

// recordLoginFailure counts a failed sign-in and locks the key past the limit.
func (s *Server) recordLoginFailure(key string) {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	if s.loginMax <= 0 {
		return
	}
	entry := s.loginFailures[key]
	entry.count++
	if entry.count >= s.loginMax {
		entry.lockedUntil = time.Now().Add(s.loginLockout)
	}
	s.loginFailures[key] = entry
}

func (s *Server) clearLoginFailures(key string) {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	delete(s.loginFailures, key)
}

// clientIP resolves the caller address, honoring a reverse proxy header.
func clientIP(r *http.Request) string {
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		parts := strings.Split(forwarded, ",")
		if candidate := strings.TrimSpace(parts[0]); candidate != "" {
			return candidate
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func newRefreshToken() (string, string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(token))
	return token, hex.EncodeToString(sum[:]), nil
}

// issueRefresh stores one refresh token and returns the raw value and expiry.
func (s *Server) issueRefresh(ctx context.Context, userID, ip, userAgent string) (string, time.Time, error) {
	token, hash, err := newRefreshToken()
	if err != nil {
		return "", time.Time{}, err
	}
	now := time.Now().UTC()
	expires := now.Add(s.refreshTTL)
	entry := model.RefreshToken{
		ID: newID("rft"), UserID: userID, TokenHash: hash, ExpiresAt: expires,
		CreatedAt: now, IP: ip, UserAgent: userAgent,
	}
	if err := s.store.CreateRefreshToken(ctx, entry); err != nil {
		return "", time.Time{}, err
	}
	return token, expires, nil
}

func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.RefreshToken) == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "refresh_token is required")
		return
	}
	sum := sha256.Sum256([]byte(strings.TrimSpace(body.RefreshToken)))
	stored, ok, err := s.store.GetRefreshTokenByHash(r.Context(), hex.EncodeToString(sum[:]))
	if err != nil || !ok || stored.Revoked || time.Now().UTC().After(stored.ExpiresAt) {
		writeError(w, http.StatusUnauthorized, "invalid_refresh_token", "refresh token is invalid or expired")
		return
	}
	account, found, err := s.auth.GetUser(r.Context(), stored.UserID)
	if err != nil || !found || !account.Active {
		writeError(w, http.StatusUnauthorized, "invalid_refresh_token", "the account is unavailable")
		return
	}
	_ = s.store.RevokeRefreshToken(r.Context(), stored.ID)
	if store.TenantShared() {
		if requested := store.TenantFrom(r.Context()); account.TenantID != requested {
			account.TenantID = requested
		}
	}
	refreshToken, refreshExpires, err := s.issueRefresh(r.Context(), account.ID, clientIP(r), r.UserAgent())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	accessToken, accessExpires, err := s.auth.IssueAccessToken(account)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token": accessToken, "expires_at": accessExpires.Format(time.RFC3339),
		"tenant_id":     account.TenantID,
		"refresh_token": refreshToken, "refresh_expires_at": refreshExpires.Format(time.RFC3339),
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RefreshToken string `json:"refresh_token"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if token := strings.TrimSpace(body.RefreshToken); token != "" {
		sum := sha256.Sum256([]byte(token))
		if stored, ok, err := s.store.GetRefreshTokenByHash(r.Context(), hex.EncodeToString(sum[:])); err == nil && ok {
			_ = s.store.RevokeRefreshToken(r.Context(), stored.ID)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleLogoutAll(w http.ResponseWriter, r *http.Request) {
	user := userOf(r)
	if _, err := s.auth.RevokeUserSessions(r.Context(), user.ID); err != nil {
		s.writeUserError(w, err)
		return
	}
	_ = s.store.RevokeUserRefreshTokens(r.Context(), user.ID)
	s.audit(r, "user.logout_all", user.ID, nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleUserSessions(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.allow(w, r, auth.PermUserAdmin); !ok {
		return
	}
	tokens, err := s.store.ListUserRefreshTokens(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	now := time.Now().UTC()
	items := make([]model.RefreshToken, 0, len(tokens))
	for _, token := range tokens {
		if token.Revoked || now.After(token.ExpiresAt) {
			continue
		}
		items = append(items, token)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleForceLogout(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.allow(w, r, auth.PermUserAdmin); !ok {
		return
	}
	id := r.PathValue("id")
	if id == userOf(r).ID {
		writeError(w, http.StatusBadRequest, "self_protected", "use /api/auth/logout-all for your own sessions")
		return
	}
	if _, err := s.auth.RevokeUserSessions(r.Context(), id); err != nil {
		s.writeUserError(w, err)
		return
	}
	_ = s.store.RevokeUserRefreshTokens(r.Context(), id)
	s.audit(r, "user.logout_forced", id, nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Username string `json:"username"`
		Password string `json:"password"`
		TenantID string `json:"tenant_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	// P27.0: the login body may select the tenant; otherwise the middleware
	// already scoped the request to X-Tenant-ID (default "default").
	if strings.TrimSpace(request.TenantID) != "" {
		ctx, _, ok := s.resolveTenant(w, r, request.TenantID)
		if !ok {
			return
		}
		r = r.WithContext(ctx)
	}
	username := strings.TrimSpace(request.Username)
	key := username + "|" + clientIP(r)
	if retryAfter, locked := s.loginLocked(key); locked {
		w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
		writeError(w, http.StatusTooManyRequests, "too_many_attempts", "too many failed sign-in attempts; retry later")
		return
	}
	token, expires, user, err := s.auth.Login(r.Context(), username, request.Password)
	if err != nil {
		s.recordLoginFailure(key)
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "invalid username or password")
		return
	}
	s.clearLoginFailures(key)
	if store.TenantShared() {
		requested := store.TenantFrom(r.Context())
		if user.TenantID != requested {
			user.TenantID = requested
			reissued, reissuedExpires, issueErr := s.auth.IssueAccessToken(user)
			if issueErr != nil {
				writeError(w, http.StatusInternalServerError, "internal_error", issueErr.Error())
				return
			}
			token, expires = reissued, reissuedExpires
		}
	}
	refreshToken, refreshExpires, refreshErr := s.issueRefresh(r.Context(), user.ID, clientIP(r), r.UserAgent())
	if refreshErr != nil {
		log.Printf("api: issue refresh token: %v", refreshErr)
	}
	response := map[string]any{
		"token":      token,
		"expires_at": expires.Format(time.RFC3339),
		"tenant_id":  user.TenantID,
		"user":       user,
	}
	if refreshToken != "" {
		response["refresh_token"] = refreshToken
		response["refresh_expires_at"] = refreshExpires.Format(time.RFC3339)
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	user := userOf(r)
	if user.TenantID == "" {
		user.TenantID = tenantName(r.Context())
	}
	writeJSON(w, http.StatusOK, user)
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
			s.reEscalateOnce(ctx)
		}
	}
}

func (s *Server) escalateOnce(ctx context.Context) {
	tenants, err := s.store.ListTenants(ctx)
	if err != nil {
		log.Printf("api: list tenants: %v", err)
		return
	}
	for _, tenant := range tenants {
		if !tenant.Active {
			continue
		}
		s.escalateTenant(store.WithTenant(ctx, tenant.ID))
	}
}

func (s *Server) escalateTenant(ctx context.Context) {
	cfg := s.loadEscalationSettings(ctx)
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
		_ = s.store.AddAudit(ctx, model.AuditEntry{
			ID: newID("aud"), At: time.Now().UTC(), Actor: "system",
			Action: "event.escalate", Resource: event.ID,
			Detail: map[string]any{"severity": event.Severity, "after_seconds": cfg.AfterSeconds},
		})
		if s.bus != nil {
			if _, err := s.bus.Publish(bus.KindEventEscalated, event, nil, "escalation"); err != nil {
				log.Printf("api: bus publish escalation: %v", err)
			}
		} else {
			s.hub.Broadcast(realtime.Frame{Type: "event_updated", Data: event})
			go s.deliverNotification(context.Background(), "escalation", event, nil)
		}
		if cfg.AutoDispatch {
			s.autoDispatch(ctx, event)
		}
		if cfg.AutoPatrol {
			s.createEventPatrol(ctx, event)
		}
	}
}

// reEscalateOnce marks already escalated, still-open events for a second alert.
func (s *Server) reEscalateOnce(ctx context.Context) {
	tenants, err := s.store.ListTenants(ctx)
	if err != nil {
		log.Printf("api: list tenants: %v", err)
		return
	}
	for _, tenant := range tenants {
		if !tenant.Active {
			continue
		}
		s.reEscalateTenant(store.WithTenant(ctx, tenant.ID))
	}
}

func (s *Server) reEscalateTenant(ctx context.Context) {
	cfg := s.loadEscalationSettings(ctx)
	if !cfg.Enabled || cfg.SecondAfterSeconds <= 0 || len(cfg.Severities) == 0 {
		return
	}
	before := time.Now().UTC().Add(-time.Duration(cfg.SecondAfterSeconds) * time.Second)
	events, err := s.store.ReEscalateStale(ctx, before, cfg.Severities)
	if err != nil {
		log.Printf("api: re-escalate: %v", err)
		return
	}
	for _, event := range events {
		s.metrics.ReEscalations.Add(1)
		_ = s.store.AddAudit(ctx, model.AuditEntry{
			ID: newID("aud"), At: time.Now().UTC(), Actor: "system",
			Action: "event.re_escalate", Resource: event.ID,
			Detail: map[string]any{"severity": event.Severity, "second_after_seconds": cfg.SecondAfterSeconds},
		})
		if s.bus != nil {
			if _, err := s.bus.Publish(bus.KindEventEscalated, event, nil, "escalation"); err != nil {
				log.Printf("api: bus publish re-escalation: %v", err)
			}
		} else {
			s.hub.Broadcast(realtime.Frame{Type: "event_updated", Data: event})
			go s.deliverNotification(context.Background(), "escalation", event, nil)
		}
		if cfg.AutoDispatch {
			s.autoDispatch(ctx, event)
		}
		if cfg.AutoPatrol {
			s.createEventPatrol(ctx, event)
		}
	}
}

// deliverNotification posts one event/escalation/dispatch to the configured
// webhook, with a silence window and bounded exponential-backoff retries.
func (s *Server) deliverNotification(ctx context.Context, kind string, event model.Event, order *model.Order) {
	cfg := s.loadNotificationSettings(ctx)
	if !cfg.Enabled || strings.TrimSpace(cfg.WebhookURL) == "" {
		return
	}
	if model.SeverityRank(event.Severity) < model.SeverityRank(cfg.MinSeverity) {
		return
	}
	silenceKey := store.TenantFrom(ctx) + "|" + event.CameraID + "|" + event.Type + "|" + event.Severity
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
		s.skipDispatch(ctx, event, camera.AreaID, "no_duty", nil)
		return
	}
	shift, operators := dutyNow(*group, time.Now())
	if len(group.Shifts) > 0 && shift == nil {
		s.skipDispatch(ctx, event, camera.AreaID, "no_duty", map[string]any{"group": group.Name, "reason_detail": "outside_shifts"})
		return
	}
	if existing, ok, err := s.store.FindOpenOrderForEvent(ctx, event.ID); err == nil && ok {
		s.metrics.OrdersDeduped.Add(1)
		s.skipDispatch(ctx, event, camera.AreaID, "open_order", map[string]any{"order_id": existing.ID})
		return
	}
	now := time.Now().UTC()
	assignee := group.Name
	note := "系统自动派单（告警升级）"
	if group.Rotation == "operator" && len(operators) > 0 {
		key := store.TenantFrom(ctx) + "|" + group.ID
		if shift != nil {
			key += "|" + shift.ID
		}
		assignee = operators[s.nextRotation(key, len(operators))]
		if shift != nil {
			note += " · " + group.Name + " · " + shift.Name
		} else {
			note += " · " + group.Name
		}
	}
	order := model.Order{
		ID: newID("ord"), EventID: event.ID, CameraID: event.CameraID,
		Title:     "告警升级 · " + event.Type + " · " + event.CameraCode + " " + event.CameraName,
		Status:    "open",
		Priority:  "urgent",
		Assignee:  assignee,
		Note:      note,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.store.CreateOrder(ctx, order); err != nil {
		log.Printf("api: auto dispatch order: %v", err)
		return
	}
	s.metrics.Orders.Add(1)
	s.metrics.DutyDispatches.Add(1)
	shiftName := ""
	if shift != nil {
		shiftName = shift.Name
	}
	_ = s.store.AddAudit(ctx, model.AuditEntry{
		ID: newID("aud"), At: now, Actor: "system",
		Action: "order.auto_dispatch", Resource: order.ID,
		Detail: map[string]any{
			"event_id": event.ID, "group": group.Name, "area_id": camera.AreaID,
			"shift": shiftName, "rotation": group.Rotation, "assignee": assignee,
		},
	})
	s.hub.Broadcast(realtime.Frame{Type: "order_updated", Data: order})
	go s.deliverNotification(ctx, "dispatch", event, &order)
}

func (s *Server) skipDispatch(ctx context.Context, event model.Event, areaID, reason string, extra map[string]any) {
	s.metrics.DutySkipped.Add(1)
	detail := map[string]any{"area_id": areaID, "reason": reason}
	for key, value := range extra {
		detail[key] = value
	}
	_ = s.store.AddAudit(ctx, model.AuditEntry{
		ID: newID("aud"), At: time.Now().UTC(), Actor: "system",
		Action: "order.auto_dispatch_skipped", Resource: event.ID, Detail: detail,
	})
}

// nextRotation advances a per-key round-robin index.
func (s *Server) nextRotation(key string, size int) int {
	if size <= 0 {
		return 0
	}
	s.rotationMu.Lock()
	defer s.rotationMu.Unlock()
	index := s.rotation[key]
	s.rotation[key] = index + 1
	return index % size
}

// siteLocation is the staffing timezone (Asia/Shanghai by default).
func siteLocation() *time.Location {
	if location, err := time.LoadLocation("Asia/Shanghai"); err == nil {
		return location
	}
	return time.UTC
}

func parseHHMM(value string) (int, bool) {
	parsed, err := time.Parse("15:04", strings.TrimSpace(value))
	if err != nil {
		return 0, false
	}
	return parsed.Hour()*60 + parsed.Minute(), true
}

func containsWeekday(weekdays []int, weekday int) bool {
	for _, value := range weekdays {
		if value == weekday {
			return true
		}
	}
	return false
}

// activeShift finds the shift covering `at` (end <= start means overnight).
func activeShift(shifts []model.DutyShift, at time.Time) *model.DutyShift {
	local := at.In(siteLocation())
	weekday := int(local.Weekday())
	if weekday == 0 {
		weekday = 7
	}
	minutes := local.Hour()*60 + local.Minute()
	for i := range shifts {
		shift := &shifts[i]
		if !containsWeekday(shift.Weekdays, weekday) {
			continue
		}
		start, startOK := parseHHMM(shift.Start)
		end, endOK := parseHHMM(shift.End)
		if !startOK || !endOK {
			continue
		}
		if end == 0 {
			end = 24 * 60
		}
		if start <= end {
			if minutes >= start && minutes < end {
				return shift
			}
		} else if minutes >= start || minutes < end {
			return shift
		}
	}
	return nil
}

// dutyNow resolves the shift and its operators for the group at a moment.
func dutyNow(group model.DutyGroup, at time.Time) (*model.DutyShift, []string) {
	if len(group.Shifts) == 0 {
		return nil, group.Operators
	}
	shift := activeShift(group.Shifts, at)
	if shift == nil {
		return nil, []string{}
	}
	return shift, shift.Operators
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

// AttachBus wires the event bus and routes publishes to display/notification.
func (s *Server) AttachBus(eventBus *bus.Bus) {
	if eventBus == nil {
		return
	}
	s.bus = eventBus
	eventBus.Subscribe(s.onBusEnvelope)
}

func (s *Server) onBusEnvelope(envelope bus.Envelope) {
	if envelope.Source == "replay" {
		return
	}
	ctx := context.Background()
	if envelope.Event.TenantID != "" {
		ctx = store.WithTenant(ctx, envelope.Event.TenantID)
	}
	switch envelope.Kind {
	case bus.KindEventCreated:
		s.hub.Broadcast(realtime.Frame{Type: "event", Data: envelope.Event})
		go s.deliverNotification(ctx, "event", envelope.Event, nil)
	case bus.KindEventEscalated:
		s.hub.Broadcast(realtime.Frame{Type: "event_updated", Data: envelope.Event})
		go s.deliverNotification(ctx, "escalation", envelope.Event, nil)
	}
}

func (s *Server) handleBusStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.allow(w, r, auth.PermAdmin); !ok {
		return
	}
	if s.bus == nil {
		writeError(w, http.StatusServiceUnavailable, "bus_unavailable", "the event bus is not attached")
		return
	}
	writeJSON(w, http.StatusOK, s.bus.Status())
}

func (s *Server) handleBusReplay(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.allow(w, r, auth.PermAdmin); !ok {
		return
	}
	if s.bus == nil {
		writeError(w, http.StatusServiceUnavailable, "bus_unavailable", "the event bus is not attached")
		return
	}
	var body struct {
		FromOffset int64  `json:"from_offset"`
		ToOffset   *int64 `json:"to_offset"`
		Notify     bool   `json:"notify"`
		Limit      int    `json:"limit"`
		AllTenants bool   `json:"all_tenants"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if body.FromOffset < 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "from_offset must be >= 0")
		return
	}
	to := int64(-1)
	if body.ToOffset != nil {
		if *body.ToOffset < body.FromOffset {
			writeError(w, http.StatusBadRequest, "bad_request", "to_offset must be >= from_offset")
			return
		}
		to = *body.ToOffset
	}
	limit := body.Limit
	if limit <= 0 || limit > 500 {
		limit = 500
	}
	replayTenant := store.TenantFrom(r.Context())
	envelopes, err := s.bus.ReadRangeForTenant(body.FromOffset, to, limit, replayTenant, body.AllTenants)
	if err != nil {
		if errors.Is(err, bus.ErrRangeUnavailable) {
			writeError(w, http.StatusConflict, "bus_driver_unsupported", err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	for _, envelope := range envelopes {
		switch envelope.Kind {
		case bus.KindEventCreated:
			s.hub.Broadcast(realtime.Frame{Type: "event_replay", Data: map[string]any{
				"event": envelope.Event, "offset": envelope.Offset, "tenant_id": envelope.TenantID, "replay": true,
			}})
		case bus.KindEventEscalated:
			s.hub.Broadcast(realtime.Frame{Type: "event_updated", Data: envelope.Event})
		}
		if body.Notify {
			notifyCtx := store.WithTenant(context.Background(), envelope.TenantID)
			go s.deliverNotification(notifyCtx, "event", envelope.Event, nil)
		}
	}
	s.bus.MarkReplayed(int64(len(envelopes)))
	items := make([]map[string]any, 0, len(envelopes))
	for _, envelope := range envelopes {
		items = append(items, map[string]any{
			"offset": envelope.Offset, "kind": envelope.Kind, "tenant_id": envelope.TenantID,
			"at": envelope.At, "event_id": envelope.Event.ID,
		})
	}
	s.audit(r, "bus.replay", "bus", map[string]any{
		"from_offset": body.FromOffset, "to_offset": to, "count": len(envelopes),
		"notify": body.Notify, "tenant": replayTenant, "all_tenants": body.AllTenants,
	})
	lastOffset := int64(-1)
	if len(envelopes) > 0 {
		lastOffset = envelopes[len(envelopes)-1].Offset
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "replayed": len(envelopes), "items": items,
		"from_offset": body.FromOffset, "to_offset": lastOffset,
		"tenant_id": replayTenant, "all_tenants": body.AllTenants,
	})
}

// SetSite replaces the served site configuration (config file override).
func (s *Server) SetSite(site model.Site) { s.site = site }

// SetTrackSample configures WebSocket sampling for track points (1 = all).
func (s *Server) SetTrackSample(sample int) {
	if sample < 1 {
		sample = 1
	}
	s.trackSample = sample
}

// SetSessionTTL configures refresh-token lifetime.
func (s *Server) SetSessionTTL(ttl time.Duration) {
	if ttl > 0 {
		s.refreshTTL = ttl
	}
}

// SetPatrolTimeouts configures how long pending/running executions may stay open.
func (s *Server) SetPatrolTimeouts(pending, running time.Duration) {
	s.patrolPendingTimeout = pending
	s.patrolRunningTimeout = running
}

// SetIngestGuard configures the ingest rate limiter and body size cap.
func (s *Server) SetIngestGuard(limiter *RateLimiter, maxBodyBytes int64) {
	s.ingestLimiter = limiter
	s.maxBodyBytes = maxBodyBytes
}

func (s *Server) handleSite(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.site)
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
	if visible, visibleErr := s.cameraVisible(r.Context(), userOf(r), event.CameraID); visibleErr != nil || !visible {
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
	if _, ok := s.allow(w, r, auth.PermAuditRead); !ok {
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

func (s *Server) handleAuditExport(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.allow(w, r, auth.PermAuditRead); !ok {
		return
	}
	filter := model.AuditFilter{
		Action: r.URL.Query().Get("action"),
		Actor:  r.URL.Query().Get("actor"),
		Limit:  parseIntDefault(r.URL.Query().Get("limit"), 5000),
	}
	if filter.Limit > 20000 {
		filter.Limit = 20000
	}
	if raw := r.URL.Query().Get("from"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "from must be RFC3339")
			return
		}
		filter.From = &parsed
	}
	if raw := r.URL.Query().Get("to"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "to must be RFC3339")
			return
		}
		filter.To = &parsed
	}
	entries, err := s.store.ListAudit(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="audit.csv"`)
	writer := csv.NewWriter(w)
	_ = writer.Write([]string{"id", "at", "actor", "action", "resource", "detail"})
	for _, entry := range entries {
		detail, _ := json.Marshal(entry.Detail)
		_ = writer.Write([]string{entry.ID, entry.At.Format(time.RFC3339), entry.Actor, entry.Action, entry.Resource, string(detail)})
	}
	writer.Flush()
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

// maxIngestBody bounds the ingest payload (1 MiB) even when no guard is set.
const maxIngestBody = 1 << 20

func (s *Server) handleIngest(w http.ResponseWriter, r *http.Request) {
	if !s.ingestLimiter.Allow() {
		s.metrics.IngestRejected.Add(1)
		writeError(w, http.StatusTooManyRequests, "rate_limited", "ingest rate limit exceeded")
		return
	}
	limit := s.maxBodyBytes
	if limit <= 0 {
		limit = maxIngestBody
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "request body exceeds the configured limit")
			return
		}
		writeError(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}
	if s.ingestFallbackURL != "" {
		s.forwardIngest(w, r, body)
		return
	}
	s.acceptIngest(w, r, body)
}

// SetRateStore enables cross-replica quota buckets (P31.0). A nil store keeps
// the in-process limiter.
func (s *Server) SetRateStore(rateStore store.RateStore) { s.rateStore = rateStore }

// SetIngestFallback forwards ingest to another API when this replica lacks the
// camera (multi-replica deployments share state through PostgreSQL).
func (s *Server) SetIngestFallback(url string) {
	s.ingestFallbackURL = strings.TrimRight(url, "/")
}

// forwardIngest relays the raw body to the fallback replica.
func (s *Server) forwardIngest(w http.ResponseWriter, r *http.Request, body []byte) {
	request, err := http.NewRequestWithContext(r.Context(), http.MethodPost, s.ingestFallbackURL+"/api/ingest/detections", bytes.NewReader(body))
	if err != nil {
		writeError(w, http.StatusBadGateway, "bad_gateway", "cannot build fallback request")
		return
	}
	request.Header.Set("Content-Type", "application/json")
	if token := r.Header.Get("X-Ingest-Token"); token != "" {
		request.Header.Set("X-Ingest-Token", token)
	}
	client := s.ingestClient
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		s.metrics.IngestFallbackFailures.Add(1)
		writeError(w, http.StatusBadGateway, "fallback_unavailable", "ingest fallback is unreachable")
		return
	}
	defer response.Body.Close()
	s.metrics.IngestForwarded.Add(1)
	for key, values := range response.Header {
		if strings.EqualFold(key, "Content-Type") {
			w.Header()[key] = values
		}
	}
	w.WriteHeader(response.StatusCode)
	_, _ = io.Copy(w, http.MaxBytesReader(w, response.Body, 1<<20))
}

// acceptIngest validates and stores the detections.
func (s *Server) acceptIngest(w http.ResponseWriter, r *http.Request, body []byte) {
	started := time.Now()
	var request model.IngestRequest
	if err := json.Unmarshal(body, &request); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	request.CameraCode = strings.TrimSpace(request.CameraCode)
	if request.CameraCode == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "camera_code is required")
		return
	}
	tenantID := store.TenantFrom(r.Context())
	if limits, ok := s.tenantLimits(r.Context()); ok {
		if !s.allowIngestQPS(tenantID, limits.IngestQPS) {
			s.tenantQuotaExceeded(w, r, "ingest_qps")
			return
		}
		if limit := limits.MaxEvents; limit > 0 {
			if counts, err := s.store.Counts(r.Context()); err == nil && !s.checkCountQuota(w, r, "events", limit, int(counts.EventsTotal)) {
				return
			}
		}
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
	ingestTenant := store.TenantFrom(r.Context())
	for i := range result.Events {
		result.Events[i].TenantID = ingestTenant
	}
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
	s.metrics.TrackWritten.Add(int64(len(points)))
	sample := s.trackSample
	if sample < 1 {
		sample = 1
	}
	for _, point := range points {
		sequence := s.trackSequence.Add(1)
		if sample > 1 && (sequence-1)%uint64(sample) != 0 {
			continue
		}
		s.hub.Broadcast(realtime.Frame{Type: "track_point", Data: point})
		s.metrics.TrackBroadcast.Add(1)
	}
	for _, event := range result.Events {
		if s.bus != nil {
			if _, err := s.bus.Publish(bus.KindEventCreated, event, nil, "ingest"); err != nil {
				log.Printf("api: bus publish: %v", err)
			}
			continue
		}
		s.hub.Broadcast(realtime.Frame{Type: "event", Data: event})
		notifyCtx := store.WithTenant(context.Background(), ingestTenant)
		go s.deliverNotification(notifyCtx, "event", event, nil)
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
	body := s.metrics.Text(s.hub.Clients(), int(counts.OnlineCameras))
	body += fmt.Sprintf(`# HELP dsh_twin_ws_frames_total WebSocket frames queued to clients.
# TYPE dsh_twin_ws_frames_total counter
dsh_twin_ws_frames_total %d
# HELP dsh_twin_ws_dropped_total WebSocket frames dropped because a client queue was full.
# TYPE dsh_twin_ws_dropped_total counter
dsh_twin_ws_dropped_total %d
`, s.hub.Frames(), s.hub.Dropped())
	if s.bus != nil {
		status := s.bus.Status()
		lag := status.NextOffset
		envelopes, err := s.bus.ReadRange(status.OldestOffset, -1, 1)
		if err == nil && len(envelopes) > 0 {
			lag = status.NextOffset - envelopes[len(envelopes)-1].Offset - 1
		}
		body += s.bus.MetricsText(lag)
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(body))
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
	if _, cameraErr := s.store.GetCamera(r.Context(), cameraID); cameraErr != nil {
		s.writeCameraError(w, cameraErr)
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
	if _, cameraErr := s.store.GetCamera(r.Context(), cameraID); cameraErr != nil {
		s.writeCameraError(w, cameraErr)
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
		EventID:  r.URL.Query().Get("event_id"),
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
	if store.IsNotFound(err) {
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
	writeJSON(w, http.StatusOK, s.loadNotificationSettings(r.Context()))
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
	writeJSON(w, http.StatusOK, s.loadEscalationSettings(r.Context()))
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
	if cfg.SecondAfterSeconds < 0 || cfg.SecondAfterSeconds > 604800 {
		writeError(w, http.StatusBadRequest, "bad_request", "second_after_seconds must be within [0,604800]")
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
		"enabled": cfg.Enabled, "after_seconds": cfg.AfterSeconds, "auto_patrol": cfg.AutoPatrol,
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
	if _, ok := s.allow(w, r, auth.PermUserAdmin); !ok {
		return
	}
	users, err := s.auth.Users(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	if users == nil {
		users = []model.UserAccount{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": users})
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	if tenant, ok, err := s.store.GetTenant(r.Context(), store.TenantFrom(r.Context())); err == nil && ok {
		if limit := tenant.Limits.MaxUsers; limit > 0 {
			if existing, err := s.auth.Users(r.Context()); err == nil && !s.checkCountQuota(w, r, "users", limit, len(existing)) {
				return
			}
		}
	}
	if _, ok := s.allow(w, r, auth.PermUserAdmin); !ok {
		return
	}
	var body struct {
		Username    string   `json:"username"`
		Password    string   `json:"password"`
		DisplayName string   `json:"display_name"`
		Role        string   `json:"role"`
		Areas       []string `json:"areas"`
		Active      *bool    `json:"active"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if err := s.validateUserAreas(r.Context(), body.Areas); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	active := true
	if body.Active != nil {
		active = *body.Active
	}
	account, err := s.auth.CreateUser(r.Context(), auth.CreateUserInput{
		Username: body.Username, Password: body.Password, DisplayName: body.DisplayName,
		Role: body.Role, Areas: body.Areas, Active: active,
	})
	if err != nil {
		s.writeUserError(w, err)
		return
	}
	s.audit(r, "user.create", account.ID, map[string]any{"username": account.Username, "role": account.Role})
	writeJSON(w, http.StatusCreated, account)
}

func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.allow(w, r, auth.PermUserAdmin); !ok {
		return
	}
	current := userOf(r)
	id := r.PathValue("id")
	var body struct {
		DisplayName *string   `json:"display_name"`
		Password    *string   `json:"password"`
		Role        *string   `json:"role"`
		Areas       *[]string `json:"areas"`
		Active      *bool     `json:"active"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if id == current.ID && body.Active != nil && !*body.Active {
		writeError(w, http.StatusBadRequest, "self_protected", "you cannot deactivate your own account")
		return
	}
	if id == current.ID && body.Role != nil && *body.Role != current.Role {
		writeError(w, http.StatusBadRequest, "self_protected", "you cannot change your own role")
		return
	}
	if body.Areas != nil {
		if err := s.validateUserAreas(r.Context(), *body.Areas); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
	}
	account, err := s.auth.UpdateUser(r.Context(), id, auth.UpdateUserInput{
		DisplayName: body.DisplayName, Password: body.Password, Role: body.Role,
		Areas: body.Areas, Active: body.Active,
	})
	if err != nil {
		s.writeUserError(w, err)
		return
	}
	if body.Password != nil {
		_ = s.store.RevokeUserRefreshTokens(r.Context(), account.ID)
	}
	s.audit(r, "user.update", account.ID, map[string]any{"role": account.Role, "active": account.Active, "password_reset": body.Password != nil})
	writeJSON(w, http.StatusOK, account)
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.allow(w, r, auth.PermUserAdmin); !ok {
		return
	}
	id := r.PathValue("id")
	if id == userOf(r).ID {
		writeError(w, http.StatusBadRequest, "self_protected", "you cannot delete your own account")
		return
	}
	if err := s.auth.DeleteUser(r.Context(), id); err != nil {
		s.writeUserError(w, err)
		return
	}
	s.audit(r, "user.delete", id, nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) validateUserAreas(ctx context.Context, areas []string) error {
	if len(areas) == 0 {
		return nil
	}
	known, err := s.store.ListAreas(ctx)
	if err != nil {
		return err
	}
	valid := make(map[string]struct{}, len(known))
	for _, area := range known {
		valid[area.ID] = struct{}{}
	}
	for _, area := range areas {
		if _, ok := valid[area]; !ok {
			return fmt.Errorf("unknown area %q", area)
		}
	}
	return nil
}

func (s *Server) writeUserError(w http.ResponseWriter, err error) {
	switch {
	case store.IsConflict(err):
		writeError(w, http.StatusConflict, "username_taken", "username already exists")
	case store.IsNotFound(err):
		writeError(w, http.StatusNotFound, "user_not_found", "user not found")
	case errors.Is(err, auth.ErrBadInput):
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
	}
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
	Name      string             `json:"name"`
	Areas     *[]string          `json:"areas,omitempty"`
	Operators *[]string          `json:"operators,omitempty"`
	Rotation  *string            `json:"rotation,omitempty"`
	Shifts    *[]model.DutyShift `json:"shifts,omitempty"`
	Active    *bool              `json:"active,omitempty"`
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
	rotation := "group"
	if body.Rotation != nil {
		rotation = *body.Rotation
	}
	if rotation != "group" && rotation != "operator" {
		writeError(w, http.StatusBadRequest, "bad_request", "rotation must be group or operator")
		return
	}
	shifts := []model.DutyShift{}
	if body.Shifts != nil {
		shifts = *body.Shifts
	}
	if err := s.validateDutyShifts(r.Context(), shifts); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	for i := range shifts {
		if strings.TrimSpace(shifts[i].ID) == "" {
			shifts[i].ID = newID("shift")
		}
	}
	active := true
	if body.Active != nil {
		active = *body.Active
	}
	now := time.Now().UTC()
	group := model.DutyGroup{
		ID: newID("duty"), Name: body.Name, Active: active, Rotation: rotation, Shifts: shifts,
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
	if decodeErr := json.NewDecoder(r.Body).Decode(&body); decodeErr != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if validateErr := s.validateDutyGroups(r.Context(), body.Areas, body.Operators); validateErr != nil {
		writeError(w, http.StatusBadRequest, "bad_request", validateErr.Error())
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
	if body.Rotation != nil {
		if *body.Rotation != "group" && *body.Rotation != "operator" {
			writeError(w, http.StatusBadRequest, "bad_request", "rotation must be group or operator")
			return
		}
		existing.Rotation = *body.Rotation
	}
	if body.Shifts != nil {
		shifts := *body.Shifts
		if shiftErr := s.validateDutyShifts(r.Context(), shifts); shiftErr != nil {
			writeError(w, http.StatusBadRequest, "bad_request", shiftErr.Error())
			return
		}
		for i := range shifts {
			if strings.TrimSpace(shifts[i].ID) == "" {
				shifts[i].ID = newID("shift")
			}
		}
		existing.Shifts = shifts
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
	at := time.Now()
	if raw := r.URL.Query().Get("at"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "at must be RFC3339")
			return
		}
		at = parsed
	}
	group := s.resolveDutyGroup(r.Context(), areaID)
	if group == nil {
		writeJSON(w, http.StatusOK, map[string]any{"area_id": areaID, "group": nil, "shift": nil, "operators": []string{}})
		return
	}
	shift, operators := dutyNow(*group, at)
	writeJSON(w, http.StatusOK, map[string]any{
		"area_id": areaID, "group": group, "shift": shift, "operators": operators,
	})
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
			exists, err := s.auth.UserExists(ctx, username)
			if err != nil {
				return err
			}
			if !exists {
				return fmt.Errorf("unknown operator %q", username)
			}
		}
	}
	return nil
}

func (s *Server) validateDutyShifts(ctx context.Context, shifts []model.DutyShift) error {
	if len(shifts) > 20 {
		return errors.New("at most 20 shifts are accepted")
	}
	for _, shift := range shifts {
		if strings.TrimSpace(shift.Name) == "" {
			return errors.New("shift name is required")
		}
		if _, ok := parseHHMM(shift.Start); !ok {
			return fmt.Errorf("shift %q has invalid start (HH:MM)", shift.Name)
		}
		if _, ok := parseHHMM(shift.End); !ok {
			return fmt.Errorf("shift %q has invalid end (HH:MM)", shift.Name)
		}
		if len(shift.Weekdays) == 0 {
			return fmt.Errorf("shift %q needs at least one weekday", shift.Name)
		}
		for _, weekday := range shift.Weekdays {
			if weekday < 1 || weekday > 7 {
				return fmt.Errorf("shift %q weekday must be within 1..7", shift.Name)
			}
		}
		for _, username := range shift.Operators {
			exists, err := s.auth.UserExists(ctx, username)
			if err != nil {
				return err
			}
			if !exists {
				return fmt.Errorf("unknown operator %q", username)
			}
		}
	}
	return nil
}

func (s *Server) writeDutyError(w http.ResponseWriter, err error) {
	if store.IsNotFound(err) {
		writeError(w, http.StatusNotFound, "duty_not_found", "duty group not found")
		return
	}
	writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
}

type patrolBody struct {
	Name            string            `json:"name"`
	Waypoints       *[]model.Waypoint `json:"waypoints"`
	CameraIDs       *[]string         `json:"camera_ids"`
	IntervalSeconds *int              `json:"interval_seconds"`
}

type patrolScheduleBody struct {
	Name            string `json:"name"`
	RouteID         string `json:"route_id"`
	IntervalMinutes *int   `json:"interval_minutes"`
	Enabled         *bool  `json:"enabled"`
}

func (s *Server) handleListPatrolSchedules(w http.ResponseWriter, r *http.Request) {
	schedules, err := s.store.ListPatrolSchedules(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	if schedules == nil {
		schedules = []model.PatrolSchedule{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": schedules})
}

func (s *Server) handleCreatePatrolSchedule(w http.ResponseWriter, r *http.Request) {
	user, ok := s.allow(w, r, auth.PermPatrolWrite)
	if !ok {
		return
	}
	var body patrolScheduleBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	schedule, err := s.validateScheduleBody(r, body, nil)
	if err != nil {
		s.writePatrolError(w, err)
		return
	}
	now := time.Now().UTC()
	schedule.ID = newID("psc")
	schedule.CreatedBy = user.Username
	schedule.CreatedAt = now
	schedule.UpdatedAt = now
	schedule.NextRunAt = now.Add(time.Duration(schedule.IntervalMinutes) * time.Minute)
	if err := s.store.CreatePatrolSchedule(r.Context(), *schedule); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	s.audit(r, "patrol.schedule.create", schedule.ID, map[string]any{"name": schedule.Name, "route_id": schedule.RouteID})
	writeJSON(w, http.StatusCreated, schedule)
}

func (s *Server) handleUpdatePatrolSchedule(w http.ResponseWriter, r *http.Request) {
	user, ok := s.allow(w, r, auth.PermPatrolWrite)
	if !ok {
		return
	}
	existing, found, err := s.store.GetPatrolSchedule(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "patrol_schedule_not_found", "patrol schedule not found")
		return
	}
	if user.Role != auth.RoleAdmin && existing.CreatedBy != user.Username {
		writeError(w, http.StatusForbidden, "forbidden", "only the creator or an admin can modify this schedule")
		return
	}
	var body patrolScheduleBody
	if decodeErr := json.NewDecoder(r.Body).Decode(&body); decodeErr != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	updated, err := s.validateScheduleBody(r, body, &existing)
	if err != nil {
		s.writePatrolError(w, err)
		return
	}
	updated.ID = existing.ID
	updated.CreatedBy = existing.CreatedBy
	updated.CreatedAt = existing.CreatedAt
	updated.UpdatedAt = time.Now().UTC()
	updated.NextRunAt = existing.NextRunAt
	if body.IntervalMinutes != nil || body.RouteID != "" {
		updated.NextRunAt = time.Now().UTC().Add(time.Duration(updated.IntervalMinutes) * time.Minute)
	}
	if err := s.store.UpdatePatrolSchedule(r.Context(), *updated); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	s.audit(r, "patrol.schedule.update", updated.ID, map[string]any{"name": updated.Name})
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleDeletePatrolSchedule(w http.ResponseWriter, r *http.Request) {
	user, ok := s.allow(w, r, auth.PermPatrolWrite)
	if !ok {
		return
	}
	existing, found, err := s.store.GetPatrolSchedule(r.Context(), r.PathValue("id"))
	if err != nil || !found {
		writeError(w, http.StatusNotFound, "patrol_schedule_not_found", "patrol schedule not found")
		return
	}
	if user.Role != auth.RoleAdmin && existing.CreatedBy != user.Username {
		writeError(w, http.StatusForbidden, "forbidden", "only the creator or an admin can delete this schedule")
		return
	}
	if err := s.store.DeletePatrolSchedule(r.Context(), existing.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	s.audit(r, "patrol.schedule.delete", existing.ID, nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRunPatrolSchedule(w http.ResponseWriter, r *http.Request) {
	user, ok := s.allow(w, r, auth.PermPatrolWrite)
	if !ok {
		return
	}
	schedule, found, err := s.store.GetPatrolSchedule(r.Context(), r.PathValue("id"))
	if err != nil || !found {
		writeError(w, http.StatusNotFound, "patrol_schedule_not_found", "patrol schedule not found")
		return
	}
	execution, err := s.createPatrolExecution(r.Context(), schedule, time.Now().UTC(), "manual")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	s.audit(r, "patrol.schedule.run", schedule.ID, map[string]any{"execution_id": execution.ID, "actor": user.Username})
	writeJSON(w, http.StatusCreated, execution)
}

func (s *Server) handleListPatrolExecutions(w http.ResponseWriter, r *http.Request) {
	filter := model.PatrolExecutionFilter{
		Status:     r.URL.Query().Get("status"),
		ScheduleID: r.URL.Query().Get("schedule_id"),
		EventID:    r.URL.Query().Get("event_id"),
		Limit:      parseIntDefault(r.URL.Query().Get("limit"), 100),
	}
	if filter.Limit > 500 {
		filter.Limit = 500
	}
	executions, err := s.store.ListPatrolExecutions(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	if executions == nil {
		executions = []model.PatrolExecution{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": executions})
}

func (s *Server) handleClaimPatrolExecution(w http.ResponseWriter, r *http.Request) {
	user, ok := s.allow(w, r, auth.PermPatrolWrite)
	if !ok {
		return
	}
	execution, found, err := s.store.GetPatrolExecution(r.Context(), r.PathValue("id"))
	if err != nil || !found {
		writeError(w, http.StatusNotFound, "patrol_execution_not_found", "patrol execution not found")
		return
	}
	if execution.Status != "pending" {
		writeError(w, http.StatusConflict, "invalid_state", "only pending executions can be claimed")
		return
	}
	now := time.Now().UTC()
	execution.Status = "running"
	execution.Assignee = user.Username
	execution.StartedAt = &now
	if err := s.store.UpdatePatrolExecution(r.Context(), execution); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	s.audit(r, "patrol.execution.claim", execution.ID, nil)
	writeJSON(w, http.StatusOK, execution)
}

func (s *Server) handleCompletePatrolExecution(w http.ResponseWriter, r *http.Request) {
	user, ok := s.allow(w, r, auth.PermPatrolWrite)
	if !ok {
		return
	}
	execution, found, err := s.store.GetPatrolExecution(r.Context(), r.PathValue("id"))
	if err != nil || !found {
		writeError(w, http.StatusNotFound, "patrol_execution_not_found", "patrol execution not found")
		return
	}
	if execution.Status != "running" {
		writeError(w, http.StatusConflict, "invalid_state", "only running executions can be completed")
		return
	}
	if user.Role != auth.RoleAdmin && execution.Assignee != user.Username {
		writeError(w, http.StatusForbidden, "forbidden", "only the assignee or an admin can complete this execution")
		return
	}
	var body struct {
		Result string `json:"result"`
		Note   string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if body.Result != "done" && body.Result != "issue" {
		writeError(w, http.StatusBadRequest, "bad_request", "result must be done or issue")
		return
	}
	now := time.Now().UTC()
	execution.Status = body.Result
	execution.Result = body.Result
	execution.Note = strings.TrimSpace(body.Note)
	execution.FinishedAt = &now
	if body.Result == "issue" {
		routeName := execution.RouteName
		cameraID := ""
		if route, ok, _ := s.store.GetPatrolRoute(r.Context(), execution.RouteID); ok {
			if routeName == "" {
				routeName = route.Name
			}
			if len(route.CameraIDs) > 0 {
				cameraID = route.CameraIDs[0]
			}
		}
		order := model.Order{
			ID: newID("ord"), EventID: "", CameraID: cameraID,
			Title:     "巡检异常 · " + routeName,
			Status:    "open",
			Priority:  "normal",
			Assignee:  user.Username,
			Note:      fmt.Sprintf("巡检发现异常（执行 %s）：%s", execution.ID, execution.Note),
			CreatedAt: now,
			UpdatedAt: now,
		}
		if err := s.store.CreateOrder(r.Context(), order); err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		s.metrics.Orders.Add(1)
		execution.OrderID = order.ID
		s.hub.Broadcast(realtime.Frame{Type: "order_updated", Data: order})
		s.audit(r, "order.created", order.ID, map[string]any{"source": "patrol", "execution_id": execution.ID})
	}
	if err := s.store.UpdatePatrolExecution(r.Context(), execution); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	s.audit(r, "patrol.execution.complete", execution.ID, map[string]any{"result": body.Result, "order_id": execution.OrderID})
	writeJSON(w, http.StatusOK, execution)
}

func (s *Server) validateScheduleBody(r *http.Request, body patrolScheduleBody, existing *model.PatrolSchedule) (*model.PatrolSchedule, error) {
	schedule := model.PatrolSchedule{IntervalMinutes: 60, Enabled: true}
	if existing != nil {
		schedule = *existing
	}
	if name := strings.TrimSpace(body.Name); name != "" {
		schedule.Name = name
	}
	if schedule.Name == "" {
		return nil, errors.New("name is required")
	}
	if body.RouteID != "" {
		schedule.RouteID = body.RouteID
	}
	if schedule.RouteID == "" {
		return nil, errors.New("route_id is required")
	}
	if _, found, err := s.store.GetPatrolRoute(r.Context(), schedule.RouteID); err != nil || !found {
		return nil, errors.New("unknown route_id")
	}
	if body.IntervalMinutes != nil {
		schedule.IntervalMinutes = *body.IntervalMinutes
	}
	if schedule.IntervalMinutes < 1 || schedule.IntervalMinutes > 10080 {
		return nil, errors.New("interval_minutes must be within [1,10080]")
	}
	if body.Enabled != nil {
		schedule.Enabled = *body.Enabled
	}
	return &schedule, nil
}

// createPatrolExecution records one patrol run and notifies connected clients.
func (s *Server) createPatrolExecution(ctx context.Context, schedule model.PatrolSchedule, scheduledAt time.Time, source string) (model.PatrolExecution, error) {
	now := time.Now().UTC()
	routeName := ""
	if route, ok, _ := s.store.GetPatrolRoute(ctx, schedule.RouteID); ok {
		routeName = route.Name
	}
	execution := model.PatrolExecution{
		ID: newID("pex"), ScheduleID: schedule.ID, RouteID: schedule.RouteID,
		RouteName: routeName, Status: "pending", ScheduledAt: scheduledAt, CreatedAt: now,
	}
	if err := s.store.CreatePatrolExecution(ctx, execution); err != nil {
		return model.PatrolExecution{}, err
	}
	_ = s.store.AddAudit(ctx, model.AuditEntry{
		ID: newID("aud"), At: now, Actor: "system",
		Action: "patrol.execution.created", Resource: execution.ID,
		Detail: map[string]any{"schedule_id": schedule.ID, "source": source},
	})
	s.hub.Broadcast(realtime.Frame{Type: "patrol_execution", Data: execution})
	return execution, nil
}

// RunPatrolScheduler scans due schedules on a fixed interval.
func (s *Server) RunPatrolScheduler(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.RunPatrolOnce(ctx)
		}
	}
}

// RunPatrolOnce materializes executions for every due schedule.
func (s *Server) RunPatrolOnce(ctx context.Context) {
	s.expirePatrolExecutions(ctx)
	schedules, err := s.store.ListPatrolSchedules(ctx)
	if err != nil {
		log.Printf("api: patrol schedule scan: %v", err)
		return
	}
	now := time.Now().UTC()
	for _, schedule := range schedules {
		if !schedule.Enabled || schedule.NextRunAt.After(now) {
			continue
		}
		if _, err := s.createPatrolExecution(ctx, schedule, schedule.NextRunAt, "schedule"); err != nil {
			log.Printf("api: patrol execution create: %v", err)
			continue
		}
		schedule.NextRunAt = now.Add(time.Duration(schedule.IntervalMinutes) * time.Minute)
		schedule.UpdatedAt = now
		if err := s.store.UpdatePatrolSchedule(ctx, schedule); err != nil {
			log.Printf("api: patrol schedule advance: %v", err)
		}
	}
}

func (s *Server) handleCancelPatrolExecution(w http.ResponseWriter, r *http.Request) {
	user, ok := s.allow(w, r, auth.PermPatrolWrite)
	if !ok {
		return
	}
	execution, found, err := s.store.GetPatrolExecution(r.Context(), r.PathValue("id"))
	if err != nil || !found {
		writeError(w, http.StatusNotFound, "patrol_execution_not_found", "patrol execution not found")
		return
	}
	if execution.Status != "pending" && execution.Status != "running" {
		writeError(w, http.StatusConflict, "invalid_state", "only pending or running executions can be cancelled")
		return
	}
	if execution.Status == "running" && user.Role != auth.RoleAdmin && execution.Assignee != user.Username {
		writeError(w, http.StatusForbidden, "forbidden", "only the assignee or an admin can cancel a running execution")
		return
	}
	var body struct {
		Note string `json:"note"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	note := strings.TrimSpace(body.Note)
	if note == "" {
		note = "人工取消"
	}
	if err := s.closePatrolExecution(r.Context(), execution, "cancelled", note, "patrol.execution.cancel", user.Username); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	if updated, ok, err := s.store.GetPatrolExecution(r.Context(), execution.ID); err == nil && ok {
		execution = updated
	}
	writeJSON(w, http.StatusOK, execution)
}

// closePatrolExecution moves an open execution to cancelled and emits audit/WS.
func (s *Server) closePatrolExecution(ctx context.Context, execution model.PatrolExecution, status, note, action, actor string) error {
	now := time.Now().UTC()
	execution.Status = status
	execution.Result = status
	execution.Note = note
	execution.FinishedAt = &now
	if err := s.store.UpdatePatrolExecution(ctx, execution); err != nil {
		return err
	}
	_ = s.store.AddAudit(ctx, model.AuditEntry{
		ID: newID("aud"), At: now, Actor: actor, Action: action, Resource: execution.ID,
		Detail: map[string]any{"status": status, "note": note},
	})
	s.hub.Broadcast(realtime.Frame{Type: "patrol_execution", Data: execution})
	return nil
}

// expirePatrolExecutions closes stale pending/running tasks.
func (s *Server) expirePatrolExecutions(ctx context.Context) {
	now := time.Now().UTC()
	if s.patrolPendingTimeout > 0 {
		executions, err := s.store.ListPatrolExecutions(ctx, model.PatrolExecutionFilter{Status: "pending", Limit: 10000})
		if err == nil {
			for _, execution := range executions {
				if now.Sub(execution.CreatedAt) < s.patrolPendingTimeout {
					continue
				}
				note := "超时未认领，自动关闭"
				if err := s.closePatrolExecution(ctx, execution, "cancelled", note, "patrol.execution.expired", "system"); err != nil {
					log.Printf("api: expire patrol execution: %v", err)
					continue
				}
			}
		}
	}
	if s.patrolRunningTimeout > 0 {
		executions, err := s.store.ListPatrolExecutions(ctx, model.PatrolExecutionFilter{Status: "running", Limit: 10000})
		if err == nil {
			for _, execution := range executions {
				if execution.StartedAt == nil || now.Sub(*execution.StartedAt) < s.patrolRunningTimeout {
					continue
				}
				note := "超时未完成，自动关闭"
				if err := s.closePatrolExecution(ctx, execution, "cancelled", note, "patrol.execution.expired", "system"); err != nil {
					log.Printf("api: expire patrol execution: %v", err)
					continue
				}
			}
		}
	}
}

func parsePatrolRange(r *http.Request) (*time.Time, *time.Time, error) {
	var from, to *time.Time
	if raw := r.URL.Query().Get("from"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return nil, nil, errors.New("from must be RFC3339")
		}
		from = &parsed
	}
	if raw := r.URL.Query().Get("to"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return nil, nil, errors.New("to must be RFC3339")
		}
		to = &parsed
	}
	return from, to, nil
}

func (s *Server) handlePatrolStats(w http.ResponseWriter, r *http.Request) {
	from, to, err := parsePatrolRange(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	executions, err := s.store.ListPatrolExecutions(r.Context(), model.PatrolExecutionFilter{
		From: from, To: to, Limit: 10000,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	type routeStat struct {
		RouteID   string `json:"route_id"`
		RouteName string `json:"route_name"`
		Total     int    `json:"total"`
		Done      int    `json:"done"`
		Issue     int    `json:"issue"`
	}
	type assigneeStat struct {
		Assignee string `json:"assignee"`
		Total    int    `json:"total"`
		Done     int    `json:"done"`
		Issue    int    `json:"issue"`
	}
	counts := map[string]int{}
	routes := map[string]*routeStat{}
	assignees := map[string]*assigneeStat{}
	durationSum := 0.0
	durationCount := 0
	for _, execution := range executions {
		counts[execution.Status]++
		routeKey := execution.RouteID
		route, ok := routes[routeKey]
		if !ok {
			route = &routeStat{RouteID: execution.RouteID, RouteName: execution.RouteName}
			routes[routeKey] = route
		}
		route.Total++
		switch execution.Status {
		case "done":
			route.Done++
		case "issue":
			route.Issue++
		}
		if execution.Assignee != "" {
			stat, ok := assignees[execution.Assignee]
			if !ok {
				stat = &assigneeStat{Assignee: execution.Assignee}
				assignees[execution.Assignee] = stat
			}
			stat.Total++
			switch execution.Status {
			case "done":
				stat.Done++
			case "issue":
				stat.Issue++
			}
		}
		if execution.StartedAt != nil && execution.FinishedAt != nil {
			durationSum += execution.FinishedAt.Sub(*execution.StartedAt).Seconds()
			durationCount++
		}
	}
	total := len(executions)
	rate := func(part int) float64 {
		if total == 0 {
			return 0
		}
		return math.Round(float64(part)/float64(total)*1000) / 1000
	}
	average := 0.0
	if durationCount > 0 {
		average = math.Round(durationSum/float64(durationCount)*10) / 10
	}
	routeList := make([]routeStat, 0, len(routes))
	for _, route := range routes {
		routeList = append(routeList, *route)
	}
	sort.Slice(routeList, func(i, j int) bool {
		if routeList[i].Total != routeList[j].Total {
			return routeList[i].Total > routeList[j].Total
		}
		return routeList[i].RouteID < routeList[j].RouteID
	})
	assigneeList := make([]assigneeStat, 0, len(assignees))
	for _, stat := range assignees {
		assigneeList = append(assigneeList, *stat)
	}
	sort.Slice(assigneeList, func(i, j int) bool {
		if assigneeList[i].Total != assigneeList[j].Total {
			return assigneeList[i].Total > assigneeList[j].Total
		}
		return assigneeList[i].Assignee < assigneeList[j].Assignee
	})
	response := map[string]any{
		"executions_total":     total,
		"pending":              counts["pending"],
		"running":              counts["running"],
		"done":                 counts["done"],
		"issue":                counts["issue"],
		"cancelled":            counts["cancelled"],
		"completion_rate":      rate(counts["done"]),
		"issue_rate":           rate(counts["issue"]),
		"avg_duration_seconds": average,
		"by_route":             routeList,
		"by_assignee":          assigneeList,
	}
	if from != nil {
		response["from"] = from.Format(time.RFC3339)
	}
	if to != nil {
		response["to"] = to.Format(time.RFC3339)
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handlePatrolExecutionsExport(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.allow(w, r, auth.PermPatrolWrite); !ok {
		return
	}
	from, to, err := parsePatrolRange(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	executions, err := s.store.ListPatrolExecutions(r.Context(), model.PatrolExecutionFilter{
		Status: r.URL.Query().Get("status"), From: from, To: to, Limit: 10000,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="patrol-executions.csv"`)
	writer := csv.NewWriter(w)
	_ = writer.Write([]string{"id", "schedule_id", "route_name", "status", "assignee", "result", "note", "scheduled_at", "started_at", "finished_at", "order_id"})
	for _, execution := range executions {
		started, finished := "", ""
		if execution.StartedAt != nil {
			started = execution.StartedAt.Format(time.RFC3339)
		}
		if execution.FinishedAt != nil {
			finished = execution.FinishedAt.Format(time.RFC3339)
		}
		_ = writer.Write([]string{
			execution.ID, execution.ScheduleID, execution.RouteName, execution.Status,
			execution.Assignee, execution.Result, execution.Note,
			execution.ScheduledAt.Format(time.RFC3339), started, finished, execution.OrderID,
		})
	}
	writer.Flush()
}

func (s *Server) handlePurgePatrolExecutions(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.allow(w, r, auth.PermAdmin); !ok {
		return
	}
	before := time.Now().UTC()
	if raw := r.URL.Query().Get("before"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "before must be RFC3339")
			return
		}
		before = parsed
	}
	statuses := []string{"done", "issue"}
	if raw := strings.TrimSpace(r.URL.Query().Get("status")); raw != "" {
		statuses = []string{}
		for _, part := range strings.Split(raw, ",") {
			status := strings.TrimSpace(part)
			if status != "done" && status != "issue" && status != "cancelled" {
				writeError(w, http.StatusBadRequest, "bad_request", "status may only contain done, issue or cancelled")
				return
			}
			statuses = append(statuses, status)
		}
	}
	deleted, err := s.store.DeletePatrolExecutions(r.Context(), before, statuses)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	s.audit(r, "patrol.executions.purge", "patrol_executions", map[string]any{
		"before": before.Format(time.RFC3339), "statuses": statuses, "deleted": deleted,
	})
	writeJSON(w, http.StatusOK, map[string]any{"deleted": deleted})
}

// createEventPatrol materializes a patrol task for an escalated event.
func (s *Server) createEventPatrol(ctx context.Context, event model.Event) {
	executions, err := s.store.ListPatrolExecutions(ctx, model.PatrolExecutionFilter{EventID: event.ID, Limit: 100})
	if err == nil {
		for _, execution := range executions {
			if execution.Status == "pending" || execution.Status == "running" {
				_ = s.store.AddAudit(ctx, model.AuditEntry{
					ID: newID("aud"), At: time.Now().UTC(), Actor: "system",
					Action: "patrol.execution.skipped", Resource: event.ID,
					Detail: map[string]any{"reason": "duplicate", "execution_id": execution.ID},
				})
				return
			}
		}
	}
	routes, err := s.store.ListPatrolRoutes(ctx)
	if err != nil || len(routes) == 0 {
		_ = s.store.AddAudit(ctx, model.AuditEntry{
			ID: newID("aud"), At: time.Now().UTC(), Actor: "system",
			Action: "patrol.execution.skipped", Resource: event.ID,
			Detail: map[string]any{"reason": "no_route"},
		})
		return
	}
	camera, _ := s.store.GetCamera(ctx, event.CameraID)
	route := matchPatrolRoute(routes, camera, event.CameraID)
	if route == nil {
		return
	}
	now := time.Now().UTC()
	execution := model.PatrolExecution{
		ID: newID("pex"), RouteID: route.ID, RouteName: route.Name,
		EventID: event.ID, Status: "pending",
		Note:        fmt.Sprintf("告警联动：%s · %s %s", event.Type, event.CameraCode, event.CameraName),
		ScheduledAt: now, CreatedAt: now,
	}
	if err := s.store.CreatePatrolExecution(ctx, execution); err != nil {
		log.Printf("api: create event patrol: %v", err)
		return
	}
	_ = s.store.AddAudit(ctx, model.AuditEntry{
		ID: newID("aud"), At: now, Actor: "system",
		Action: "patrol.execution.created", Resource: execution.ID,
		Detail: map[string]any{"source": "event", "event_id": event.ID, "route_id": route.ID},
	})
	s.hub.Broadcast(realtime.Frame{Type: "patrol_execution", Data: execution})
}

// matchPatrolRoute prefers routes covering the camera, then the nearest waypoint.
func matchPatrolRoute(routes []model.PatrolRoute, camera model.Camera, cameraID string) *model.PatrolRoute {
	var containing *model.PatrolRoute
	latest := time.Time{}
	for index := range routes {
		for _, id := range routes[index].CameraIDs {
			if id != cameraID {
				continue
			}
			if containing == nil || routes[index].UpdatedAt.After(latest) {
				latest = routes[index].UpdatedAt
				containing = &routes[index]
			}
		}
	}
	if containing != nil {
		return containing
	}
	var best *model.PatrolRoute
	bestDistance := math.Inf(1)
	for index := range routes {
		for _, waypoint := range routes[index].Waypoints {
			dx := waypoint.X - camera.X
			dz := waypoint.Z - camera.Z
			distance := dx*dx + dz*dz
			if distance < bestDistance {
				bestDistance = distance
				best = &routes[index]
			}
		}
	}
	return best
}

func (s *Server) writePatrolError(w http.ResponseWriter, err error) {
	if errors.Is(err, errUnknownCamera) {
		writeError(w, http.StatusBadRequest, "unknown_camera", err.Error())
		return
	}
	writeError(w, http.StatusBadRequest, "bad_request", err.Error())
}

func (s *Server) handleListPatrolRoutes(w http.ResponseWriter, r *http.Request) {
	routes, err := s.store.ListPatrolRoutes(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	if routes == nil {
		routes = []model.PatrolRoute{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": routes})
}

func (s *Server) handleCreatePatrolRoute(w http.ResponseWriter, r *http.Request) {
	user, ok := s.allow(w, r, auth.PermPatrolWrite)
	if !ok {
		return
	}
	var body patrolBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	route, err := s.validatePatrolBody(r, body, nil)
	if err != nil {
		s.writePatrolError(w, err)
		return
	}
	now := time.Now().UTC()
	route.ID = newID("patrol")
	route.CreatedBy = user.Username
	route.CreatedAt = now
	route.UpdatedAt = now
	if err := s.store.CreatePatrolRoute(r.Context(), *route); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	s.audit(r, "patrol.create", route.ID, map[string]any{"name": route.Name, "waypoints": len(route.Waypoints)})
	writeJSON(w, http.StatusCreated, route)
}

func (s *Server) handleUpdatePatrolRoute(w http.ResponseWriter, r *http.Request) {
	user, ok := s.allow(w, r, auth.PermPatrolWrite)
	if !ok {
		return
	}
	existing, found, err := s.store.GetPatrolRoute(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "patrol_not_found", "patrol route not found")
		return
	}
	if user.Role != auth.RoleAdmin && existing.CreatedBy != user.Username {
		writeError(w, http.StatusForbidden, "forbidden", "only the creator or an admin can modify this route")
		return
	}
	var body patrolBody
	if decodeErr := json.NewDecoder(r.Body).Decode(&body); decodeErr != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	updated, err := s.validatePatrolBody(r, body, &existing)
	if err != nil {
		s.writePatrolError(w, err)
		return
	}
	updated.ID = existing.ID
	updated.CreatedBy = existing.CreatedBy
	updated.CreatedAt = existing.CreatedAt
	updated.UpdatedAt = time.Now().UTC()
	if err := s.store.UpdatePatrolRoute(r.Context(), *updated); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	s.audit(r, "patrol.update", updated.ID, map[string]any{"name": updated.Name})
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleDeletePatrolRoute(w http.ResponseWriter, r *http.Request) {
	user, ok := s.allow(w, r, auth.PermPatrolWrite)
	if !ok {
		return
	}
	existing, found, err := s.store.GetPatrolRoute(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "patrol_not_found", "patrol route not found")
		return
	}
	if user.Role != auth.RoleAdmin && existing.CreatedBy != user.Username {
		writeError(w, http.StatusForbidden, "forbidden", "only the creator or an admin can delete this route")
		return
	}
	if err := s.store.DeletePatrolRoute(r.Context(), existing.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	s.audit(r, "patrol.delete", existing.ID, nil)
	w.WriteHeader(http.StatusNoContent)
}

// validatePatrolBody merges a request body over an existing route (nil = create).
var errUnknownCamera = errors.New("unknown_camera")

func (s *Server) validatePatrolBody(r *http.Request, body patrolBody, existing *model.PatrolRoute) (*model.PatrolRoute, error) {
	route := model.PatrolRoute{IntervalSeconds: 3, CameraIDs: []string{}}
	if existing != nil {
		route = *existing
	}
	if name := strings.TrimSpace(body.Name); name != "" {
		route.Name = name
	}
	if route.Name == "" {
		return nil, errors.New("name is required")
	}
	if body.Waypoints != nil {
		route.Waypoints = *body.Waypoints
	}
	if len(route.Waypoints) < 2 || len(route.Waypoints) > 50 {
		return nil, errors.New("waypoints must contain 2 to 50 points")
	}
	if body.CameraIDs != nil {
		route.CameraIDs = *body.CameraIDs
	}
	if route.CameraIDs == nil {
		route.CameraIDs = []string{}
	}
	if len(route.CameraIDs) > 50 {
		return nil, errors.New("at most 50 cameras are accepted")
	}
	for _, cameraID := range route.CameraIDs {
		if _, err := s.store.GetCamera(r.Context(), cameraID); err != nil {
			return nil, fmt.Errorf("%w %q", errUnknownCamera, cameraID)
		}
	}
	if body.IntervalSeconds != nil {
		route.IntervalSeconds = *body.IntervalSeconds
	}
	if route.IntervalSeconds < 1 || route.IntervalSeconds > 60 {
		return nil, errors.New("interval_seconds must be within [1,60]")
	}
	return &route, nil
}

func (s *Server) writeCameraError(w http.ResponseWriter, err error) {
	if store.IsNotFound(err) {
		writeError(w, http.StatusNotFound, "camera_not_found", "camera not found")
		return
	}
	writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
}

func (s *Server) writeEventError(w http.ResponseWriter, err error) {
	if store.IsNotFound(err) {
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

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := w.Header()
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("X-Frame-Options", "DENY")
		header.Set("Referrer-Policy", "no-referrer")
		header.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
		next.ServeHTTP(w, r)
	})
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
