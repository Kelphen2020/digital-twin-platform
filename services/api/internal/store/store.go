// Package store defines persistence for the API. The in-memory implementation
// is used when DATABASE_URL is empty (smoke tests, laptop demo); the PostgreSQL
// implementation is used by docker compose and production.
//
// P27.0 adds multi-tenancy: every tenant-scoped entity lives behind the tenant
// carried by the request context (store.WithTenant / store.TenantFrom). The
// "default" tenant keeps the pre-P27 behaviour, so old clients keep working.
package store

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"digital-twin-platform/api/internal/model"
)

// ErrNotFound is the canonical not-found sentinel, aliased to model so that
// store implementations and API handlers can compare with errors.Is.
var ErrNotFound = model.ErrNotFound

// ErrConflict is the canonical conflict sentinel shared with model.
var ErrConflict = model.ErrConflict

// IsNotFound reports whether err is a not-found error from any Store
// implementation (memory or PostgreSQL).
func IsNotFound(err error) bool { return errors.Is(err, model.ErrNotFound) }

// IsConflict reports whether err is a uniqueness/conflict error.
func IsConflict(err error) bool { return errors.Is(err, model.ErrConflict) }

// Store is the persistence seam. Every method is safe for concurrent use.
type Store interface {
	CreateTenant(ctx context.Context, tenant model.Tenant) error
	GetTenant(ctx context.Context, id string) (model.Tenant, bool, error)
	ListTenants(ctx context.Context) ([]model.Tenant, error)
	UpdateTenant(ctx context.Context, tenant model.Tenant) error
	DeleteTenant(ctx context.Context, id string) (map[string]int, error)

	CreateExportJob(ctx context.Context, job model.ExportJob) error
	UpdateExportJob(ctx context.Context, job model.ExportJob) error
	GetExportJob(ctx context.Context, id string) (model.ExportJob, bool, error)
	ListExportJobs(ctx context.Context, tenantID string, limit int) ([]model.ExportJob, error)
	DeleteExportJob(ctx context.Context, id string) error
	PurgeExportJobs(ctx context.Context, tenantID string, before time.Time, keep int) ([]string, error)

	ListAreas(ctx context.Context) ([]model.Area, error)
	CreateArea(ctx context.Context, area model.Area) error
	ListCameras(ctx context.Context) ([]model.Camera, error)
	CreateCamera(ctx context.Context, camera model.Camera) error
	GetCamera(ctx context.Context, id string) (model.Camera, error)
	GetCameraByCode(ctx context.Context, code string) (model.Camera, error)
	UpdateCamera(ctx context.Context, id string, patch model.CameraPatch) (model.Camera, error)
	SetCameraCalibration(ctx context.Context, id string, calibration *model.Calibration) (model.Camera, error)
	ListEvents(ctx context.Context, filter model.EventFilter) ([]model.Event, error)
	GetEvent(ctx context.Context, id string) (model.Event, error)
	CreateEvents(ctx context.Context, events []model.Event) error
	AckEvent(ctx context.Context, id string) (model.Event, error)
	CloseEvent(ctx context.Context, id string) (model.Event, error)
	CreateEventAction(ctx context.Context, action model.EventAction) error
	ListEventActions(ctx context.Context, eventID string) ([]model.EventAction, error)
	CreateTrackPoints(ctx context.Context, points []model.TrackPoint) error
	ListTrackPoints(ctx context.Context, filter model.TrackFilter) ([]model.TrackPoint, error)
	AddAudit(ctx context.Context, entry model.AuditEntry) error
	ListAudit(ctx context.Context, filter model.AuditFilter) ([]model.AuditEntry, error)
	GetCameraRules(ctx context.Context, cameraID string) (model.CameraRules, error)
	SetCameraRules(ctx context.Context, rules model.CameraRules) (model.CameraRules, error)
	FindOpenOrderForEvent(ctx context.Context, eventID string) (model.Order, bool, error)
	CreateOrder(ctx context.Context, order model.Order) error
	GetOrder(ctx context.Context, id string) (model.Order, error)
	UpdateOrder(ctx context.Context, id string, update model.OrderUpdate) (model.Order, error)
	ListOrders(ctx context.Context, filter model.OrderFilter) ([]model.Order, error)
	GetSetting(ctx context.Context, key string) ([]byte, bool, error)
	SetSetting(ctx context.Context, key string, value []byte) error
	CreateNotificationLog(ctx context.Context, entry model.NotificationLog) error
	ListNotificationLog(ctx context.Context, limit int) ([]model.NotificationLog, error)
	EscalateStale(ctx context.Context, before time.Time, severities []string) ([]model.Event, error)
	ReEscalateStale(ctx context.Context, before time.Time, severities []string) ([]model.Event, error)
	CreatePatrolRoute(ctx context.Context, route model.PatrolRoute) error
	GetPatrolRoute(ctx context.Context, id string) (model.PatrolRoute, bool, error)
	ListPatrolRoutes(ctx context.Context) ([]model.PatrolRoute, error)
	UpdatePatrolRoute(ctx context.Context, route model.PatrolRoute) error
	DeletePatrolRoute(ctx context.Context, id string) error
	CreatePatrolSchedule(ctx context.Context, schedule model.PatrolSchedule) error
	GetPatrolSchedule(ctx context.Context, id string) (model.PatrolSchedule, bool, error)
	ListPatrolSchedules(ctx context.Context) ([]model.PatrolSchedule, error)
	UpdatePatrolSchedule(ctx context.Context, schedule model.PatrolSchedule) error
	DeletePatrolSchedule(ctx context.Context, id string) error
	CreatePatrolExecution(ctx context.Context, execution model.PatrolExecution) error
	GetPatrolExecution(ctx context.Context, id string) (model.PatrolExecution, bool, error)
	ListPatrolExecutions(ctx context.Context, filter model.PatrolExecutionFilter) ([]model.PatrolExecution, error)
	UpdatePatrolExecution(ctx context.Context, execution model.PatrolExecution) error
	DeletePatrolExecutions(ctx context.Context, before time.Time, statuses []string) (int, error)
	CreateRefreshToken(ctx context.Context, token model.RefreshToken) error
	GetRefreshTokenByHash(ctx context.Context, tokenHash string) (model.RefreshToken, bool, error)
	RevokeRefreshToken(ctx context.Context, id string) error
	RevokeUserRefreshTokens(ctx context.Context, userID string) error
	ListUserRefreshTokens(ctx context.Context, userID string) ([]model.RefreshToken, error)
	GetUserAccountByUsername(ctx context.Context, username string) (model.UserAccount, bool, error)
	GetUserAccountByID(ctx context.Context, id string) (model.UserAccount, bool, error)
	ListUserAccounts(ctx context.Context) ([]model.UserAccount, error)
	CreateUserAccount(ctx context.Context, account model.UserAccount) error
	UpdateUserAccount(ctx context.Context, account model.UserAccount) error
	DeleteUserAccount(ctx context.Context, id string) error
	CreateDutyGroup(ctx context.Context, group model.DutyGroup) error
	GetDutyGroup(ctx context.Context, id string) (model.DutyGroup, error)
	ListDutyGroups(ctx context.Context) ([]model.DutyGroup, error)
	UpdateDutyGroup(ctx context.Context, id string, group model.DutyGroup) (model.DutyGroup, error)
	DeleteDutyGroup(ctx context.Context, id string) error
	Stats(ctx context.Context, from, to time.Time, cameraIDs []string) (model.StatsSummary, error)
	Counts(ctx context.Context) (model.Counts, error)
}

