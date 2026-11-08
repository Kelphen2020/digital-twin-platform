package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDutyGroupsCRUDAndResolution(t *testing.T) {
	ts, _ := newP3Server(t)
	admin := login(t, ts, "admin", "Admin@123456")
	operator := login(t, ts, "operator", "Operator@123")

	status, body := authorized(t, http.MethodGet, ts.URL+"/api/users", admin, nil)
	if status != http.StatusOK || !strings.Contains(string(body), `"username":"operator"`) {
		t.Fatalf("users: %d %s", status, body)
	}

	status, body = authorized(t, http.MethodPost, ts.URL+"/api/duty/groups", admin, map[string]any{
		"name": "东门值班组", "areas": []string{"area_gate"}, "operators": []string{"operator"}, "active": true,
	})
	if status != http.StatusCreated {
		t.Fatalf("create duty: %d %s", status, body)
	}
	var group struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	_ = json.Unmarshal(body, &group)

	status, body = authorized(t, http.MethodGet, ts.URL+"/api/duty/current?area_id=area_gate", operator, nil)
	if status != http.StatusOK || !strings.Contains(string(body), group.ID) {
		t.Fatalf("current duty gate: %d %s", status, body)
	}
	status, body = authorized(t, http.MethodGet, ts.URL+"/api/duty/current?area_id=area_parking", operator, nil)
	if status != http.StatusOK || !strings.Contains(string(body), `"group":null`) {
		t.Fatalf("current duty parking should be null: %d %s", status, body)
	}
	status, _ = authorized(t, http.MethodPost, ts.URL+"/api/duty/groups", operator, map[string]any{"name": "x"})
	if status != http.StatusForbidden {
		t.Fatalf("operator duty create should be 403, got %d", status)
	}
	status, body = authorized(t, http.MethodPatch, ts.URL+"/api/duty/groups/"+group.ID, admin, map[string]any{"active": false})
	if status != http.StatusOK || !strings.Contains(string(body), `"active":false`) {
		t.Fatalf("patch duty: %d %s", status, body)
	}
	status, body = authorized(t, http.MethodGet, ts.URL+"/api/duty/current?area_id=area_gate", operator, nil)
	if status != http.StatusOK || !strings.Contains(string(body), `"group":null`) {
		t.Fatalf("inactive duty should not resolve: %d %s", status, body)
	}
	status, _ = authorized(t, http.MethodDelete, ts.URL+"/api/duty/groups/"+group.ID, admin, nil)
	if status != http.StatusNoContent {
		t.Fatalf("delete duty: %d", status)
	}
}

