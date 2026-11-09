package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestAlertLinkedPatrolCreation(t *testing.T) {
	ts, server, _ := newP13Server(t)
	operator := login(t, ts, "operator", "Operator@123")
	admin := login(t, ts, "admin", "Admin@123456")

	status, body := authorized(t, http.MethodPost, ts.URL+"/api/patrol/routes", operator, map[string]any{
		"name":             "联动巡检路线",
		"waypoints":        []map[string]any{{"x": -90, "y": -60, "z": 12}, {"x": 0, "y": 0, "z": 14}},
		"camera_ids":       []string{"cam_001"},
		"interval_seconds": 2,
	})
	if status != http.StatusCreated {
		t.Fatalf("create route: %d %s", status, body)
	}
	var route struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(body, &route)

	status, body = authorized(t, http.MethodPut, ts.URL+"/api/settings/escalation", admin, map[string]any{
		"enabled": true, "after_seconds": 1, "severities": []string{"warn", "critical"},
		"auto_dispatch": false, "second_after_seconds": 1, "auto_patrol": true,
	})
	if status != http.StatusOK {
		t.Fatalf("escalation settings: %d %s", status, body)
	}
	var settings map[string]any
	_ = json.Unmarshal(body, &settings)
	if settings["auto_patrol"] != true {
		t.Fatalf("auto_patrol not persisted: %s", body)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go server.RunEscalation(ctx, 50*time.Millisecond)

	eventID, accepted := ingestAt(t, ts, "CAM-001", "intrusion", 0.95, time.Now().Add(-10*time.Second))
	if accepted != 1 {
		t.Fatal("failed to create stale event")
	}

	var execution struct {
		ID         string `json:"id"`
		ScheduleID string `json:"schedule_id"`
		RouteID    string `json:"route_id"`
		EventID    string `json:"event_id"`
		Status     string `json:"status"`
		Note       string `json:"note"`
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		status, body = authorized(t, http.MethodGet, ts.URL+"/api/patrol/executions?event_id="+eventID, admin, nil)
		if status == http.StatusOK {
			var list struct {
				Items []json.RawMessage `json:"items"`
			}
			if json.Unmarshal(body, &list) == nil && len(list.Items) == 1 {
				_ = json.Unmarshal(list.Items[0], &execution)
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if execution.ID == "" {
		t.Fatalf("no linked patrol execution for event %s", eventID)
	}
	if execution.Status != "pending" || execution.ScheduleID != "" || execution.EventID != eventID {
		t.Fatalf("unexpected execution: %+v", execution)
	}
	if execution.RouteID != route.ID {
		t.Fatalf("route mismatch: got %s want %s", execution.RouteID, route.ID)
	}
	if !strings.Contains(execution.Note, "告警联动") || !strings.Contains(execution.Note, "CAM-001") {
		t.Fatalf("note missing linkage: %q", execution.Note)
	}

	// Re-escalation must not create a second pending task (dedupe).
	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		status, body = authorized(t, http.MethodGet, ts.URL+"/api/audit?limit=300", admin, nil)
		if status == http.StatusOK && strings.Contains(string(body), "patrol.execution.skipped") && strings.Contains(string(body), eventID) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	status, body = authorized(t, http.MethodGet, ts.URL+"/api/patrol/executions?event_id="+eventID, admin, nil)
	var list struct {
		Items []struct {
			Status string `json:"status"`
		} `json:"items"`
	}
	_ = json.Unmarshal(body, &list)
	if len(list.Items) != 1 || list.Items[0].Status != "pending" {
		t.Fatalf("dedupe failed, executions=%s", body)
	}

	// Restore settings.
	_, _ = authorized(t, http.MethodPut, ts.URL+"/api/settings/escalation", admin, map[string]any{
		"enabled": true, "after_seconds": 300, "severities": []string{"warn", "critical"},
		"auto_dispatch": true, "second_after_seconds": 0, "auto_patrol": false,
	})
}
