package api_test

import (
	"context"
	"encoding/json"
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

func newP13Server(t *testing.T) (*httptest.Server, *api.Server, *store.Memory) {
	t.Helper()
	memory := store.NewMemory(2000)
	memory.Seed(store.SeedCameras(), store.SeedAreas())
	authService := auth.NewWithRepo("on", "test-secret", memory)
	if err := authService.EnsureSeed(context.Background()); err != nil {
		t.Fatalf("seed users: %v", err)
	}
	server := api.New(memory, rules.New(5*time.Second), realtime.NewHub(), metrics.New(), "test", authService, "")
	ts := httptest.NewServer(server.Handler())
	t.Cleanup(ts.Close)
	return ts, server, memory
}

func createPatrolRouteForSchedule(t *testing.T, ts *httptest.Server, token string) string {
	t.Helper()
	status, body := authorized(t, http.MethodPost, ts.URL+"/api/patrol/routes", token, map[string]any{
		"name":             "P13巡检路线",
		"waypoints":        []map[string]any{{"x": -90, "y": -60, "z": 12, "heading": 90}, {"x": 0, "y": 0, "z": 14}},
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
	return route.ID
}

func createSchedule(t *testing.T, ts *httptest.Server, token, routeID string) string {
	t.Helper()
	status, body := authorized(t, http.MethodPost, ts.URL+"/api/patrol/schedules", token, map[string]any{
		"name": "P13计划", "route_id": routeID, "interval_minutes": 60, "enabled": true,
	})
	if status != http.StatusCreated {
		t.Fatalf("create schedule: %d %s", status, body)
	}
	var schedule struct {
		ID        string `json:"id"`
		NextRunAt string `json:"next_run_at"`
	}
	_ = json.Unmarshal(body, &schedule)
	if schedule.ID == "" || schedule.NextRunAt == "" {
		t.Fatalf("schedule payload incomplete: %s", body)
	}
	return schedule.ID
}

func TestPatrolScheduleCRUDAndValidation(t *testing.T) {
	ts, _, _ := newP13Server(t)
	admin := login(t, ts, "admin", "Admin@123456")
	operator := login(t, ts, "operator", "Operator@123")
	viewer := login(t, ts, "viewer", "Viewer@123")
	routeID := createPatrolRouteForSchedule(t, ts, operator)
	scheduleID := createSchedule(t, ts, operator, routeID)

	status, body := authorized(t, http.MethodGet, ts.URL+"/api/patrol/schedules", viewer, nil)
	if status != http.StatusOK || !strings.Contains(string(body), scheduleID) {
		t.Fatalf("viewer list schedules: %d %s", status, body)
	}
	status, _ = authorized(t, http.MethodPost, ts.URL+"/api/patrol/schedules", viewer, map[string]any{
		"name": "x", "route_id": routeID, "interval_minutes": 60,
	})
	if status != http.StatusForbidden {
		t.Fatalf("viewer create schedule should be 403, got %d", status)
	}
	status, body = authorized(t, http.MethodPatch, ts.URL+"/api/patrol/schedules/"+scheduleID, operator, map[string]any{
		"name": "P13计划（改）", "interval_minutes": 120,
	})
	if status != http.StatusOK || !strings.Contains(string(body), "P13计划（改）") || !strings.Contains(string(body), `"interval_minutes":120`) {
		t.Fatalf("patch schedule: %d %s", status, body)
	}
	// Validation.
	invalid := []map[string]any{
		{"name": "x", "interval_minutes": 60},
		{"name": "x", "route_id": "patrol_missing", "interval_minutes": 60},
		{"name": "x", "route_id": routeID, "interval_minutes": 0},
		{"name": "x", "route_id": routeID, "interval_minutes": 10081},
	}
	for _, payload := range invalid {
		status, body = authorized(t, http.MethodPost, ts.URL+"/api/patrol/schedules", operator, payload)
		if status != http.StatusBadRequest {
			t.Fatalf("invalid schedule %v should be 400, got %d %s", payload, status, body)
		}
	}
	// Admin can delete someone else's schedule.
	status, _ = authorized(t, http.MethodDelete, ts.URL+"/api/patrol/schedules/"+scheduleID, admin, nil)
	if status != http.StatusNoContent {
		t.Fatalf("admin delete operator schedule: %d", status)
	}
}

func TestPatrolExecutionLifecycleAndOrderLink(t *testing.T) {
	ts, _, _ := newP13Server(t)
	operator := login(t, ts, "operator", "Operator@123")
	viewer := login(t, ts, "viewer", "Viewer@123")
	routeID := createPatrolRouteForSchedule(t, ts, operator)
	scheduleID := createSchedule(t, ts, operator, routeID)

	status, body := authorized(t, http.MethodPost, ts.URL+"/api/patrol/schedules/"+scheduleID+"/run", operator, nil)
	if status != http.StatusCreated {
		t.Fatalf("run now: %d %s", status, body)
	}
	var execution struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	_ = json.Unmarshal(body, &execution)
	if execution.ID == "" || execution.Status != "pending" {
		t.Fatalf("execution payload: %s", body)
	}
	status, body = authorized(t, http.MethodGet, ts.URL+"/api/patrol/executions", operator, nil)
	if status != http.StatusOK || !strings.Contains(string(body), execution.ID) {
		t.Fatalf("list executions: %d %s", status, body)
	}
	status, _ = authorized(t, http.MethodPost, ts.URL+"/api/patrol/executions/"+execution.ID+"/claim", viewer, nil)
	if status != http.StatusForbidden {
		t.Fatalf("viewer claim should be 403, got %d", status)
	}
	status, body = authorized(t, http.MethodPost, ts.URL+"/api/patrol/executions/"+execution.ID+"/claim", operator, nil)
	if status != http.StatusOK || !strings.Contains(string(body), `"status":"running"`) || !strings.Contains(string(body), `"assignee":"operator"`) {
		t.Fatalf("claim: %d %s", status, body)
	}
	status, body = authorized(t, http.MethodPost, ts.URL+"/api/patrol/executions/"+execution.ID+"/complete", operator, map[string]any{
		"result": "done", "note": "一切正常",
	})
	if status != http.StatusOK || !strings.Contains(string(body), `"status":"done"`) || !strings.Contains(string(body), "finished_at") {
		t.Fatalf("complete done: %d %s", status, body)
	}

	// Issue flow creates an order.
	status, body = authorized(t, http.MethodPost, ts.URL+"/api/patrol/schedules/"+scheduleID+"/run", operator, nil)
	if status != http.StatusCreated {
		t.Fatalf("second run: %d %s", status, body)
	}
	_ = json.Unmarshal(body, &execution)
	status, _ = authorized(t, http.MethodPost, ts.URL+"/api/patrol/executions/"+execution.ID+"/claim", operator, nil)
	if status != http.StatusOK {
		t.Fatalf("second claim: %d", status)
	}
	status, body = authorized(t, http.MethodPost, ts.URL+"/api/patrol/executions/"+execution.ID+"/complete", operator, map[string]any{
		"result": "issue", "note": "摄像头画面模糊",
	})
	if status != http.StatusOK || !strings.Contains(string(body), `"status":"issue"`) || !strings.Contains(string(body), `"order_id":"ord_`) {
		t.Fatalf("complete issue: %d %s", status, body)
	}
	var issueExecution struct {
		OrderID string `json:"order_id"`
	}
	_ = json.Unmarshal(body, &issueExecution)
	status, body = authorized(t, http.MethodGet, ts.URL+"/api/orders?limit=100", operator, nil)
	if status != http.StatusOK || !strings.Contains(string(body), issueExecution.OrderID) || !strings.Contains(string(body), "巡检异常") {
		t.Fatalf("order for patrol issue: %d %s", status, body)
	}
	admin := login(t, ts, "admin", "Admin@123456")
	status, body = authorized(t, http.MethodGet, ts.URL+"/api/audit?limit=200", admin, nil)
	for _, needle := range []string{"patrol.schedule.create", "patrol.schedule.run", "patrol.execution.claim", "patrol.execution.complete", "order.created"} {
		if !strings.Contains(string(body), needle) {
			t.Fatalf("audit missing %q", needle)
		}
	}
}

func TestPatrolSchedulerCreatesDueExecutions(t *testing.T) {
	ts, server, memory := newP13Server(t)
	operator := login(t, ts, "operator", "Operator@123")
	routeID := createPatrolRouteForSchedule(t, ts, operator)
	scheduleID := createSchedule(t, ts, operator, routeID)

	// Force the schedule due, then run one scheduler pass.
	schedule, found, err := memory.GetPatrolSchedule(context.Background(), scheduleID)
	if err != nil || !found {
		t.Fatalf("load schedule: %v %v", found, err)
	}
	schedule.NextRunAt = time.Now().Add(-time.Minute)
	if err := memory.UpdatePatrolSchedule(context.Background(), schedule); err != nil {
		t.Fatalf("make due: %v", err)
	}
	server.RunPatrolOnce(context.Background())

	status, body := authorized(t, http.MethodGet, ts.URL+"/api/patrol/executions?schedule_id="+scheduleID, operator, nil)
	if status != http.StatusOK || !strings.Contains(string(body), scheduleID) || !strings.Contains(string(body), `"status":"pending"`) {
		t.Fatalf("scheduler execution: %d %s", status, body)
	}
	advised, found, _ := memory.GetPatrolSchedule(context.Background(), scheduleID)
	if !found || !advised.NextRunAt.After(time.Now()) {
		t.Fatalf("next_run_at was not advanced: %+v", advised)
	}
}