// memoryState holds the per-tenant dataset. duty_groups and track_points stay
// global for now (docs/API_P27.md §3) and live on Memory directly.
type memoryState struct {
	cameras       map[string]*model.Camera
	order         []string
	areas         []model.Area
	events        []model.Event
	rules         map[string]model.CameraRules
	orders        []model.Order
	settings      map[string][]byte
	logs          []model.NotificationLog
	users         map[string]model.UserAccount
	usernames     map[string]string
	refresh       map[string]model.RefreshToken
	refreshByHash map[string]string
	patrols       map[string]model.PatrolRoute
	schedules     map[string]model.PatrolSchedule
	executions    map[string]model.PatrolExecution
	actions       map[string][]model.EventAction
	audit         []model.AuditEntry
	duty          []model.DutyGroup
	tracks        []model.TrackPoint
}

// Memory is the in-memory Store used without a database.
type Memory struct {
	mu     sync.RWMutex
	states map[string]*memoryState
	// tenant metadata (P27)
	tenants map[string]model.Tenant

	maxEvents int
	maxTracks int
	maxAudit  int
	// export jobs are stored per tenant and survive only in this process (P33)
	exportJobs map[string]model.ExportJob
}

// NewMemory creates an empty in-memory store with the seeded default tenant.
func NewMemory(maxEvents int) *Memory {
	if maxEvents < 100 {
		maxEvents = 100
	}
	m := &Memory{
		states:     make(map[string]*memoryState),
		tenants:    make(map[string]model.Tenant),
		maxEvents:  maxEvents,
		maxTracks:  50000,
		maxAudit:   5000,
		exportJobs: make(map[string]model.ExportJob),
	}
	m.states[model.DefaultTenantID] = newMemoryState()
	now := time.Now().UTC()
	m.tenants[model.DefaultTenantID] = model.Tenant{
		ID: model.DefaultTenantID, Name: "默认园区", Active: true,
		CreatedAt: now, UpdatedAt: now,
	}
	return m
}

func newMemoryState() *memoryState {
	return &memoryState{
		cameras:       make(map[string]*model.Camera),
		actions:       make(map[string][]model.EventAction),
		rules:         make(map[string]model.CameraRules),
		settings:      make(map[string][]byte),
		users:         make(map[string]model.UserAccount),
		usernames:     make(map[string]string),
		refresh:       make(map[string]model.RefreshToken),
		refreshByHash: make(map[string]string),
		patrols:       make(map[string]model.PatrolRoute),
		schedules:     make(map[string]model.PatrolSchedule),
		executions:    make(map[string]model.PatrolExecution),
	}
}

// stateLocked returns the dataset for the context tenant. Callers must hold at
// least the read lock; create must only be true under the write lock.
func (m *Memory) stateLocked(tenantID string, create bool) *memoryState {
	if state, ok := m.states[tenantID]; ok {
		return state
	}
	if !create {
		return nil
	}
	state := newMemoryState()
	m.states[tenantID] = state
	return state
}

func (m *Memory) state(ctx context.Context, create bool) *memoryState {
	return m.stateLocked(tenantOf(ctx), create)
}

// ---- tenants (P27.0) ------------------------------------------------------

func (m *Memory) CreateTenant(_ context.Context, tenant model.Tenant) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.tenants[tenant.ID]; exists {
		return model.ErrConflict
	}
	m.tenants[tenant.ID] = tenant
	m.stateLocked(tenant.ID, true)
	return nil
}

func (m *Memory) GetTenant(_ context.Context, id string) (model.Tenant, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	tenant, ok := m.tenants[id]
	return tenant, ok, nil
}

func (m *Memory) ListTenants(_ context.Context) ([]model.Tenant, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]model.Tenant, 0, len(m.tenants))
	for _, tenant := range m.tenants {
		out = append(out, tenant)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (m *Memory) UpdateTenant(_ context.Context, tenant model.Tenant) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.tenants[tenant.ID]; !exists {
		return model.ErrNotFound
	}
	m.tenants[tenant.ID] = tenant
	return nil
}

