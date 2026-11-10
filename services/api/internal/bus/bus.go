// Package bus is the P5.0 event bus: envelopes are persisted by a driver and
// fanned out to subscribers, so live display/notification and replay share one
// path. The memory driver keeps a bounded ring; the jsonl driver appends to a
// durable log and can replay history after a restart.
package bus

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"digital-twin-platform/api/internal/model"
)

// Kinds of envelopes.
const (
	KindEventCreated   = "event_created"
	KindEventEscalated = "event_escalated"
)

// ErrRangeUnavailable means the requested offsets are no longer retained.
var ErrRangeUnavailable = errors.New("requested offsets are not retained by this bus driver")

// Envelope is one bus record.
type Envelope struct {
	Offset int64        `json:"offset"`
	Kind   string       `json:"kind"`
	At     time.Time    `json:"at"`
	Event  model.Event  `json:"event"`
	Order  *model.Order `json:"order,omitempty"`
	Source string       `json:"source"`
}

// Status is the admin-visible bus snapshot.
type Status struct {
	Driver        string     `json:"driver"`
	Path          string     `json:"path"`
	Published     int64      `json:"published"`
	Replayed      int64      `json:"replayed"`
	OldestOffset  int64      `json:"oldest_offset"`
	NextOffset    int64      `json:"next_offset"`
	LastPublishAt *time.Time `json:"last_publish_at,omitempty"`
	Subscribers   int        `json:"subscribers"`
}

// Driver persists and reads envelopes.
type Driver interface {
	Publish(envelope Envelope) (Envelope, error)
	ReadRange(from, to int64, limit int) ([]Envelope, error)
	Status() (published, oldest, next int64, path string)
}

// Handler receives one published envelope.
type Handler func(Envelope)

// Bus coordinates a driver and its subscribers.
type Bus struct {
	mu        sync.RWMutex
	driver    Driver
	handlers  []Handler
	published atomic.Int64
	replayed  atomic.Int64
	errors    atomic.Int64
	lastAt    atomic.Value // time.Time
}

// NewMemory builds a bus over an in-memory ring buffer.
func NewMemory(max int) *Bus {
	return &Bus{driver: &memoryDriver{max: max}}
}

// NewJSONL builds a bus over an append-only JSONL log under dir.
func NewJSONL(dir string) (*Bus, error) {
	driver, err := newJSONLDriver(dir)
	if err != nil {
		return nil, err
	}
	return &Bus{driver: driver}, nil
}

// Subscribe registers a handler invoked synchronously on publish.
func (b *Bus) Subscribe(handler Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers = append(b.handlers, handler)
}

// Publish persists an envelope and fans it out.
func (b *Bus) Publish(kind string, event model.Event, order *model.Order, source string) (Envelope, error) {
	envelope := Envelope{Kind: kind, At: time.Now().UTC(), Event: event, Order: order, Source: source}
	stored, err := b.driver.Publish(envelope)
	if err != nil {
		b.errors.Add(1)
		return Envelope{}, err
	}
	b.published.Add(1)
	b.lastAt.Store(stored.At)
	b.mu.RLock()
	handlers := append([]Handler(nil), b.handlers...)
	b.mu.RUnlock()
	for _, handler := range handlers {
		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					b.errors.Add(1)
				}
			}()
			handler(stored)
		}()
	}
	return stored, nil
}

// ReadRange reads a stored offset range for replay.
func (b *Bus) ReadRange(from, to int64, limit int) ([]Envelope, error) {
	return b.driver.ReadRange(from, to, limit)
}

// MarkReplayed records one replayed envelope for metrics.
func (b *Bus) MarkReplayed(count int64) {
	b.replayed.Add(count)
}

// AddError records one replay/subscriber error.
func (b *Bus) AddError() { b.errors.Add(1) }

// Errors is the current error counter.
func (b *Bus) Errors() int64 { return b.errors.Load() }

// Status snapshots the bus for the admin API.
func (b *Bus) Status() Status {
	_, oldest, next, path := b.driver.Status()
	b.mu.RLock()
	subscribers := len(b.handlers)
	b.mu.RUnlock()
	status := Status{
		Driver:       driverName(b.driver),
		Path:         path,
		Published:    b.published.Load(),
		Replayed:     b.replayed.Load(),
		OldestOffset: oldest,
		NextOffset:   next,
		Subscribers:  subscribers,
	}
	if value, ok := b.lastAt.Load().(time.Time); ok {
		last := value
		status.LastPublishAt = &last
	}
	return status
}

