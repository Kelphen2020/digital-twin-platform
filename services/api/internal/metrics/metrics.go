// Package metrics keeps the Prometheus counters exposed at /metrics.
package metrics

import (
	"fmt"
	"sort"
	"strings"
	"sync"
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

	NotificationsOK         atomic.Int64
	NotificationsFailed     atomic.Int64
	NotificationsSuppressed atomic.Int64
	Escalations             atomic.Int64
	Orders                  atomic.Int64
	DutyDispatches          atomic.Int64
	DutySkipped             atomic.Int64
	OrdersDeduped           atomic.Int64
	ReEscalations           atomic.Int64
	TrackWritten            atomic.Int64
	TrackBroadcast          atomic.Int64
	IngestRejected          atomic.Int64
	IngestForwarded         atomic.Int64
	IngestFallbackFailures  atomic.Int64

	tenantMu       sync.Mutex
	tenantRequests map[string]int64

	quotaMu       sync.Mutex
	quotaExceeded map[string]int64

	exportPurgedMu sync.Mutex
	exportPurged   int64

	// P35: per-tenant ingest volume and camera gauges.
	ingestMu      sync.Mutex
	tenantIngest  map[string]int64
	cameraTenants map[string]int64
}

// New returns an empty counter set.
func New() *Metrics { return &Metrics{tenantRequests: make(map[string]int64)} }

// RecordTenantRequest counts one request for the resolved tenant (P27.0).
func (m *Metrics) RecordTenantRequest(tenant string) {
	if tenant == "" {
		return
	}
	m.tenantMu.Lock()
	if m.tenantRequests == nil {
		m.tenantRequests = make(map[string]int64)
	}
	m.tenantRequests[tenant]++
	m.tenantMu.Unlock()
}

// RecordTenantIngest counts accepted ingest requests per tenant (P35.0).
func (m *Metrics) RecordTenantIngest(tenant string, detections int) {
	if tenant == "" {
		return
	}
	m.ingestMu.Lock()
	if m.tenantIngest == nil {
		m.tenantIngest = make(map[string]int64)
	}
	m.tenantIngest[tenant]++
	m.ingestMu.Unlock()
	if detections > 0 {
		m.Detections.Add(int64(detections))
	}
}

// SetTenantCamerasOnline publishes the online camera gauge per tenant (P35.0).
func (m *Metrics) SetTenantCamerasOnline(tenant string, online int) {
	if tenant == "" {
		return
	}
	m.ingestMu.Lock()
	if m.cameraTenants == nil {
		m.cameraTenants = make(map[string]int64)
	}
	m.cameraTenants[tenant] = int64(online)
	m.ingestMu.Unlock()
}

// TenantIngest returns per-tenant accepted ingest counts.
func (m *Metrics) TenantIngest() map[string]int64 {
	m.ingestMu.Lock()
	defer m.ingestMu.Unlock()
	out := make(map[string]int64, len(m.tenantIngest))
	for key, value := range m.tenantIngest {
		out[key] = value
	}
	return out
}

// TenantCameras returns per-tenant online camera gauges.
func (m *Metrics) TenantCameras() map[string]int64 {
	m.ingestMu.Lock()
	defer m.ingestMu.Unlock()
	out := make(map[string]int64, len(m.cameraTenants))
	for key, value := range m.cameraTenants {
		out[key] = value
	}
	return out
}

// ExportJobsPurged adds to the export-retention purge counter (P33.0).
func (m *Metrics) ExportJobsPurged(count int64) {
	if count <= 0 {
		return
	}
	m.exportPurgedMu.Lock()
	m.exportPurged += count
	m.exportPurgedMu.Unlock()
}

// ExportJobsPurgedTotal returns how many export jobs were pruned.
func (m *Metrics) ExportJobsPurgedTotal() int64 {
	m.exportPurgedMu.Lock()
	defer m.exportPurgedMu.Unlock()
	return m.exportPurged
}

// RecordTenantQuotaExceeded counts one quota rejection for a tenant dimension
// (P30.0), e.g. dimension "users", "events", "ingest_qps" or "cameras".
func (m *Metrics) RecordTenantQuotaExceeded(tenant, dimension string) {
	if tenant == "" || dimension == "" {
		return
	}
	m.quotaMu.Lock()
	if m.quotaExceeded == nil {
		m.quotaExceeded = make(map[string]int64)
	}
	m.quotaExceeded[tenant+"\x00"+dimension]++
	m.quotaMu.Unlock()
}

