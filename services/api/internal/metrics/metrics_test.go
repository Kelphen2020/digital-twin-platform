package metrics_test

import (
	"strings"
	"testing"

	"digital-twin-platform/api/internal/metrics"
)

func TestTextExposesRequiredSeries(t *testing.T) {
	meters := metrics.New()
	meters.Detections.Add(10)
	meters.Dropped.Add(1)
	meters.Event("warn")
	meters.Event("critical")
	meters.Escalations.Add(1)
	meters.NotificationsOK.Add(3)
	meters.NotificationsFailed.Add(1)
	meters.NotificationsSuppressed.Add(2)
	meters.DutyDispatches.Add(2)
	meters.DutySkipped.Add(1)
	meters.Orders.Add(5)
	meters.OrdersDeduped.Add(2)
	meters.ReEscalations.Add(1)
	meters.TrackWritten.Add(7)
	meters.TrackBroadcast.Add(6)
	meters.IngestRejected.Add(2)
	meters.IngestForwarded.Add(3)
	meters.IngestFallbackFailures.Add(1)
	meters.ObserveIngest(2500) // 2.5 ms in microseconds

	text := meters.Text(2, 5)
	for _, needle := range []string{
		"dsh_twin_detections_total 10",
		"dsh_twin_events_dropped_total 1",
		"dsh_twin_events_total{severity=\"warn\"} 1",
		"dsh_twin_escalations_total 1",
		"dsh_twin_ws_clients 2",
		"dsh_twin_notifications_total{result=\"ok\"} 3",
		"dsh_twin_orders_total 5",
		"dsh_twin_orders_deduped_total 2",
		"dsh_twin_re_escalations_total 1",
		"dsh_twin_track_points_written_total 7",
		"dsh_twin_track_points_broadcast_total 6",
		"dsh_twin_ingest_rejected_total 2",
		"dsh_twin_ingest_forwarded_total 3",
		"dsh_twin_ingest_fallback_failures_total 1",
		"dsh_twin_ingest_duration_seconds_count 1",
		"# TYPE dsh_twin_detections_total counter",
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("metrics text missing %q\n%s", needle, text)
		}
	}
	if !strings.Contains(text, "# HELP dsh_twin_detections_total") {
		t.Fatal("HELP lines missing")
	}
	// The histogram sum must reflect the recorded duration.
	if !strings.Contains(text, "dsh_twin_ingest_duration_seconds_sum 0.0025") {
		t.Fatalf("duration sum wrong:\n%s", text)
	}
}

func TestEmptyMetricsAreZero(t *testing.T) {
	text := metrics.New().Text(0, 0)
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}
		if !strings.HasSuffix(line, " 0") {
			t.Fatalf("expected zero-valued series, got %q", line)
		}
	}
}

// P35: per-tenant ingest and camera gauges are labelled and sorted.
func TestTenantMetricsExposition(t *testing.T) {
	m := metrics.New()
	m.RecordTenantIngest("tnt_b", 0)
	m.RecordTenantIngest("default", 0)
	m.RecordTenantIngest("", 3) // ignored
	m.SetTenantCamerasOnline("tnt_b", 2)
	m.SetTenantCamerasOnline("default", 6)
	m.SetTenantCamerasOnline("", 1) // ignored
	m.RecordTenantQuotaExceeded("tnt_b", "ingest_qps")
	m.ExportJobsPurged(4)

	text := m.Text(0, 6)
	for _, want := range []string{
		`dsh_twin_tenant_ingest_total{tenant="default"} 1`,
		`dsh_twin_tenant_ingest_total{tenant="tnt_b"} 1`,
		`dsh_twin_tenant_cameras_online{tenant="default"} 6`,
		`dsh_twin_tenant_cameras_online{tenant="tnt_b"} 2`,
		`dsh_twin_tenant_quota_exceeded_total{tenant="tnt_b",dimension="ingest_qps"} 1`,
		`dsh_twin_export_jobs_purged_total 4`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("metrics text missing %q\n%s", want, text)
		}
	}
	if got := m.TenantIngest()["default"]; got != 1 {
		t.Fatalf("tenant ingest map: %v", m.TenantIngest())
	}
	if got := m.TenantCameras()["tnt_b"]; got != 2 {
		t.Fatalf("tenant cameras map: %v", m.TenantCameras())
	}
}
