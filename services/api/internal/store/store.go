// Package store defines persistence for the API. The in-memory implementation
// is used when DATABASE_URL is empty (smoke tests, laptop demo); the PostgreSQL
// implementation is used by docker compose and production.
package store

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"digital-twin-platform/api/internal/model"
)

// ErrNotFound is returned when a camera or event id does not exist.
var ErrNotFound = errors.New("not found")

// Store is the persistence seam. Every method is safe for concurrent use.
type Store interface {
	ListAreas(ctx context.Context) ([]model.Area, error)
	ListCameras(ctx context.Context) ([]model.Camera, error)
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

// Memory is the in-memory Store used without a database.
type Memory struct {
	mu            sync.RWMutex
	cameras       map[string]*model.Camera
	order         []string
	areas         []model.Area
	events        []model.Event
	rules         map[string]model.CameraRules
	orders        []model.Order
	settings      map[string][]byte
	logs          []model.NotificationLog
	duty          []model.DutyGroup
	users         map[string]model.UserAccount
	usernames     map[string]string
	refresh       map[string]model.RefreshToken
	refreshByHash map[string]string
	patrols       map[string]model.PatrolRoute
	schedules     map[string]model.PatrolSchedule
	executions    map[string]model.PatrolExecution
	tracks        []model.TrackPoint
	actions       map[string][]model.EventAction
	audit         []model.AuditEntry
	maxEvents     int
	maxTracks     int
	maxAudit      int
}

// NewMemory creates an empty in-memory store.
func NewMemory(maxEvents int) *Memory {
	if maxEvents < 100 {
		maxEvents = 100
	}
	return &Memory{
		cameras:       make(map[string]*model.Camera),
		maxEvents:     maxEvents,
		maxTracks:     50000,
		maxAudit:      5000,
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

// Seed loads the demo site. It is idempotent per id.
func (m *Memory) Seed(cameras []model.Camera, areas []model.Area) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.areas = append([]model.Area(nil), areas...)
	for _, camera := range cameras {
		clone := camera
		if _, exists := m.cameras[clone.ID]; !exists {
			m.order = append(m.order, clone.ID)
		}
		m.cameras[clone.ID] = &clone
	}
}

func (m *Memory) ListAreas(context.Context) ([]model.Area, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]model.Area(nil), m.areas...), nil
}

func (m *Memory) ListCameras(context.Context) ([]model.Camera, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]model.Camera, 0, len(m.order))
	for _, id := range m.order {
		if camera, ok := m.cameras[id]; ok {
			out = append(out, *camera)
		}
	}
	return out, nil
}

func (m *Memory) GetCamera(_ context.Context, id string) (model.Camera, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	camera, ok := m.cameras[id]
	if !ok {
		return model.Camera{}, ErrNotFound
	}
	return *camera, nil
}

func (m *Memory) GetCameraByCode(_ context.Context, code string) (model.Camera, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, camera := range m.cameras {
		if camera.Code == code {
			return *camera, nil
		}
	}
	return model.Camera{}, ErrNotFound
}

func (m *Memory) UpdateCamera(_ context.Context, id string, patch model.CameraPatch) (model.Camera, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	camera, ok := m.cameras[id]
	if !ok {
		return model.Camera{}, ErrNotFound
	}
	applyPatch(camera, patch)
	camera.UpdatedAt = time.Now().UTC()
	return *camera, nil
}

func (m *Memory) SetCameraCalibration(_ context.Context, id string, calibration *model.Calibration) (model.Camera, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	camera, ok := m.cameras[id]
	if !ok {
		return model.Camera{}, ErrNotFound
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

func (m *Memory) ListEvents(_ context.Context, filter model.EventFilter) ([]model.Event, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	out := make([]model.Event, 0, limit)
	for i := len(m.events) - 1; i >= 0 && len(out) < limit; i-- {
		event := m.events[i]
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

func (m *Memory) GetEvent(_ context.Context, id string) (model.Event, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for i := range m.events {
		if m.events[i].ID == id {
			return m.events[i], nil
		}
	}
	return model.Event{}, ErrNotFound
}

func (m *Memory) CreateEvents(_ context.Context, events []model.Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, events...)
	if len(m.events) > m.maxEvents {
		m.events = append([]model.Event(nil), m.events[len(m.events)-m.maxEvents:]...)
	}
	return nil
}

func (m *Memory) AckEvent(ctx context.Context, id string) (model.Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.events {
		if m.events[i].ID != id {
			continue
		}
		m.events[i].Status = "acked"
		return m.events[i], nil
	}
	return model.Event{}, ErrNotFound
}

func (m *Memory) CloseEvent(ctx context.Context, id string) (model.Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.events {
		if m.events[i].ID != id {
			continue
		}
		now := time.Now().UTC()
		m.events[i].Status = "closed"
		m.events[i].ClosedAt = &now
		return m.events[i], nil
	}
	return model.Event{}, ErrNotFound
}

func (m *Memory) CreateEventAction(_ context.Context, action model.EventAction) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.findEventLocked(action.EventID); !ok {
		return ErrNotFound
	}
	m.actions[action.EventID] = append(m.actions[action.EventID], action)
	return nil
}

func (m *Memory) ListEventActions(_ context.Context, eventID string) ([]model.EventAction, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]model.EventAction(nil), m.actions[eventID]...), nil
}

