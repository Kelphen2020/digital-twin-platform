package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestDutyShiftsAndResolution(t *testing.T) {
	ts, _ := newP3Server(t)
	admin := login(t, ts, "admin", "Admin@123456")

	status, body := authorized(t, http.MethodPost, ts.URL+"/api/duty/groups", admin, map[string]any{
		"name": "班次测试组", "areas": []string{"area_gate"}, "rotation": "operator", "active": true,
		"shifts": []map[string]any{{
			"name": "早班", "start": "08:00", "end": "16:00",
			"weekdays": []int{1, 2, 3, 4, 5, 6, 7}, "operators": []string{"operator", "viewer"},
		}},
	})
	if status != http.StatusCreated {
		t.Fatalf("create shift group: %d %s", status, body)
	}
	var group struct {
		ID     string `json:"id"`
		Shifts []struct {
			ID string `json:"id"`
		} `json:"shifts"`
	}
	_ = json.Unmarshal(body, &group)
	if len(group.Shifts) != 1 || group.Shifts[0].ID == "" {
		t.Fatalf("shift id not generated: %s", body)
	}

	// 09:00 on a Sunday (weekday 7) is covered by the shift.
	status, body = authorized(t, http.MethodGet, ts.URL+"/api/duty/current?area_id=area_gate&at=2026-11-08T09:00:00%2B08:00", admin, nil)
	if status != http.StatusOK || !strings.Contains(string(body), `"name":"早班"`) || !strings.Contains(string(body), `"operators":["operator","viewer"]`) {
		t.Fatalf("current duty in shift: %d %s", status, body)
	}
	// 20:00 is outside the shift: no duty.
	status, body = authorized(t, http.MethodGet, ts.URL+"/api/duty/current?area_id=area_gate&at=2026-11-08T20:00:00%2B08:00", admin, nil)
	if status != http.StatusOK || !strings.Contains(string(body), `"shift":null`) || !strings.Contains(string(body), `"operators":[]`) {
		t.Fatalf("current duty outside shift: %d %s", status, body)
	}
	// Invalid shift input is rejected.
	status, _ = authorized(t, http.MethodPost, ts.URL+"/api/duty/groups", admin, map[string]any{
		"name": "坏班次", "areas": []string{"area_gate"},
		"shifts": []map[string]any{{"name": "x", "start": "25:00", "end": "26:00", "weekdays": []int{1}}},
	})
	if status != http.StatusBadRequest {
		t.Fatalf("invalid shift should be 400, got %d", status)
	}
	status, _ = authorized(t, http.MethodDelete, ts.URL+"/api/duty/groups/"+group.ID, admin, nil)
	if status != http.StatusNoContent {
		t.Fatalf("delete group: %d", status)
	}
}

