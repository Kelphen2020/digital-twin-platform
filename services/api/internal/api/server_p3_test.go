package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"digital-twin-platform/api/internal/api"
	"digital-twin-platform/api/internal/auth"
	"digital-twin-platform/api/internal/metrics"
	"digital-twin-platform/api/internal/realtime"
	"digital-twin-platform/api/internal/rules"
	"digital-twin-platform/api/internal/store"
)

// newP3Server builds an auth-on server whose workers are started by the test.
func newP3Server(t *testing.T) (*httptest.Server, *api.Server) {
	t.Helper()
	memory := store.NewMemory(2000)
	memory.Seed(store.SeedCameras(), store.SeedAreas())
	server := api.New(memory, rules.New(5*time.Second), realtime.NewHub(), metrics.New(),
		"test", auth.New("on", "test-secret"), "")
	server.LoadSettings(context.Background())
	ts := httptest.NewServer(server.Handler())
	t.Cleanup(ts.Close)
	return ts, server
}

func ingestAt(t *testing.T, ts *httptest.Server, camera, class string, confidence float64, occurred time.Time) (string, int) {
	t.Helper()
	status, body := doJSON(t, http.MethodPost, ts.URL+"/api/ingest/detections", map[string]any{
		"camera_code": camera,
		"ts":          occurred.UTC().Format(time.RFC3339),
		"detections": []map[string]any{{
			"class": class, "confidence": confidence, "bbox": []float64{0.4, 0.3, 0.6, 0.8},
			"world": map[string]float64{"x": 1, "y": 2, "z": 0},
		}},
	})
	if status != http.StatusAccepted {
		t.Fatalf("ingest: %d %s", status, body)
	}
	var response struct {
		Accepted int `json:"accepted"`
		Events   []struct {
			ID string `json:"id"`
		} `json:"events"`
	}
	_ = json.Unmarshal(body, &response)
	if response.Accepted == 1 {
		return response.Events[0].ID, 1
	}
	return "", 0
}

func TestDataLevelFilteringByArea(t *testing.T) {
	ts, _ := newP3Server(t)
	admin := login(t, ts, "admin", "Admin@123456")
	operator := login(t, ts, "operator", "Operator@123")

	outOfScope, accepted := ingestAt(t, ts, "CAM-002", "intrusion", 0.95, time.Now())
	if accepted != 1 {
		t.Fatal("failed to create out-of-scope event")
	}
	inScope, accepted := ingestAt(t, ts, "CAM-001", "smoke", 0.95, time.Now())
	if accepted != 1 {
		t.Fatal("failed to create in-scope event")
	}

	status, body := authorized(t, http.MethodGet, ts.URL+"/api/cameras", operator, nil)
	if status != http.StatusOK {
		t.Fatalf("operator cameras: %d %s", status, body)
	}
	for _, code := range []string{"CAM-002", "CAM-003", "CAM-004"} {
		if strings.Contains(string(body), code) {
			t.Fatalf("operator should not see %s: %s", code, body)
		}
	}
	for _, code := range []string{"CAM-001", "CAM-005", "CAM-006"} {
		if !strings.Contains(string(body), code) {
			t.Fatalf("operator should see %s: %s", code, body)
		}
	}

	status, body = authorized(t, http.MethodGet, ts.URL+"/api/events?limit=100", operator, nil)
	if status != http.StatusOK || strings.Contains(string(body), outOfScope) {
		t.Fatalf("operator events leak out-of-scope event: %d %s", status, body)
	}
	if !strings.Contains(string(body), inScope) {
		t.Fatalf("operator events missing in-scope event: %s", body)
	}
	status, body = authorized(t, http.MethodGet, ts.URL+"/api/events/"+outOfScope, operator, nil)
	if status != http.StatusForbidden || !strings.Contains(string(body), "out_of_scope") {
		t.Fatalf("operator event detail: %d %s", status, body)
	}
	status, _ = authorized(t, http.MethodGet, ts.URL+"/api/audit", operator, nil)
	if status != http.StatusForbidden {
		t.Fatalf("operator audit should be 403, got %d", status)
	}
	status, _ = authorized(t, http.MethodGet, ts.URL+"/api/notifications", operator, nil)
	if status != http.StatusForbidden {
		t.Fatalf("operator notifications should be 403, got %d", status)
	}

	// Admin sees everything and both stats scopes.
	status, body = authorized(t, http.MethodGet, ts.URL+"/api/events?limit=100", admin, nil)
	if status != http.StatusOK || !strings.Contains(string(body), outOfScope) {
		t.Fatalf("admin events missing out-of-scope event: %d %s", status, body)
	}
	_, adminStats := authorized(t, http.MethodGet, ts.URL+"/api/stats/summary", admin, nil)
	_, operatorStats := authorized(t, http.MethodGet, ts.URL+"/api/stats/summary", operator, nil)
	var adminSummary, operatorSummary struct {
		Totals struct {
			Events int64 `json:"events"`
		} `json:"totals"`
	}
	_ = json.Unmarshal(adminStats, &adminSummary)
	_ = json.Unmarshal(operatorStats, &operatorSummary)
	if operatorSummary.Totals.Events >= adminSummary.Totals.Events {
		t.Fatalf("operator stats should exclude out-of-scope events: operator=%d admin=%d",
			operatorSummary.Totals.Events, adminSummary.Totals.Events)
	}
}