func (m *Memory) CreateTrackPoints(_ context.Context, points []model.TrackPoint) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tracks = append(m.tracks, points...)
	if len(m.tracks) > m.maxTracks {
		m.tracks = append([]model.TrackPoint(nil), m.tracks[len(m.tracks)-m.maxTracks:]...)
	}
	return nil
}

func (m *Memory) ListTrackPoints(_ context.Context, filter model.TrackFilter) ([]model.TrackPoint, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	limit := filter.Limit
	if limit <= 0 {
		limit = 500
	}
	out := make([]model.TrackPoint, 0, limit)
	for i := len(m.tracks) - 1; i >= 0 && len(out) < limit; i-- {
		point := m.tracks[i]
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

func (m *Memory) AddAudit(_ context.Context, entry model.AuditEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.audit = append(m.audit, entry)
	if len(m.audit) > m.maxAudit {
		m.audit = append([]model.AuditEntry(nil), m.audit[len(m.audit)-m.maxAudit:]...)
	}
	return nil
}

func (m *Memory) ListAudit(_ context.Context, filter model.AuditFilter) ([]model.AuditEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}
	out := make([]model.AuditEntry, 0, limit)
	for i := len(m.audit) - 1; i >= 0 && len(out) < limit; i-- {
		entry := m.audit[i]
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

func (m *Memory) Stats(_ context.Context, from, to time.Time, cameraIDs []string) (model.StatsSummary, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	summary := model.StatsSummary{
		From:   from,
		To:     to,
		Totals: model.StatsTotals{BySeverity: map[string]int64{}, ByType: map[string]int64{}},
	}
	perCamera := map[string]int64{}
	perHour := map[time.Time]int64{}
	for _, event := range m.events {
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
	for _, id := range m.order {
		camera, ok := m.cameras[id]
		if !ok {
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

func (m *Memory) GetCameraRules(_ context.Context, cameraID string) (model.CameraRules, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	rules, ok := m.rules[cameraID]
	if !ok {
		return model.CameraRules{}, ErrNotFound
	}
	return rules, nil
}

func (m *Memory) SetCameraRules(_ context.Context, rules model.CameraRules) (model.CameraRules, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.cameras[rules.CameraID]; !ok {
		return model.CameraRules{}, ErrNotFound
	}
	now := time.Now().UTC()
	rules.UpdatedAt = &now
	if rules.EnabledClasses == nil {
		rules.EnabledClasses = []string{}
	}
	m.rules[rules.CameraID] = rules
	return rules, nil
}

func (m *Memory) FindOpenOrderForEvent(_ context.Context, eventID string) (model.Order, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for i := len(m.orders) - 1; i >= 0; i-- {
		order := m.orders[i]
		if order.EventID != eventID {
			continue
		}
		switch order.Status {
		case "open", "assigned", "processing":
			return order, true, nil
		}
	}
	return model.Order{}, false, nil
}

func (m *Memory) ReEscalateStale(_ context.Context, before time.Time, severities []string) ([]model.Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	escalated := make([]model.Event, 0, 4)
	for i := range m.events {
		event := &m.events[i]
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

func (m *Memory) CreateOrder(_ context.Context, order model.Order) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.orders = append(m.orders, order)
	if len(m.orders) > 10000 {
		m.orders = append([]model.Order(nil), m.orders[len(m.orders)-10000:]...)
	}
	return nil
}

func (m *Memory) GetOrder(_ context.Context, id string) (model.Order, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for i := range m.orders {
		if m.orders[i].ID == id {
			return m.orders[i], nil
		}
	}
	return model.Order{}, ErrNotFound
}

func (m *Memory) UpdateOrder(_ context.Context, id string, update model.OrderUpdate) (model.Order, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.orders {
		if m.orders[i].ID != id {
			continue
		}
		applyOrderUpdate(&m.orders[i], update)
		return m.orders[i], nil
	}
	return model.Order{}, ErrNotFound
}

func (m *Memory) ListOrders(_ context.Context, filter model.OrderFilter) ([]model.Order, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}
	out := make([]model.Order, 0, limit)
	for i := len(m.orders) - 1; i >= 0 && len(out) < limit; i-- {
		order := m.orders[i]
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

func (m *Memory) Counts(_ context.Context) (model.Counts, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	counts := model.Counts{EventsTotal: int64(len(m.events))}
	for _, camera := range m.cameras {
		if camera.Status == "online" {
			counts.OnlineCameras++
		}
	}
	return counts, nil
}

func (m *Memory) findEventLocked(id string) (*model.Event, bool) {
	for i := range m.events {
		if m.events[i].ID == id {
			return &m.events[i], true
		}
	}
	return nil, false
}

func (m *Memory) CreatePatrolRoute(_ context.Context, route model.PatrolRoute) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.patrols[route.ID] = route
	return nil
}

func (m *Memory) GetPatrolRoute(_ context.Context, id string) (model.PatrolRoute, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	route, ok := m.patrols[id]
	return route, ok, nil
}

func (m *Memory) ListPatrolRoutes(_ context.Context) ([]model.PatrolRoute, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]model.PatrolRoute, 0, len(m.patrols))
	for _, route := range m.patrols {
		out = append(out, route)
	}
	return out, nil
}

func (m *Memory) UpdatePatrolRoute(_ context.Context, route model.PatrolRoute) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.patrols[route.ID]; !ok {
		return model.ErrNotFound
	}
	m.patrols[route.ID] = route
	return nil
}

func (m *Memory) DeletePatrolRoute(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.patrols[id]; !ok {
		return model.ErrNotFound
	}
	delete(m.patrols, id)
	return nil
}

func (m *Memory) CreatePatrolSchedule(_ context.Context, schedule model.PatrolSchedule) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.schedules[schedule.ID] = schedule
	return nil
}

func (m *Memory) GetPatrolSchedule(_ context.Context, id string) (model.PatrolSchedule, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	schedule, ok := m.schedules[id]
	return schedule, ok, nil
}

func (m *Memory) ListPatrolSchedules(_ context.Context) ([]model.PatrolSchedule, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]model.PatrolSchedule, 0, len(m.schedules))
	for _, schedule := range m.schedules {
		out = append(out, schedule)
	}
	return out, nil
}

func (m *Memory) UpdatePatrolSchedule(_ context.Context, schedule model.PatrolSchedule) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.schedules[schedule.ID]; !ok {
		return model.ErrNotFound
	}
	m.schedules[schedule.ID] = schedule
	return nil
}

func (m *Memory) DeletePatrolSchedule(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.schedules[id]; !ok {
		return model.ErrNotFound
	}
	delete(m.schedules, id)
	return nil
}

func (m *Memory) CreatePatrolExecution(_ context.Context, execution model.PatrolExecution) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.executions[execution.ID] = execution
	return nil
}