// MetricsText renders bus counters in Prometheus text format.
func (b *Bus) MetricsText(lag int64) string {
	return fmt.Sprintf(`# HELP dsh_twin_bus_published_total Envelopes published to the bus.
# TYPE dsh_twin_bus_published_total counter
dsh_twin_bus_published_total %d
# HELP dsh_twin_bus_replayed_total Envelopes replayed from the bus.
# TYPE dsh_twin_bus_replayed_total counter
dsh_twin_bus_replayed_total %d
# HELP dsh_twin_bus_errors_total Bus publish/replay/subscriber errors.
# TYPE dsh_twin_bus_errors_total counter
dsh_twin_bus_errors_total %d
# HELP dsh_twin_bus_lag Envelopes after the last consumed offset.
# TYPE dsh_twin_bus_lag gauge
dsh_twin_bus_lag %d
`, b.published.Load(), b.replayed.Load(), b.errors.Load(), lag)
}

func driverName(driver Driver) string {
	switch driver.(type) {
	case *memoryDriver:
		return "memory"
	case *jsonlDriver:
		return "jsonl"
	}
	return "unknown"
}

// ---- memory driver ------------------------------------------------------

type memoryDriver struct {
	mu     sync.Mutex
	max    int
	base   int64
	next   int64
	buffer []Envelope
}

func (d *memoryDriver) Publish(envelope Envelope) (Envelope, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.max <= 0 {
		d.max = 1000
	}
	envelope.Offset = d.next
	d.next++
	d.buffer = append(d.buffer, envelope)
	if len(d.buffer) > d.max {
		drop := len(d.buffer) - d.max
		d.buffer = d.buffer[drop:]
		d.base += int64(drop)
	}
	return envelope, nil
}

func (d *memoryDriver) ReadRange(from, to int64, limit int) ([]Envelope, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.base > 0 && from < d.base {
		return nil, ErrRangeUnavailable
	}
	if limit <= 0 {
		limit = 500
	}
	out := make([]Envelope, 0, limit)
	for _, envelope := range d.buffer {
		if envelope.Offset < from {
			continue
		}
		if to >= 0 && envelope.Offset > to {
			break
		}
		out = append(out, envelope)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (d *memoryDriver) Status() (published, oldest, next int64, path string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.next, d.base, d.next, ""
}

// ---- jsonl driver -------------------------------------------------------

type jsonlDriver struct {
	mu   sync.Mutex
	path string
	file *os.File
	next int64
}

func newJSONLDriver(dir string) (*jsonlDriver, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "events.jsonl")
	driver := &jsonlDriver{path: path}
	if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
		scanner := bufio.NewScanner(bytes.NewReader(data))
		scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
		count := int64(0)
		for scanner.Scan() {
			if len(scanner.Bytes()) > 0 {
				count++
			}
		}
		driver.next = count
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	driver.file = file
	return driver, nil
}

func (d *jsonlDriver) Publish(envelope Envelope) (Envelope, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	envelope.Offset = d.next
	raw, err := json.Marshal(envelope)
	if err != nil {
		return Envelope{}, err
	}
	if _, err := d.file.Write(append(raw, '\n')); err != nil {
		return Envelope{}, err
	}
	d.next++
	return envelope, nil
}

func (d *jsonlDriver) ReadRange(from, to int64, limit int) ([]Envelope, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if limit <= 0 {
		limit = 500
	}
	file, err := os.Open(d.path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	out := make([]Envelope, 0, limit)
	var offset int64
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		current := offset
		offset++
		if current < from {
			continue
		}
		if to >= 0 && current > to {
			break
		}
		var envelope Envelope
		if err := json.Unmarshal(line, &envelope); err != nil {
			continue
		}
		if envelope.Offset == 0 {
			envelope.Offset = current
		}
		out = append(out, envelope)
		if len(out) >= limit {
			break
		}
	}
	return out, scanner.Err()
}

func (d *jsonlDriver) Status() (published, oldest, next int64, path string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.next, 0, d.next, d.path
}