// ErrProtectedTenant is returned when an operation targets the implicit
// default tenant (which may not be archived or deleted).
var ErrProtectedTenant = errors.New("default tenant is protected")

// DeleteTenant removes every record of one tenant and returns per-entity counts.
func (m *Memory) DeleteTenant(_ context.Context, id string) (map[string]int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.tenants[id]; !exists {
		return nil, model.ErrNotFound
	}
	if id == model.DefaultTenantID {
		return nil, ErrProtectedTenant
	}
	counts := map[string]int{}
	if state, ok := m.states[id]; ok {
		counts["cameras"] = len(state.cameras)
		counts["areas"] = len(state.areas)
		counts["rules"] = len(state.rules)
		counts["settings"] = len(state.settings)
		counts["events"] = len(state.events)
		counts["orders"] = len(state.orders)
		counts["users"] = len(state.users)
		counts["refresh_tokens"] = len(state.refresh)
		counts["patrol_routes"] = len(state.patrols)
		counts["patrol_schedules"] = len(state.schedules)
		counts["patrol_executions"] = len(state.executions)
		counts["audit"] = len(state.audit)
		counts["notifications"] = len(state.logs)
		counts["tracks"] = len(state.tracks)
		counts["duty_groups"] = len(state.duty)
		actions := 0
		for _, list := range state.actions {
			actions += len(list)
		}
		counts["actions"] = actions
	}
	delete(m.states, id)
	delete(m.tenants, id)
	return counts, nil
}

// Seed loads the demo site into the default tenant. It is idempotent per id.
func (m *Memory) Seed(cameras []model.Camera, areas []model.Area) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.stateLocked(model.DefaultTenantID, true)
	for i := range areas {
		areas[i].TenantID = model.DefaultTenantID
	}
	state.areas = append([]model.Area(nil), areas...)
	for i := range cameras {
		clone := cameras[i]
		clone.TenantID = model.DefaultTenantID
		if _, exists := state.cameras[clone.ID]; !exists {
			state.order = append(state.order, clone.ID)
		}
		state.cameras[clone.ID] = &clone
	}
}

func (m *Memory) ListAreas(ctx context.Context) ([]model.Area, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	state := m.state(ctx, false)
	if state == nil {
		return []model.Area{}, nil
	}
	return append([]model.Area(nil), state.areas...), nil
}

func (m *Memory) CreateArea(ctx context.Context, area model.Area) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.state(ctx, true)
	area.TenantID = tenantOf(ctx)
	state.areas = append(state.areas, area)
	return nil
}

func (m *Memory) CreateCamera(ctx context.Context, camera model.Camera) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.state(ctx, true)
	tenant := tenantOf(ctx)
	for _, existing := range state.cameras {
		if existing.Code == camera.Code && existing.TenantID == tenant {
			return model.ErrConflict
		}
	}
	camera.TenantID = tenant
	clone := camera
	state.cameras[clone.ID] = &clone
	state.order = append(state.order, clone.ID)
	return nil
}

func (m *Memory) ListCameras(ctx context.Context) ([]model.Camera, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	state := m.state(ctx, false)
	if state == nil {
		return []model.Camera{}, nil
	}
	out := make([]model.Camera, 0, len(state.order))
	for _, id := range state.order {
		if camera, ok := state.cameras[id]; ok {
			out = append(out, *camera)
		}
	}
	return out, nil
}

func (m *Memory) GetCamera(ctx context.Context, id string) (model.Camera, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	state := m.state(ctx, false)
	if state == nil {
		return model.Camera{}, model.ErrNotFound
	}
	camera, ok := state.cameras[id]
	if !ok || camera.TenantID != tenantOf(ctx) {
		return model.Camera{}, model.ErrNotFound
	}
	return *camera, nil
}

func (m *Memory) GetCameraByCode(ctx context.Context, code string) (model.Camera, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	state := m.state(ctx, false)
	if state == nil {
		return model.Camera{}, model.ErrNotFound
	}
	tenant := tenantOf(ctx)
	for _, camera := range state.cameras {
		if camera.Code == code && camera.TenantID == tenant {
			return *camera, nil
		}
	}
	return model.Camera{}, model.ErrNotFound
}

func (m *Memory) UpdateCamera(ctx context.Context, id string, patch model.CameraPatch) (model.Camera, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.state(ctx, false)
	if state == nil {
		return model.Camera{}, model.ErrNotFound
	}
	camera, ok := state.cameras[id]
	if !ok || camera.TenantID != tenantOf(ctx) {
		return model.Camera{}, model.ErrNotFound
	}
	applyPatch(camera, patch)
	camera.UpdatedAt = time.Now().UTC()
	return *camera, nil
}

func (m *Memory) SetCameraCalibration(ctx context.Context, id string, calibration *model.Calibration) (model.Camera, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.state(ctx, false)
	if state == nil {
		return model.Camera{}, model.ErrNotFound
	}
	camera, ok := state.cameras[id]
	if !ok || camera.TenantID != tenantOf(ctx) {
		return model.Camera{}, model.ErrNotFound
	}
	if calibration != nil {
		now := time.Now().UTC()
		clone := *calibration
		clone.SolvedAt = &now
		camera.Calibration = &clone
	} else {
		camera.Calibration = nil
	}
	camera.UpdatedAt = time.Now().UTC()
	return *camera, nil
}