func (m *Memory) GetPatrolExecution(_ context.Context, id string) (model.PatrolExecution, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	execution, ok := m.executions[id]
	return execution, ok, nil
}

func (m *Memory) ListPatrolExecutions(_ context.Context, filter model.PatrolExecutionFilter) ([]model.PatrolExecution, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]model.PatrolExecution, 0, len(m.executions))
	for _, execution := range m.executions {
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

func (m *Memory) DeletePatrolExecutions(_ context.Context, before time.Time, statuses []string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	allowed := make(map[string]struct{}, len(statuses))
	for _, status := range statuses {
		allowed[status] = struct{}{}
	}
	deleted := 0
	for id, execution := range m.executions {
		if _, ok := allowed[execution.Status]; !ok {
			continue
		}
		if execution.CreatedAt.After(before) {
			continue
		}
		delete(m.executions, id)
		deleted++
	}
	return deleted, nil
}

func (m *Memory) UpdatePatrolExecution(_ context.Context, execution model.PatrolExecution) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.executions[execution.ID]; !ok {
		return model.ErrNotFound
	}
	m.executions[execution.ID] = execution
	return nil
}

func (m *Memory) CreateRefreshToken(_ context.Context, token model.RefreshToken) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.refresh[token.ID] = token
	m.refreshByHash[token.TokenHash] = token.ID
	return nil
}

func (m *Memory) GetRefreshTokenByHash(_ context.Context, tokenHash string) (model.RefreshToken, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	id, ok := m.refreshByHash[tokenHash]
	if !ok {
		return model.RefreshToken{}, false, nil
	}
	return m.refresh[id], true, nil
}

func (m *Memory) RevokeRefreshToken(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	token, ok := m.refresh[id]
	if !ok {
		return model.ErrNotFound
	}
	token.Revoked = true
	m.refresh[id] = token
	return nil
}