func TestSettingsEscalationAndWebhook(t *testing.T) {
	ts, server := newP3Server(t)
	admin := login(t, ts, "admin", "Admin@123456")

	var mu sync.Mutex
	var received []string
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		received = append(received, string(body))
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer receiver.Close()

	status, body := authorized(t, http.MethodPut, ts.URL+"/api/settings/notifications", admin, map[string]any{
		"enabled": true, "webhook_url": receiver.URL + "/hook", "format": "generic",
		"min_severity": "warn", "timeout_seconds": 5,
	})
	if status != http.StatusOK {
		t.Fatalf("put notifications: %d %s", status, body)
	}
	status, body = authorized(t, http.MethodPut, ts.URL+"/api/settings/escalation", admin, map[string]any{
		"enabled": true, "after_seconds": 1, "severities": []string{"warn", "critical"},
	})
	if status != http.StatusOK {
		t.Fatalf("put escalation: %d %s", status, body)
	}
	status, body = authorized(t, http.MethodGet, ts.URL+"/api/settings/escalation", admin, nil)
	if status != http.StatusOK || !strings.Contains(string(body), `"after_seconds":1`) {
		t.Fatalf("get escalation: %d %s", status, body)
	}

	// Invalid settings are rejected.
	status, _ = authorized(t, http.MethodPut, ts.URL+"/api/settings/notifications", admin, map[string]any{
		"enabled": true, "webhook_url": "ftp://nope", "format": "generic",
	})
	if status != http.StatusBadRequest {
		t.Fatalf("invalid webhook url should be 400, got %d", status)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go server.RunEscalation(ctx, 50*time.Millisecond)

	// A stale warn event escalates and notifies.
	eventID, accepted := ingestAt(t, ts, "CAM-001", "intrusion", 0.95, time.Now().Add(-10*time.Second))
	if accepted != 1 {
		t.Fatal("failed to create stale event")
	}
	var escalated bool
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		status, body = authorized(t, http.MethodGet, ts.URL+"/api/events/"+eventID, admin, nil)
		if status == http.StatusOK && strings.Contains(string(body), "escalated_at") {
			escalated = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !escalated {
		t.Fatalf("event %s did not escalate", eventID)
	}

	// Notification delivery is recorded and reached the receiver.
	notified := false
	deadline = time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		status, body = authorized(t, http.MethodGet, ts.URL+"/api/notifications?limit=50", admin, nil)
		if status == http.StatusOK && strings.Contains(string(body), eventID) && strings.Contains(string(body), `"ok":true`) {
			notified = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !notified {
		t.Fatalf("notification for %s not recorded: %s", eventID, body)
	}
	mu.Lock()
	defer mu.Unlock()
	found := false
	for _, payload := range received {
		if strings.Contains(payload, eventID) {
			found = true
		}
	}
	if !found {
		t.Fatalf("receiver never saw event %s; payloads=%v", eventID, received)
	}

	// Escalation is also audited with actor=system.
	status, body = authorized(t, http.MethodGet, ts.URL+"/api/audit?limit=50", admin, nil)
	if status != http.StatusOK || !strings.Contains(string(body), "event.escalate") {
		t.Fatalf("audit missing event.escalate: %d %s", status, body)
	}

	// Restore disabled notifications to keep other tests quiet.
	status, body = authorized(t, http.MethodPut, ts.URL+"/api/settings/notifications", admin, map[string]any{
		"enabled": false, "webhook_url": "", "format": "generic", "min_severity": "warn", "timeout_seconds": 5,
	})
	if status != http.StatusOK {
		t.Fatalf("disable notifications: %d %s", status, body)
	}
	fmt.Fprint(io.Discard, body)
}