func (m *Memory) ListEvents(ctx context.Context, filter model.EventFilter) ([]model.Event, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	state := m.state(ctx, false)
	if state == nil {
		return []model.Event{}, nil
	}
	tenant := tenantOf(ctx)
	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	out := make([]model.Event, 0, limit)
	for i := len(state.events) - 1; i >= 0 && len(out) < limit; i-- {
		event := state.events[i]
		if event.TenantID != tenant {
			continue
		}
		if filter.Status != "" && event.Status != filter.Status {
			continue
		}
		if filter.Type != "" && event.Type != filter.Type {
			continue
		}
		if filter.Since != nil && event.OccurredAt.Before(*filter.Since) {
			continue
		}
		if len(filter.CameraIDs) > 0 && !containsString(filter.CameraIDs, event.CameraID) {
			continue
		}
		out = append(out, event)
	}
	return out, nil
}

func (m *Memory) GetEvent(ctx context.Context, id string) (model.Event, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	state := m.state(ctx, false)
	if state == nil {
		return model.Event{}, model.ErrNotFound
	}
	tenant := tenantOf(ctx)
	for i := range state.events {
		if state.events[i].ID == id && state.events[i].TenantID == tenant {
			return state.events[i], nil
		}
	}
	return model.Event{}, model.ErrNotFound
}

func (m *Memory) CreateEvents(ctx context.Context, events []model.Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.state(ctx, true)
	tenant := tenantOf(ctx)
	for i := range events {
		events[i].TenantID = tenant
	}
	state.events = append(state.events, events...)
	if len(state.events) > m.maxEvents {
		state.events = append([]model.Event(nil), state.events[len(state.events)-m.maxEvents:]...)
	}
	return nil
}

func (m *Memory) AckEvent(ctx context.Context, id string) (model.Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.state(ctx, false)
	if state == nil {
		return model.Event{}, model.ErrNotFound
	}
	tenant := tenantOf(ctx)
	for i := range state.events {
		if state.events[i].ID != id || state.events[i].TenantID != tenant {
			continue
		}
		state.events[i].Status = "acked"
		return state.events[i], nil
	}
	return model.Event{}, model.ErrNotFound
}

func (m *Memory) CloseEvent(ctx context.Context, id string) (model.Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.state(ctx, false)
	if state == nil {
		return model.Event{}, model.ErrNotFound
	}
	tenant := tenantOf(ctx)
	for i := range state.events {
		if state.events[i].ID != id || state.events[i].TenantID != tenant {
			continue
		}
		now := time.Now().UTC()
		state.events[i].Status = "closed"
		state.events[i].ClosedAt = &now
		return state.events[i], nil
	}
	return model.Event{}, model.ErrNotFound
}

func (m *Memory) CreateEventAction(ctx context.Context, action model.EventAction) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.state(ctx, false)
	if state == nil {
		return model.ErrNotFound
	}
	if _, ok := findEventLocked(state, action.EventID, tenantOf(ctx)); !ok {
		return model.ErrNotFound
	}
	state.actions[action.EventID] = append(state.actions[action.EventID], action)
	return nil
}

func (m *Memory) ListEventActions(ctx context.Context, eventID string) ([]model.EventAction, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	state := m.state(ctx, false)
	if state == nil {
		return []model.EventAction{}, nil
	}
	return append([]model.EventAction(nil), state.actions[eventID]...), nil
}

func (m *Memory) CreateTrackPoints(ctx context.Context, points []model.TrackPoint) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.state(ctx, true)
	tenant := tenantOf(ctx)
	for i := range points {
		points[i].TenantID = tenant
	}
	state.tracks = append(state.tracks, points...)
	if len(state.tracks) > m.maxTracks {
		state.tracks = append([]model.TrackPoint(nil), state.tracks[len(state.tracks)-m.maxTracks:]...)
	}
	return nil
}

func (m *Memory) ListTrackPoints(ctx context.Context, filter model.TrackFilter) ([]model.TrackPoint, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	state := m.state(ctx, false)
	if state == nil {
		return []model.TrackPoint{}, nil
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = 500
	}
	out := make([]model.TrackPoint, 0, limit)
	for i := len(state.tracks) - 1; i >= 0 && len(out) < limit; i-- {
		point := state.tracks[i]
		if filter.CameraID != "" && point.CameraID != filter.CameraID {
			continue
		}
		if filter.Since != nil && point.TS.Before(*filter.Since) {
			continue
		}
		if len(filter.CameraIDs) > 0 && !containsString(filter.CameraIDs, point.CameraID) {
			continue
		}
		out = append(out, point)
	}
	return out, nil
}

func (m *Memory) AddAudit(ctx context.Context, entry model.AuditEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.state(ctx, true)
	entry.TenantID = tenantOf(ctx)
	state.audit = append(state.audit, entry)
	if len(state.audit) > m.maxAudit {
		state.audit = append([]model.AuditEntry(nil), state.audit[len(state.audit)-m.maxAudit:]...)
	}
	return nil
}

func (m *Memory) ListAudit(ctx context.Context, filter model.AuditFilter) ([]model.AuditEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	state := m.state(ctx, false)
	if state == nil {
		return []model.AuditEntry{}, nil
	}
	tenant := tenantOf(ctx)
	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}
	out := make([]model.AuditEntry, 0, limit)
	for i := len(state.audit) - 1; i >= 0 && len(out) < limit; i-- {
		entry := state.audit[i]
		if entry.TenantID != tenant {
			continue
		}
		if filter.Action != "" && entry.Action != filter.Action {
			continue
		}
		if filter.Actor != "" && entry.Actor != filter.Actor {
			continue
		}
		if filter.From != nil && entry.At.Before(*filter.From) {
			continue
		}
		if filter.To != nil && entry.At.After(*filter.To) {
			continue
		}
		out = append(out, entry)
	}
	return out, nil
}