func TestRotationDedupeAndReEscalation(t *testing.T) {
	ts, server := newP3Server(t)
	admin := login(t, ts, "admin", "Admin@123456")

	status, body := authorized(t, http.MethodPost, ts.URL+"/api/duty/groups", admin, map[string]any{
		"name": "轮转组", "areas": []string{"area_gate"}, "rotation": "operator", "active": true,
		"shifts": []map[string]any{{
			"name": "全天", "start": "00:00", "end": "00:00",
			"weekdays": []int{1, 2, 3, 4, 5, 6, 7}, "operators": []string{"operator", "viewer"},
		}},
	})
	if status != http.StatusCreated {
		t.Fatalf("create rotation group: %d %s", status, body)
	}
	var group struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(body, &group)
	t.Cleanup(func() {
		_, _ = authorized(t, http.MethodDelete, ts.URL+"/api/duty/groups/"+group.ID, admin, nil)
	})

	status, body = authorized(t, http.MethodPut, ts.URL+"/api/settings/escalation", admin, map[string]any{
		"enabled": true, "after_seconds": 1, "severities": []string{"warn", "critical"},
		"auto_dispatch": true, "second_after_seconds": 1,
	})
	if status != http.StatusOK {
		t.Fatalf("escalation settings: %d %s", status, body)
	}
	for _, invalid := range []int{-1, 604801} {
		status, _ = authorized(t, http.MethodPut, ts.URL+"/api/settings/escalation", admin, map[string]any{
			"enabled": true, "after_seconds": 1, "severities": []string{"warn"}, "second_after_seconds": invalid,
		})
		if status != http.StatusBadRequest {
			t.Fatalf("second_after_seconds=%d should be 400, got %d", invalid, status)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go server.RunEscalation(ctx, 50*time.Millisecond)

	firstEvent, accepted := ingestAt(t, ts, "CAM-001", "intrusion", 0.95, time.Now().Add(-10*time.Second))
	if accepted != 1 {
		t.Fatal("failed to create first stale event")
	}
	firstOrder := waitOrderForEvent(t, ts, admin, firstEvent)
	if firstOrder.Assignee != "operator" && firstOrder.Assignee != "viewer" {
		t.Fatalf("rotation assignee should be an operator, got %q", firstOrder.Assignee)
	}
	if !strings.Contains(firstOrder.Note, "自动派单") || !strings.Contains(firstOrder.Note, "轮转组") {
		t.Fatalf("note should mention group/shift: %q", firstOrder.Note)
	}

	secondEvent, accepted := ingestAt(t, ts, "CAM-006", "no_helmet", 0.95, time.Now().Add(-10*time.Second))
	if accepted != 1 {
		t.Fatal("failed to create second stale event")
	}
	secondOrder := waitOrderForEvent(t, ts, admin, secondEvent)
	if secondOrder.Assignee == firstOrder.Assignee {
		t.Fatalf("rotation did not advance: %q == %q", secondOrder.Assignee, firstOrder.Assignee)
	}

	// Second escalation of the first event, and open-order dedupe.
	reEscalated := false
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		status, body = authorized(t, http.MethodGet, ts.URL+"/api/events/"+firstEvent, admin, nil)
		if status == http.StatusOK && strings.Contains(string(body), "re_escalated_at") {
			reEscalated = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !reEscalated {
		t.Fatalf("event %s was not re-escalated", firstEvent)
	}

	// Only one order for the first event (dedupe).
	status, body = authorized(t, http.MethodGet, ts.URL+"/api/orders?event_id="+firstEvent, admin, nil)
	if status != http.StatusOK {
		t.Fatalf("orders by event: %d %s", status, body)
	}
	var orders struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	_ = json.Unmarshal(body, &orders)
	if len(orders.Items) != 1 {
		t.Fatalf("expected exactly one open order for %s, got %d: %s", firstEvent, len(orders.Items), body)
	}

	status, body = authorized(t, http.MethodGet, ts.URL+"/api/audit?limit=200", admin, nil)
	if status != http.StatusOK {
		t.Fatalf("audit: %d", status)
	}
	for _, needle := range []string{"event.re_escalate", "order.auto_dispatch_skipped", `"reason":"open_order"`} {
		if !strings.Contains(string(body), needle) {
			t.Fatalf("audit missing %q", needle)
		}
	}
	status, body = authorized(t, http.MethodGet, ts.URL+"/metrics", admin, nil)
	if status != http.StatusOK {
		t.Fatalf("metrics: %d", status)
	}
	if value := metricValue(string(body), "dsh_twin_orders_deduped_total"); value < 1 {
		t.Fatalf("orders_deduped metric not incremented: %s", body)
	}
	if value := metricValue(string(body), "dsh_twin_re_escalations_total"); value < 1 {
		t.Fatalf("re_escalations metric not incremented: %s", body)
	}

	// Restore escalation defaults.
	_, _ = authorized(t, http.MethodPut, ts.URL+"/api/settings/escalation", admin, map[string]any{
		"enabled": true, "after_seconds": 300, "severities": []string{"warn", "critical"},
		"auto_dispatch": true, "second_after_seconds": 0,
	})
}

type orderRow struct {
	ID       string `json:"id"`
	Assignee string `json:"assignee"`
	Note     string `json:"note"`
}

func waitOrderForEvent(t *testing.T, ts *httptest.Server, token, eventID string) orderRow {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		status, body := authorized(t, http.MethodGet, ts.URL+"/api/orders?event_id="+eventID, token, nil)
		if status == http.StatusOK {
			var orders struct {
				Items []orderRow `json:"items"`
			}
			if json.Unmarshal(body, &orders) == nil && len(orders.Items) > 0 {
				return orders.Items[0]
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("no order appeared for event %s", eventID)
	return orderRow{}
}

func metricValue(body, name string) float64 {
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, name+" ") {
			value, _ := strconv.ParseFloat(strings.TrimSpace(strings.TrimPrefix(line, name+" ")), 64)
			return value
		}
	}
	return 0
}
