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