func (m *Memory) Stats(ctx context.Context, from, to time.Time, cameraIDs []string) (model.StatsSummary, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	state := m.state(ctx, false)
	summary := model.StatsSummary{
		From:   from,
		To:     to,
		Totals: model.StatsTotals{BySeverity: map[string]int64{}, ByType: map[string]int64{}},
	}
	if state == nil {
		return summary, nil
	}
	tenant := tenantOf(ctx)
	perCamera := map[string]int64{}
	perHour := map[time.Time]int64{}
	for _, event := range state.events {
		if event.TenantID != tenant {
			continue
		}
		if event.OccurredAt.Before(from) || event.OccurredAt.After(to) {
			continue
		}
		if len(cameraIDs) > 0 && !containsString(cameraIDs, event.CameraID) {
			continue
		}
		summary.Totals.Events++
		summary.Totals.BySeverity[event.Severity]++
		summary.Totals.ByType[event.Type]++
		perCamera[event.CameraID]++
		perHour[event.OccurredAt.UTC().Truncate(time.Hour)]++
	}
	for _, id := range state.order {
		camera, ok := state.cameras[id]
		if !ok || camera.TenantID != tenant {
			continue
		}
		count := perCamera[id]
		if count == 0 {
			continue
		}
		summary.ByCamera = append(summary.ByCamera, model.CameraStat{
			CameraID: id, Code: camera.Code, Name: camera.Name, Count: count,
		})
	}
	sort.Slice(summary.ByCamera, func(i, j int) bool { return summary.ByCamera[i].Count > summary.ByCamera[j].Count })
	for hour, count := range perHour {
		summary.ByHour = append(summary.ByHour, model.HourStat{Hour: hour, Count: count})
	}
	sort.Slice(summary.ByHour, func(i, j int) bool { return summary.ByHour[i].Hour.Before(summary.ByHour[j].Hour) })
	return summary, nil
}

func (m *Memory) GetCameraRules(ctx context.Context, cameraID string) (model.CameraRules, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	state := m.state(ctx, false)
	if state == nil {
		return model.CameraRules{}, model.ErrNotFound
	}
	rules, ok := state.rules[cameraID]
	if !ok || rules.TenantID != tenantOf(ctx) {
		return model.CameraRules{}, model.ErrNotFound
	}
	return rules, nil
}

func (m *Memory) SetCameraRules(ctx context.Context, rules model.CameraRules) (model.CameraRules, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.state(ctx, false)
	if state == nil {
		return model.CameraRules{}, model.ErrNotFound
	}
	tenant := tenantOf(ctx)
	if camera, ok := state.cameras[rules.CameraID]; !ok || camera.TenantID != tenant {
		return model.CameraRules{}, model.ErrNotFound
	}
	now := time.Now().UTC()
	rules.TenantID = tenant
	rules.UpdatedAt = &now
	if rules.EnabledClasses == nil {
		rules.EnabledClasses = []string{}
	}
	state.rules[rules.CameraID] = rules
	return rules, nil
}

func (m *Memory) FindOpenOrderForEvent(ctx context.Context, eventID string) (model.Order, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	state := m.state(ctx, false)
	if state == nil {
		return model.Order{}, false, nil
	}
	tenant := tenantOf(ctx)
	for i := len(state.orders) - 1; i >= 0; i-- {
		order := state.orders[i]
		if order.EventID != eventID || order.TenantID != tenant {
			continue
		}
		switch order.Status {
		case "open", "assigned", "processing":
			return order, true, nil
		}
	}
	return model.Order{}, false, nil
}

func (m *Memory) ReEscalateStale(ctx context.Context, before time.Time, severities []string) ([]model.Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.state(ctx, false)
	if state == nil {
		return []model.Event{}, nil
	}
	tenant := tenantOf(ctx)
	now := time.Now().UTC()
	escalated := make([]model.Event, 0, 4)
	for i := range state.events {
		event := &state.events[i]
		if event.TenantID != tenant {
			continue
		}
		if event.Status == "closed" || event.EscalatedAt == nil || event.ReEscalatedAt != nil {
			continue
		}
		if event.EscalatedAt.After(before) {
			continue
		}
		if len(severities) > 0 && !containsString(severities, event.Severity) {
			continue
		}
		stamp := now
		event.ReEscalatedAt = &stamp
		escalated = append(escalated, *event)
	}
	return escalated, nil
}

func (m *Memory) CreateOrder(ctx context.Context, order model.Order) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.state(ctx, true)
	order.TenantID = tenantOf(ctx)
	state.orders = append(state.orders, order)
	if len(state.orders) > 10000 {
		state.orders = append([]model.Order(nil), state.orders[len(state.orders)-10000:]...)
	}
	return nil
}

func (m *Memory) GetOrder(ctx context.Context, id string) (model.Order, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	state := m.state(ctx, false)
	if state == nil {
		return model.Order{}, model.ErrNotFound
	}
	tenant := tenantOf(ctx)
	for i := range state.orders {
		if state.orders[i].ID == id && state.orders[i].TenantID == tenant {
			return state.orders[i], nil
		}
	}
	return model.Order{}, model.ErrNotFound
}

func (m *Memory) UpdateOrder(ctx context.Context, id string, update model.OrderUpdate) (model.Order, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.state(ctx, false)
	if state == nil {
		return model.Order{}, model.ErrNotFound
	}
	tenant := tenantOf(ctx)
	for i := range state.orders {
		if state.orders[i].ID != id || state.orders[i].TenantID != tenant {
			continue
		}
		applyOrderUpdate(&state.orders[i], update)
		return state.orders[i], nil
	}
	return model.Order{}, model.ErrNotFound
}

