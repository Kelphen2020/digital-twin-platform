// Package metrics keeps the Prometheus counters exposed at /metrics.
package metrics

import (
	"fmt"
	"strings"
	"sync/atomic"
)

// Metrics is a lock-free counter set. Labels are fixed in P0 (severity only).
type Metrics struct {
	Detections atomic.Int64
	Dropped    atomic.Int64
	IngestUS   atomic.Int64 // sum of ingest durations in microseconds
	IngestN    atomic.Int64
	Info       atomic.Int64
	Warn       atomic.Int64
	Critical   atomic.Int64
}

// New returns an empty counter set.
func New() *Metrics { return &Metrics{} }

// ObserveIngest records one ingest handler duration in microseconds.
func (m *Metrics) ObserveIngest(microseconds int64) {
	m.IngestUS.Add(microseconds)
	m.IngestN.Add(1)
}

// Event records one produced event under its severity.
func (m *Metrics) Event(severity string) {
	switch severity {
	case "critical":
		m.Critical.Add(1)
	case "warn":
		m.Warn.Add(1)
	default:
		m.Info.Add(1)
	}
}

// Text renders the counters in Prometheus text exposition format.
func (m *Metrics) Text(wsClients, onlineCameras int) string {
	var b strings.Builder
	b.WriteString("# HELP dsh_twin_detections_total Detection batches received.\n")
	b.WriteString("# TYPE dsh_twin_detections_total counter\n")
	fmt.Fprintf(&b, "dsh_twin_detections_total %d\n", m.Detections.Load())

	b.WriteString("# HELP dsh_twin_events_total Events produced by the rule engine, by severity.\n")
	b.WriteString("# TYPE dsh_twin_events_total counter\n")
	fmt.Fprintf(&b, "dsh_twin_events_total{severity=\"info\"} %d\n", m.Info.Load())
	fmt.Fprintf(&b, "dsh_twin_events_total{severity=\"warn\"} %d\n", m.Warn.Load())
	fmt.Fprintf(&b, "dsh_twin_events_total{severity=\"critical\"} %d\n", m.Critical.Load())

	b.WriteString("# HELP dsh_twin_events_dropped_total Detections dropped by the rule engine.\n")
	b.WriteString("# TYPE dsh_twin_events_dropped_total counter\n")
	fmt.Fprintf(&b, "dsh_twin_events_dropped_total %d\n", m.Dropped.Load())

	b.WriteString("# HELP dsh_twin_ingest_duration_seconds Ingest handler duration.\n")
	b.WriteString("# TYPE dsh_twin_ingest_duration_seconds summary\n")
	n := m.IngestN.Load()
	sumSeconds := float64(m.IngestUS.Load()) / 1e6
	fmt.Fprintf(&b, "dsh_twin_ingest_duration_seconds_sum %g\n", sumSeconds)
	fmt.Fprintf(&b, "dsh_twin_ingest_duration_seconds_count %d\n", n)

	b.WriteString("# HELP dsh_twin_ws_clients Connected WebSocket clients.\n")
	b.WriteString("# TYPE dsh_twin_ws_clients gauge\n")
	fmt.Fprintf(&b, "dsh_twin_ws_clients %d\n", wsClients)

	b.WriteString("# HELP dsh_twin_cameras_online Cameras currently online.\n")
	b.WriteString("# TYPE dsh_twin_cameras_online gauge\n")
	fmt.Fprintf(&b, "dsh_twin_cameras_online %d\n", onlineCameras)
	return b.String()
}