func (m *Memory) RevokeUserRefreshTokens(_ context.Context, userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, token := range m.refresh {
		if token.UserID == userID {
			token.Revoked = true
			m.refresh[id] = token
		}
	}
	return nil
}

func (m *Memory) ListUserRefreshTokens(_ context.Context, userID string) ([]model.RefreshToken, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]model.RefreshToken, 0, 4)
	for _, token := range m.refresh {
		if token.UserID == userID {
			out = append(out, token)
		}
	}
	return out, nil
}

func (m *Memory) GetUserAccountByUsername(_ context.Context, username string) (model.UserAccount, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	id, ok := m.usernames[username]
	if !ok {
		return model.UserAccount{}, false, nil
	}
	account, exists := m.users[id]
	return account, exists, nil
}

func (m *Memory) GetUserAccountByID(_ context.Context, id string) (model.UserAccount, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	account, ok := m.users[id]
	return account, ok, nil
}

func (m *Memory) ListUserAccounts(context.Context) ([]model.UserAccount, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]model.UserAccount, 0, len(m.users))
	for _, account := range m.users {
		out = append(out, account)
	}
	return out, nil
}

func (m *Memory) CreateUserAccount(_ context.Context, account model.UserAccount) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.usernames[account.Username]; exists {
		return model.ErrConflict
	}
	m.users[account.ID] = account
	m.usernames[account.Username] = account.ID
	return nil
}

func (m *Memory) UpdateUserAccount(_ context.Context, account model.UserAccount) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.users[account.ID]; !exists {
		return model.ErrNotFound
	}
	m.users[account.ID] = account
	m.usernames[account.Username] = account.ID
	return nil
}

func (m *Memory) DeleteUserAccount(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	account, exists := m.users[id]
	if !exists {
		return model.ErrNotFound
	}
	delete(m.users, id)
	delete(m.usernames, account.Username)
	return nil
}

func (m *Memory) CreateDutyGroup(_ context.Context, group model.DutyGroup) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.duty = append(m.duty, group)
	return nil
}

func (m *Memory) GetDutyGroup(_ context.Context, id string) (model.DutyGroup, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for i := range m.duty {
		if m.duty[i].ID == id {
			return m.duty[i], nil
		}
	}
	return model.DutyGroup{}, ErrNotFound
}

func (m *Memory) ListDutyGroups(_ context.Context) ([]model.DutyGroup, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]model.DutyGroup(nil), m.duty...), nil
}

func (m *Memory) UpdateDutyGroup(_ context.Context, id string, group model.DutyGroup) (model.DutyGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.duty {
		if m.duty[i].ID != id {
			continue
		}
		group.ID = id
		group.CreatedAt = m.duty[i].CreatedAt
		group.UpdatedAt = time.Now().UTC()
		m.duty[i] = group
		return group, nil
	}
	return model.DutyGroup{}, ErrNotFound
}

func (m *Memory) DeleteDutyGroup(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.duty {
		if m.duty[i].ID == id {
			m.duty = append(m.duty[:i], m.duty[i+1:]...)
			return nil
		}
	}
	return ErrNotFound
}

func (m *Memory) GetSetting(_ context.Context, key string) ([]byte, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	value, ok := m.settings[key]
	if !ok {
		return nil, false, nil
	}
	return append([]byte(nil), value...), true, nil
}

func (m *Memory) SetSetting(_ context.Context, key string, value []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.settings[key] = append([]byte(nil), value...)
	return nil
}

func (m *Memory) CreateNotificationLog(_ context.Context, entry model.NotificationLog) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.logs = append(m.logs, entry)
	if len(m.logs) > 1000 {
		m.logs = append([]model.NotificationLog(nil), m.logs[len(m.logs)-1000:]...)
	}
	return nil
}

func (m *Memory) ListNotificationLog(_ context.Context, limit int) ([]model.NotificationLog, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if limit <= 0 {
		limit = 50
	}
	out := make([]model.NotificationLog, 0, limit)
	for i := len(m.logs) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, m.logs[i])
	}
	return out, nil
}

func (m *Memory) EscalateStale(_ context.Context, before time.Time, severities []string) ([]model.Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	escalated := make([]model.Event, 0, 4)
	for i := range m.events {
		event := &m.events[i]
		if event.Status == "closed" || event.EscalatedAt != nil {
			continue
		}
		if event.OccurredAt.After(before) {
			continue
		}
		if len(severities) > 0 && !containsString(severities, event.Severity) {
			continue
		}
		closed := now
		event.EscalatedAt = &closed
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