func (m *Memory) ListOrders(ctx context.Context, filter model.OrderFilter) ([]model.Order, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	state := m.state(ctx, false)
	if state == nil {
		return []model.Order{}, nil
	}
	tenant := tenantOf(ctx)
	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}
	out := make([]model.Order, 0, limit)
	for i := len(state.orders) - 1; i >= 0 && len(out) < limit; i-- {
		order := state.orders[i]
		if order.TenantID != tenant {
			continue
		}
		if filter.Status != "" && order.Status != filter.Status {
			continue
		}
		if filter.Assignee != "" && order.Assignee != filter.Assignee {
			continue
		}
		if filter.EventID != "" && order.EventID != filter.EventID {
			continue
		}
		if len(filter.CameraIDs) > 0 && !containsString(filter.CameraIDs, order.CameraID) {
			continue
		}
		out = append(out, order)
	}
	return out, nil
}

func applyOrderUpdate(order *model.Order, update model.OrderUpdate) {
	now := time.Now().UTC()
	if update.Status != nil {
		order.Status = *update.Status
		if order.Status == "done" || order.Status == "cancelled" {
			closed := now
			order.ClosedAt = &closed
		}
	}
	if update.Assignee != nil {
		order.Assignee = *update.Assignee
	}
	if update.Note != nil {
		order.Note = *update.Note
	}
	order.UpdatedAt = now
}

func (m *Memory) Counts(ctx context.Context) (model.Counts, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	state := m.state(ctx, false)
	counts := model.Counts{}
	if state == nil {
		return counts, nil
	}
	tenant := tenantOf(ctx)
	for _, event := range state.events {
		if event.TenantID == tenant {
			counts.EventsTotal++
		}
	}
	for _, camera := range state.cameras {
		if camera.TenantID == tenant && camera.Status == "online" {
			counts.OnlineCameras++
		}
	}
	return counts, nil
}

func findEventLocked(state *memoryState, id, tenantID string) (*model.Event, bool) {
	for i := range state.events {
		if state.events[i].ID == id && state.events[i].TenantID == tenantID {
			return &state.events[i], true
		}
	}
	return nil, false
}

func (m *Memory) CreatePatrolRoute(ctx context.Context, route model.PatrolRoute) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.state(ctx, true)
	route.TenantID = tenantOf(ctx)
	state.patrols[route.ID] = route
	return nil
}

func (m *Memory) GetPatrolRoute(ctx context.Context, id string) (model.PatrolRoute, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	state := m.state(ctx, false)
	if state == nil {
		return model.PatrolRoute{}, false, nil
	}
	route, ok := state.patrols[id]
	if !ok || route.TenantID != tenantOf(ctx) {
		return model.PatrolRoute{}, false, nil
	}
	return route, true, nil
}

func (m *Memory) ListPatrolRoutes(ctx context.Context) ([]model.PatrolRoute, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	state := m.state(ctx, false)
	if state == nil {
		return []model.PatrolRoute{}, nil
	}
	tenant := tenantOf(ctx)
	out := make([]model.PatrolRoute, 0, len(state.patrols))
	for _, route := range state.patrols {
		if route.TenantID == tenant {
			out = append(out, route)
		}
	}
	return out, nil
}

func (m *Memory) UpdatePatrolRoute(ctx context.Context, route model.PatrolRoute) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.state(ctx, false)
	if state == nil {
		return model.ErrNotFound
	}
	existing, ok := state.patrols[route.ID]
	if !ok || existing.TenantID != tenantOf(ctx) {
		return model.ErrNotFound
	}
	route.TenantID = existing.TenantID
	state.patrols[route.ID] = route
	return nil
}

func (m *Memory) DeletePatrolRoute(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.state(ctx, false)
	if state == nil {
		return model.ErrNotFound
	}
	existing, ok := state.patrols[id]
	if !ok || existing.TenantID != tenantOf(ctx) {
		return model.ErrNotFound
	}
	delete(state.patrols, id)
	return nil
}

func (m *Memory) CreatePatrolSchedule(ctx context.Context, schedule model.PatrolSchedule) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.state(ctx, true)
	schedule.TenantID = tenantOf(ctx)
	state.schedules[schedule.ID] = schedule
	return nil
}

func (m *Memory) GetPatrolSchedule(ctx context.Context, id string) (model.PatrolSchedule, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	state := m.state(ctx, false)
	if state == nil {
		return model.PatrolSchedule{}, false, nil
	}
	schedule, ok := state.schedules[id]
	if !ok || schedule.TenantID != tenantOf(ctx) {
		return model.PatrolSchedule{}, false, nil
	}
	return schedule, true, nil
}

func (m *Memory) ListPatrolSchedules(ctx context.Context) ([]model.PatrolSchedule, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	state := m.state(ctx, false)
	if state == nil {
		return []model.PatrolSchedule{}, nil
	}
	tenant := tenantOf(ctx)
	out := make([]model.PatrolSchedule, 0, len(state.schedules))
	for _, schedule := range state.schedules {
		if schedule.TenantID == tenant {
			out = append(out, schedule)
		}
	}
	return out, nil
}

func (m *Memory) UpdatePatrolSchedule(ctx context.Context, schedule model.PatrolSchedule) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.state(ctx, false)
	if state == nil {
		return model.ErrNotFound
	}
	existing, ok := state.schedules[schedule.ID]
	if !ok || existing.TenantID != tenantOf(ctx) {
		return model.ErrNotFound
	}
	schedule.TenantID = existing.TenantID
	state.schedules[schedule.ID] = schedule
	return nil
}

func (m *Memory) DeletePatrolSchedule(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.state(ctx, false)
	if state == nil {
		return model.ErrNotFound
	}
	existing, ok := state.schedules[id]
	if !ok || existing.TenantID != tenantOf(ctx) {
		return model.ErrNotFound
	}
	delete(state.schedules, id)
	return nil
}

func (m *Memory) CreatePatrolExecution(ctx context.Context, execution model.PatrolExecution) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.state(ctx, true)
	execution.TenantID = tenantOf(ctx)
	state.executions[execution.ID] = execution
	return nil
}