func TestEscalationAutoDispatchOrder(t *testing.T) {
	ts, server := newP3Server(t)
	admin := login(t, ts, "admin", "Admin@123456")

	// Duty group covering the event camera's area.
	status, body := authorized(t, http.MethodPost, ts.URL+"/api/duty/groups", admin, map[string]any{
		"name": "东门自动派单组", "areas": []string{"area_gate"}, "operators": []string{"operator"}, "active": true,
	})
	if status != http.StatusCreated {
		t.Fatalf("create duty: %d %s", status, body)
	}
	status, body = authorized(t, http.MethodPut, ts.URL+"/api/settings/escalation", admin, map[string]any{
		"enabled": true, "after_seconds": 1, "severities": []string{"warn", "critical"}, "auto_dispatch": true,
	})
	if status != http.StatusOK {
		t.Fatalf("put escalation: %d %s", status, body)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go server.RunEscalation(ctx, 50*time.Millisecond)

	eventID, accepted := ingestAt(t, ts, "CAM-001", "intrusion", 0.95, time.Now().Add(-10*time.Second))
	if accepted != 1 {
		t.Fatal("failed to create stale event")
	}

	deadline := time.Now().Add(10 * time.Second)
	dispatched := false
	for time.Now().Before(deadline) {
		status, body = authorized(t, http.MethodGet, ts.URL+"/api/orders?limit=20", admin, nil)
		if status == http.StatusOK && strings.Contains(string(body), eventID) &&
			strings.Contains(string(body), "东门自动派单组") && strings.Contains(string(body), "自动派单") {
			dispatched = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !dispatched {
		t.Fatalf("auto dispatch order for %s not found: %s", eventID, body)
	}

	status, body = authorized(t, http.MethodGet, ts.URL+"/api/audit?limit=100", admin, nil)
	if status != http.StatusOK || !strings.Contains(string(body), "order.auto_dispatch") {
		t.Fatalf("audit missing auto dispatch: %d %s", status, body)
	}
	_, metricsBody := authorized(t, http.MethodGet, ts.URL+"/metrics", admin, nil)
	for _, needle := range []string{"dsh_twin_duty_dispatches_total", "dsh_twin_orders_total", "dsh_twin_escalations_total"} {
		if !strings.Contains(string(metricsBody), needle) {
			t.Fatalf("metrics missing %q:\n%s", needle, metricsBody)
		}
	}

	// Restore defaults and drop the test group.
	_, _ = authorized(t, http.MethodPut, ts.URL+"/api/settings/escalation", admin, map[string]any{
		"enabled": true, "after_seconds": 300, "severities": []string{"warn", "critical"}, "auto_dispatch": true,
	})
}

func TestNotificationRetryAndSilence(t *testing.T) {
	ts, _ := newP3Server(t)
	admin := login(t, ts, "admin", "Admin@123456")

	var attempts atomic.Int64
	var succeededOnce atomic.Bool
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt := attempts.Add(1)
		if attempt <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		succeededOnce.Store(true)
		w.WriteHeader(http.StatusOK)
	}))
	defer receiver.Close()

	status, body := authorized(t, http.MethodPut, ts.URL+"/api/settings/notifications", admin, map[string]any{
		"enabled": true, "webhook_url": receiver.URL + "/hook", "format": "generic",
		"min_severity": "warn", "timeout_seconds": 5,
		"max_attempts": 3, "backoff_seconds": 0, "silence_seconds": 0,
	})
	if status != http.StatusOK {
		t.Fatalf("put notifications: %d %s", status, body)
	}

	eventID, accepted := ingestAt(t, ts, "CAM-005", "intrusion", 0.95, time.Now())
	if accepted != 1 {
		t.Fatal("failed to create event for retry test")
	}
	var delivered struct {
		Items []struct {
			EventID    string `json:"event_id"`
			OK         bool   `json:"ok"`
			StatusCode int    `json:"status_code"`
			Attempts   int    `json:"attempts"`
		} `json:"items"`
	}
	deadline := time.Now().Add(10 * time.Second)
	found := false
	for time.Now().Before(deadline) {
		status, body = authorized(t, http.MethodGet, ts.URL+"/api/notifications?limit=50", admin, nil)
		if status == http.StatusOK {
			_ = json.Unmarshal(body, &delivered)
			for _, item := range delivered.Items {
				if item.EventID == eventID && item.OK {
					found = true
					if item.Attempts != 3 || item.StatusCode != 200 {
						t.Fatalf("retry metadata wrong: %+v", item)
					}
				}
			}
		}
		if found {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !found || !succeededOnce.Load() || attempts.Load() < 3 {
		t.Fatalf("retry delivery failed: found=%v attempts=%d body=%s", found, attempts.Load(), body)
	}

	// Silence: two identical events within the window deliver once.
	_, _ = authorized(t, http.MethodPut, ts.URL+"/api/cameras/cam_006/rules", admin, map[string]any{
		"confidence_threshold": 0.6, "dedupe_seconds": 0,
		"enabled_classes": []string{"person", "vehicle", "intrusion", "smoke", "no_helmet"},
	})
	status, body = authorized(t, http.MethodPut, ts.URL+"/api/settings/notifications", admin, map[string]any{
		"enabled": true, "webhook_url": receiver.URL + "/hook", "format": "generic",
		"min_severity": "warn", "timeout_seconds": 5,
		"max_attempts": 1, "backoff_seconds": 0, "silence_seconds": 60,
	})
	if status != http.StatusOK {
		t.Fatalf("put silence notifications: %d %s", status, body)
	}
	before := attempts.Load()
	firstID, first := ingestAt(t, ts, "CAM-006", "intrusion", 0.95, time.Now())
	_, second := ingestAt(t, ts, "CAM-006", "intrusion", 0.95, time.Now())
	if first != 1 || second != 1 {
		t.Fatalf("expected two events for silence test, got %d/%d", first, second)
	}
	time.Sleep(1500 * time.Millisecond)
	if attempts.Load()-before != 1 {
		t.Fatalf("expected exactly one delivery inside the silence window, got %d", attempts.Load()-before)
	}
	_, metricsBody := authorized(t, http.MethodGet, ts.URL+"/metrics", admin, nil)
	line := findMetric(string(metricsBody), "dsh_twin_notifications_suppressed_total")
	if value, _ := strconv.ParseFloat(strings.TrimSpace(line), 64); value < 1 {
		t.Fatalf("suppressed metric not incremented: %q", line)
	}
	_ = firstID

	// Restore defaults.
	_, _ = authorized(t, http.MethodPut, ts.URL+"/api/settings/notifications", admin, map[string]any{
		"enabled": false, "webhook_url": "", "format": "generic", "min_severity": "warn",
		"timeout_seconds": 5, "max_attempts": 3, "backoff_seconds": 2, "silence_seconds": 30,
	})
	_, _ = authorized(t, http.MethodPut, ts.URL+"/api/cameras/cam_006/rules", admin, map[string]any{
		"confidence_threshold": 0.6, "dedupe_seconds": 5,
		"enabled_classes": []string{"person", "vehicle", "intrusion", "smoke", "no_helmet"},
	})
}

// findMetric returns the value text of a metric line by name.
func findMetric(body, name string) string {
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, name+" ") {
			return strings.TrimPrefix(line, name+" ")
		}
	}
	return "0"
}
