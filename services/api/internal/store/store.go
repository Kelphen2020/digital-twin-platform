// Package store defines persistence for the P0 API. The in-memory
// implementation is used when DATABASE_URL is empty (smoke tests, laptop demo);
// the PostgreSQL implementation is used by docker compose and production.
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
	ListEvents(ctx context.Context, filter model.EventFilter) ([]model.Event, error)
	CreateEvents(ctx context.Context, events []model.Event) error
	AckEvent(ctx context.Context, id string) (model.Event, error)
	Counts(ctx context.Context) (model.Counts, error)
}

// Memory is the in-memory Store used without a database.
type Memory struct {
	mu      sync.RWMutex
	cameras map[string]*model.Camera
	order   []string
	areas   []model.Area
	events  []model.Event
	max     int
}

// NewMemory creates an empty in-memory store that retains at most maxEvents.
func NewMemory(maxEvents int) *Memory {
	if maxEvents < 100 {
		maxEvents = 100
	}
	return &Memory{cameras: make(map[string]*model.Camera), max: maxEvents}
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
		out = append(out, event)
	}
	return out, nil
}

func (m *Memory) CreateEvents(_ context.Context, events []model.Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, events...)
	if len(m.events) > m.max {
		m.events = append([]model.Event(nil), m.events[len(m.events)-m.max:]...)
	}
	return nil
}

func (m *Memory) AckEvent(_ context.Context, id string) (model.Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.events {
		if m.events[i].ID == id {
			m.events[i].Status = "acked"
			return m.events[i], nil
		}
	}
	return model.Event{}, ErrNotFound
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