func (m *Memory) GetPatrolExecution(ctx context.Context, id string) (model.PatrolExecution, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	state := m.state(ctx, false)
	if state == nil {
		return model.PatrolExecution{}, false, nil
	}
	execution, ok := state.executions[id]
	if !ok || execution.TenantID != tenantOf(ctx) {
		return model.PatrolExecution{}, false, nil
	}
	return execution, true, nil
}

func (m *Memory) ListPatrolExecutions(ctx context.Context, filter model.PatrolExecutionFilter) ([]model.PatrolExecution, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	state := m.state(ctx, false)
	if state == nil {
		return []model.PatrolExecution{}, nil
	}
	tenant := tenantOf(ctx)
	out := make([]model.PatrolExecution, 0, len(state.executions))
	for _, execution := range state.executions {
		if execution.TenantID != tenant {
			continue
		}
		if filter.Status != "" && execution.Status != filter.Status {
			continue
		}
		if filter.ScheduleID != "" && execution.ScheduleID != filter.ScheduleID {
			continue
		}
		if filter.EventID != "" && execution.EventID != filter.EventID {
			continue
		}
		if filter.From != nil && execution.CreatedAt.Before(*filter.From) {
			continue
		}
		if filter.To != nil && execution.CreatedAt.After(*filter.To) {
			continue
		}
		out = append(out, execution)
	}
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

func (m *Memory) DeletePatrolExecutions(ctx context.Context, before time.Time, statuses []string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.state(ctx, false)
	if state == nil {
		return 0, nil
	}
	tenant := tenantOf(ctx)
	allowed := make(map[string]struct{}, len(statuses))
	for _, status := range statuses {
		allowed[status] = struct{}{}
	}
	deleted := 0
	for id, execution := range state.executions {
		if execution.TenantID != tenant {
			continue
		}
		if _, ok := allowed[execution.Status]; !ok {
			continue
		}
		if execution.CreatedAt.After(before) {
			continue
		}
		delete(state.executions, id)
		deleted++
	}
	return deleted, nil
}

func (m *Memory) UpdatePatrolExecution(ctx context.Context, execution model.PatrolExecution) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.state(ctx, false)
	if state == nil {
		return model.ErrNotFound
	}
	existing, ok := state.executions[execution.ID]
	if !ok || existing.TenantID != tenantOf(ctx) {
		return model.ErrNotFound
	}
	execution.TenantID = existing.TenantID
	state.executions[execution.ID] = execution
	return nil
}

func (m *Memory) CreateRefreshToken(ctx context.Context, token model.RefreshToken) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.state(ctx, true)
	token.TenantID = tenantOf(ctx)
	state.refresh[token.ID] = token
	state.refreshByHash[token.TokenHash] = token.ID
	return nil
}

func (m *Memory) GetRefreshTokenByHash(ctx context.Context, tokenHash string) (model.RefreshToken, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	state := m.state(ctx, false)
	if state == nil {
		return model.RefreshToken{}, false, nil
	}
	id, ok := state.refreshByHash[tokenHash]
	if !ok {
		return model.RefreshToken{}, false, nil
	}
	token, exists := state.refresh[id]
	if !exists || token.TenantID != tenantOf(ctx) {
		return model.RefreshToken{}, false, nil
	}
	return token, true, nil
}

func (m *Memory) RevokeRefreshToken(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.state(ctx, false)
	if state == nil {
		return model.ErrNotFound
	}
	token, ok := state.refresh[id]
	if !ok || token.TenantID != tenantOf(ctx) {
		return model.ErrNotFound
	}
	token.Revoked = true
	state.refresh[id] = token
	return nil
}

func (m *Memory) RevokeUserRefreshTokens(ctx context.Context, userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.state(ctx, false)
	if state == nil {
		return nil
	}
	tenant := tenantOf(ctx)
	for id, token := range state.refresh {
		if token.UserID == userID && token.TenantID == tenant {
			token.Revoked = true
			state.refresh[id] = token
		}
	}
	return nil
}

func (m *Memory) ListUserRefreshTokens(ctx context.Context, userID string) ([]model.RefreshToken, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	state := m.state(ctx, false)
	if state == nil {
		return []model.RefreshToken{}, nil
	}
	tenant := tenantOf(ctx)
	out := make([]model.RefreshToken, 0, 4)
	for _, token := range state.refresh {
		if token.UserID == userID && token.TenantID == tenant {
			out = append(out, token)
		}
	}
	return out, nil
}

func (m *Memory) GetUserAccountByUsername(ctx context.Context, username string) (model.UserAccount, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	state := m.state(ctx, false)
	if state == nil {
		return model.UserAccount{}, false, nil
	}
	id, ok := state.usernames[username]
	if !ok {
		return model.UserAccount{}, false, nil
	}
	account, exists := state.users[id]
	if !exists || account.TenantID != tenantOf(ctx) {
		return model.UserAccount{}, false, nil
	}
	return account, true, nil
}

func (m *Memory) GetUserAccountByID(ctx context.Context, id string) (model.UserAccount, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	state := m.state(ctx, false)
	if state == nil {
		return model.UserAccount{}, false, nil
	}
	account, ok := state.users[id]
	if !ok || account.TenantID != tenantOf(ctx) {
		return model.UserAccount{}, false, nil
	}
	return account, true, nil
}

func (m *Memory) ListUserAccounts(ctx context.Context) ([]model.UserAccount, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	state := m.state(ctx, false)
	if state == nil {
		return []model.UserAccount{}, nil
	}
	tenant := tenantOf(ctx)
	out := make([]model.UserAccount, 0, len(state.users))
	for _, account := range state.users {
		if account.TenantID == tenant {
			out = append(out, account)
		}
	}
	return out, nil
}

