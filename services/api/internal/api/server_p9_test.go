package api_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"digital-twin-platform/api/internal/api"
	"digital-twin-platform/api/internal/auth"
	"digital-twin-platform/api/internal/metrics"
	"digital-twin-platform/api/internal/realtime"
	"digital-twin-platform/api/internal/rules"
	"digital-twin-platform/api/internal/store"
)

func newP9Server(t *testing.T) (*httptest.Server, *api.Server) {
	t.Helper()
	memory := store.NewMemory(2000)
	memory.Seed(store.SeedCameras(), store.SeedAreas())
	server := api.New(memory, rules.New(5*time.Second), realtime.NewHub(), metrics.New(),
		"test", auth.New("off", ""), "")
	ts := httptest.NewServer(server.Handler())
	t.Cleanup(ts.Close)
	return ts, server
}

func TestIngestRateLimit(t *testing.T) {
	ts, server := newP9Server(t)
	server.SetIngestGuard(api.NewRateLimiter(1, 1), 0)
	body := map[string]any{
		"camera_code": "CAM-004",
		"detections":  []map[string]any{{"class": "person", "confidence": 0.9}},
	}
	status, _ := doJSON(t, http.MethodPost, ts.URL+"/api/ingest/detections", body)
	if status != http.StatusAccepted {
		t.Fatalf("first ingest should pass, got %d", status)
	}
	status, response := doJSON(t, http.MethodPost, ts.URL+"/api/ingest/detections", body)
	if status != http.StatusTooManyRequests || !strings.Contains(string(response), "rate_limited") {
		t.Fatalf("second ingest should be 429, got %d %s", status, response)
	}
	_, metricsBody := doJSON(t, http.MethodGet, ts.URL+"/metrics", nil)
	if !strings.Contains(string(metricsBody), "dsh_twin_ingest_rejected_total 1") {
		t.Fatalf("rejected metric missing:\n%s", metricsBody)
	}
}

func TestIngestBodyLimit(t *testing.T) {
	ts, server := newP9Server(t)
	server.SetIngestGuard(nil, 16)
	status, response := doJSON(t, http.MethodPost, ts.URL+"/api/ingest/detections", map[string]any{
		"camera_code": "CAM-004",
		"detections":  []map[string]any{{"class": "person", "confidence": 0.9}},
	})
	if status != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d %s", status, response)
	}
}

func TestTrackBroadcastSampling(t *testing.T) {
	ts, server := newP9Server(t)
	server.SetTrackSample(2)
	status, response := doJSON(t, http.MethodPost, ts.URL+"/api/ingest/detections", map[string]any{
		"camera_code": "CAM-005",
		"detections": []map[string]any{
			{"class": "person", "confidence": 0.9, "track_id": "trk-a", "world": map[string]float64{"x": 1, "y": 2, "z": 0}},
			{"class": "person", "confidence": 0.9, "track_id": "trk-b", "world": map[string]float64{"x": 3, "y": 4, "z": 0}},
			{"class": "person", "confidence": 0.9, "track_id": "trk-c", "world": map[string]float64{"x": 5, "y": 6, "z": 0}},
			{"class": "person", "confidence": 0.9, "track_id": "trk-d", "world": map[string]float64{"x": 7, "y": 8, "z": 0}},
		},
	})
	if status != http.StatusAccepted {
		t.Fatalf("ingest: %d %s", status, response)
	}
	_, metricsBody := doJSON(t, http.MethodGet, ts.URL+"/metrics", nil)
	for _, needle := range []string{
		"dsh_twin_track_points_written_total 4",
		"dsh_twin_track_points_broadcast_total 2",
		"dsh_twin_ws_frames_total",
	} {
		if !strings.Contains(string(metricsBody), needle) {
			t.Fatalf("metrics missing %q:\n%s", needle, metricsBody)
		}
	}
}