// TenantQuotaExceeded returns a copy of quota counters keyed tenant\x00dimension.
func (m *Metrics) TenantQuotaExceeded() map[string]int64 {
	m.quotaMu.Lock()
	defer m.quotaMu.Unlock()
	out := make(map[string]int64, len(m.quotaExceeded))
	for key, count := range m.quotaExceeded {
		out[key] = count
	}
	return out
}

// TenantRequests returns a copy of the per-tenant request counters.
func (m *Metrics) TenantRequests() map[string]int64 {
	m.tenantMu.Lock()
	defer m.tenantMu.Unlock()
	out := make(map[string]int64, len(m.tenantRequests))
	for tenant, count := range m.tenantRequests {
		out[tenant] = count
	}
	return out
}

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

	b.WriteString("# HELP dsh_twin_notifications_total Webhook delivery outcomes.\n")
	b.WriteString("# TYPE dsh_twin_notifications_total counter\n")
	fmt.Fprintf(&b, "dsh_twin_notifications_total{result=\"ok\"} %d\n", m.NotificationsOK.Load())
	fmt.Fprintf(&b, "dsh_twin_notifications_total{result=\"failed\"} %d\n", m.NotificationsFailed.Load())

	b.WriteString("# HELP dsh_twin_notifications_suppressed_total Deliveries suppressed by the silence window.\n")
	b.WriteString("# TYPE dsh_twin_notifications_suppressed_total counter\n")
	fmt.Fprintf(&b, "dsh_twin_notifications_suppressed_total %d\n", m.NotificationsSuppressed.Load())

	b.WriteString("# HELP dsh_twin_escalations_total Escalated events.\n")
	b.WriteString("# TYPE dsh_twin_escalations_total counter\n")
	fmt.Fprintf(&b, "dsh_twin_escalations_total %d\n", m.Escalations.Load())

	b.WriteString("# HELP dsh_twin_orders_total Work orders created.\n")
	b.WriteString("# TYPE dsh_twin_orders_total counter\n")
	fmt.Fprintf(&b, "dsh_twin_orders_total %d\n", m.Orders.Load())

	b.WriteString("# HELP dsh_twin_duty_dispatches_total Escalations auto-dispatched to a duty group.\n")
	b.WriteString("# TYPE dsh_twin_duty_dispatches_total counter\n")
	fmt.Fprintf(&b, "dsh_twin_duty_dispatches_total %d\n", m.DutyDispatches.Load())

	b.WriteString("# HELP dsh_twin_duty_skipped_total Escalations without an on-duty group.\n")
	b.WriteString("# TYPE dsh_twin_duty_skipped_total counter\n")
	fmt.Fprintf(&b, "dsh_twin_duty_skipped_total %d\n", m.DutySkipped.Load())

	b.WriteString("# HELP dsh_twin_orders_deduped_total Auto-dispatch attempts skipped by open-order dedupe.\n")
	b.WriteString("# TYPE dsh_twin_orders_deduped_total counter\n")
	fmt.Fprintf(&b, "dsh_twin_orders_deduped_total %d\n", m.OrdersDeduped.Load())

	b.WriteString("# HELP dsh_twin_re_escalations_total Events escalated a second time.\n")
	b.WriteString("# TYPE dsh_twin_re_escalations_total counter\n")
	fmt.Fprintf(&b, "dsh_twin_re_escalations_total %d\n", m.ReEscalations.Load())

	b.WriteString("# HELP dsh_twin_track_points_written_total Track points persisted.\n")
	b.WriteString("# TYPE dsh_twin_track_points_written_total counter\n")
	fmt.Fprintf(&b, "dsh_twin_track_points_written_total %d\n", m.TrackWritten.Load())

	b.WriteString("# HELP dsh_twin_track_points_broadcast_total Track points broadcast over WebSocket.\n")
	b.WriteString("# TYPE dsh_twin_track_points_broadcast_total counter\n")
	fmt.Fprintf(&b, "dsh_twin_track_points_broadcast_total %d\n", m.TrackBroadcast.Load())

	b.WriteString("# HELP dsh_twin_ingest_rejected_total Ingest requests rejected by the rate limiter.\n")
	b.WriteString("# TYPE dsh_twin_ingest_rejected_total counter\n")
	fmt.Fprintf(&b, "dsh_twin_ingest_rejected_total %d\n", m.IngestRejected.Load())

	b.WriteString("# HELP dsh_twin_ingest_forwarded_total Ingest requests forwarded to the fallback replica.\n")
	b.WriteString("# TYPE dsh_twin_ingest_forwarded_total counter\n")
	fmt.Fprintf(&b, "dsh_twin_ingest_forwarded_total %d\n", m.IngestForwarded.Load())

	b.WriteString("# HELP dsh_twin_ingest_fallback_failures_total Failed forwards to the fallback replica.\n")
	b.WriteString("# TYPE dsh_twin_ingest_fallback_failures_total counter\n")
	fmt.Fprintf(&b, "dsh_twin_ingest_fallback_failures_total %d\n", m.IngestFallbackFailures.Load())

	b.WriteString("# HELP dsh_twin_cameras_online Cameras currently online.\n")
	b.WriteString("# TYPE dsh_twin_cameras_online gauge\n")
	fmt.Fprintf(&b, "dsh_twin_cameras_online %d\n", onlineCameras)
	b.WriteString("# HELP dsh_twin_tenant_requests_total API requests received per tenant (P27.0).\n")
	b.WriteString("# TYPE dsh_twin_tenant_requests_total counter\n")
	tenants := m.TenantRequests()
	names := make([]string, 0, len(tenants))
	for tenant := range tenants {
		names = append(names, tenant)
	}
	sort.Strings(names)
	for _, tenant := range names {
		fmt.Fprintf(&b, "dsh_twin_tenant_requests_total{tenant=%q} %d\n", tenant, tenants[tenant])
	}
	ingests := m.TenantIngest()
	ingestKeys := make([]string, 0, len(ingests))
	for key := range ingests {
		ingestKeys = append(ingestKeys, key)
	}
	sort.Strings(ingestKeys)
	b.WriteString("# HELP dsh_twin_tenant_ingest_total Accepted ingest requests per tenant (P35.0).\n")
	b.WriteString("# TYPE dsh_twin_tenant_ingest_total counter\n")
	for _, tenant := range ingestKeys {
		fmt.Fprintf(&b, "dsh_twin_tenant_ingest_total{tenant=%q} %d\n", tenant, ingests[tenant])
	}
	cameras := m.TenantCameras()
	cameraKeys := make([]string, 0, len(cameras))
	for key := range cameras {
		cameraKeys = append(cameraKeys, key)
	}
	sort.Strings(cameraKeys)
	b.WriteString("# HELP dsh_twin_tenant_cameras_online Online cameras per tenant (P35.0).\n")
	b.WriteString("# TYPE dsh_twin_tenant_cameras_online gauge\n")
	for _, tenant := range cameraKeys {
		fmt.Fprintf(&b, "dsh_twin_tenant_cameras_online{tenant=%q} %d\n", tenant, cameras[tenant])
	}
	fmt.Fprintf(&b, "# HELP dsh_twin_export_jobs_purged_total Finished export jobs pruned by retention (P33.0).\n")
	fmt.Fprintf(&b, "# TYPE dsh_twin_export_jobs_purged_total counter\n")
	fmt.Fprintf(&b, "dsh_twin_export_jobs_purged_total %d\n", m.ExportJobsPurgedTotal())
	b.WriteString("# HELP dsh_twin_tenant_quota_exceeded_total Tenant quota rejections by dimension (P30.0).\n")
	b.WriteString("# TYPE dsh_twin_tenant_quota_exceeded_total counter\n")
	quotas := m.TenantQuotaExceeded()
	quotaKeys := make([]string, 0, len(quotas))
	for key := range quotas {
		quotaKeys = append(quotaKeys, key)
	}
	sort.Strings(quotaKeys)
	for _, key := range quotaKeys {
		parts := strings.SplitN(key, "\x00", 2)
		if len(parts) != 2 {
			continue
		}
		fmt.Fprintf(&b, "dsh_twin_tenant_quota_exceeded_total{tenant=%q,dimension=%q} %d\n",
			parts[0], parts[1], quotas[key])
	}
	return b.String()
}