func (m *Memory) CreateUserAccount(ctx context.Context, account model.UserAccount) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.state(ctx, true)
	if _, exists := state.usernames[account.Username]; exists {
		return model.ErrConflict
	}
	account.TenantID = tenantOf(ctx)
	state.users[account.ID] = account
	state.usernames[account.Username] = account.ID
	return nil
}

func (m *Memory) UpdateUserAccount(ctx context.Context, account model.UserAccount) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.state(ctx, false)
	if state == nil {
		return model.ErrNotFound
	}
	existing, exists := state.users[account.ID]
	if !exists || existing.TenantID != tenantOf(ctx) {
		return model.ErrNotFound
	}
	account.TenantID = existing.TenantID
	state.users[account.ID] = account
	state.usernames[account.Username] = account.ID
	return nil
}

func (m *Memory) DeleteUserAccount(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.state(ctx, false)
	if state == nil {
		return model.ErrNotFound
	}
	account, exists := state.users[id]
	if !exists || account.TenantID != tenantOf(ctx) {
		return model.ErrNotFound
	}
	delete(state.users, id)
	delete(state.usernames, account.Username)
	return nil
}

func (m *Memory) CreateDutyGroup(ctx context.Context, group model.DutyGroup) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.state(ctx, true)
	group.TenantID = tenantOf(ctx)
	state.duty = append(state.duty, group)
	return nil
}

func (m *Memory) GetDutyGroup(ctx context.Context, id string) (model.DutyGroup, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	state := m.state(ctx, false)
	if state == nil {
		return model.DutyGroup{}, model.ErrNotFound
	}
	for i := range state.duty {
		if state.duty[i].ID == id {
			return state.duty[i], nil
		}
	}
	return model.DutyGroup{}, model.ErrNotFound
}

func (m *Memory) ListDutyGroups(ctx context.Context) ([]model.DutyGroup, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	state := m.state(ctx, false)
	if state == nil {
		return []model.DutyGroup{}, nil
	}
	return append([]model.DutyGroup(nil), state.duty...), nil
}

func (m *Memory) UpdateDutyGroup(ctx context.Context, id string, group model.DutyGroup) (model.DutyGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.state(ctx, true)
	for i := range state.duty {
		if state.duty[i].ID != id {
			continue
		}
		group.ID = id
		group.TenantID = tenantOf(ctx)
		group.CreatedAt = state.duty[i].CreatedAt
		group.UpdatedAt = time.Now().UTC()
		state.duty[i] = group
		return group, nil
	}
	return model.DutyGroup{}, model.ErrNotFound
}

func (m *Memory) DeleteDutyGroup(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.state(ctx, false)
	if state == nil {
		return model.ErrNotFound
	}
	for i := range state.duty {
		if state.duty[i].ID == id {
			state.duty = append(state.duty[:i], state.duty[i+1:]...)
			return nil
		}
	}
	return model.ErrNotFound
}

func (m *Memory) GetSetting(ctx context.Context, key string) ([]byte, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	state := m.state(ctx, false)
	if state == nil {
		return nil, false, nil
	}
	value, ok := state.settings[key]
	if !ok {
		return nil, false, nil
	}
	return append([]byte(nil), value...), true, nil
}

func (m *Memory) SetSetting(ctx context.Context, key string, value []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.state(ctx, true)
	state.settings[key] = append([]byte(nil), value...)
	return nil
}

func (m *Memory) CreateNotificationLog(ctx context.Context, entry model.NotificationLog) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.state(ctx, true)
	entry.TenantID = tenantOf(ctx)
	state.logs = append(state.logs, entry)
	if len(state.logs) > 1000 {
		state.logs = append([]model.NotificationLog(nil), state.logs[len(state.logs)-1000:]...)
	}
	return nil
}

func (m *Memory) ListNotificationLog(ctx context.Context, limit int) ([]model.NotificationLog, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	state := m.state(ctx, false)
	if state == nil {
		return []model.NotificationLog{}, nil
	}
	tenant := tenantOf(ctx)
	if limit <= 0 {
		limit = 50
	}
	out := make([]model.NotificationLog, 0, limit)
	for i := len(state.logs) - 1; i >= 0 && len(out) < limit; i-- {
		if state.logs[i].TenantID != tenant {
			continue
		}
		out = append(out, state.logs[i])
	}
	return out, nil
}

func (m *Memory) EscalateStale(ctx context.Context, before time.Time, severities []string) ([]model.Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.state(ctx, false)
	if state == nil {
		return []model.Event{}, nil
	}
	tenant := tenantOf(ctx)
	now := time.Now().UTC()
	escalated := make([]model.Event, 0, 4)
	for i := range state.events {
		event := &state.events[i]
		if event.TenantID != tenant {
			continue
		}
		if event.Status == "closed" || event.EscalatedAt != nil {
			continue
		}
		if event.OccurredAt.After(before) {
			continue
		}
		if len(severities) > 0 && !containsString(severities, event.Severity) {
			continue
		}
		stamp := now
		event.EscalatedAt = &stamp
		escalated = append(escalated, *event)
	}
	return escalated, nil
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func applyPatch(camera *model.Camera, patch model.CameraPatch) {
	if patch.Status != nil {
		camera.Status = *patch.Status
	}
	if patch.X != nil {
		camera.X = *patch.X
	}
	if patch.Y != nil {
		camera.Y = *patch.Y
	}
	if patch.Z != nil {
		camera.Z = *patch.Z
	}
	if patch.Heading != nil {
		camera.Heading = *patch.Heading
	}
}

// SortCamerasByCode is a helper for deterministic seeding and tests.
func SortCamerasByCode(cameras []model.Camera) {
	sort.Slice(cameras, func(i, j int) bool { return cameras[i].Code < cameras[j].Code })
}
