package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func seedPatrolExecutions(t *testing.T, ts *httptest.Server, operator string) (string, string) {
	t.Helper()
	routeID := createPatrolRouteForSchedule(t, ts, operator)
	scheduleID := createSchedule(t, ts, operator, routeID)
	// done execution
	status, body := authorized(t, http.MethodPost, ts.URL+"/api/patrol/schedules/"+scheduleID+"/run", operator, nil)
	if status != http.StatusCreated {
		t.Fatalf("run 1: %d %s", status, body)
	}
	var execution struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(body, &execution)
	if status, _ := authorized(t, http.MethodPost, ts.URL+"/api/patrol/executions/"+execution.ID+"/claim", operator, nil); status != http.StatusOK {
		t.Fatalf("claim 1: %d", status)
	}
	if status, body := authorized(t, http.MethodPost, ts.URL+"/api/patrol/executions/"+execution.ID+"/complete", operator, map[string]any{"result": "done", "note": "ok"}); status != http.StatusOK {
		t.Fatalf("complete 1: %d %s", status, body)
	}
	// issue execution
	status, body = authorized(t, http.MethodPost, ts.URL+"/api/patrol/schedules/"+scheduleID+"/run", operator, nil)
	if status != http.StatusCreated {
		t.Fatalf("run 2: %d %s", status, body)
	}
	_ = json.Unmarshal(body, &execution)
	if status, _ := authorized(t, http.MethodPost, ts.URL+"/api/patrol/executions/"+execution.ID+"/claim", operator, nil); status != http.StatusOK {
		t.Fatalf("claim 2: %d", status)
	}
	if status, body := authorized(t, http.MethodPost, ts.URL+"/api/patrol/executions/"+execution.ID+"/complete", operator, map[string]any{"result": "issue", "note": "镜头脏污"}); status != http.StatusOK {
		t.Fatalf("complete 2: %d %s", status, body)
	}
	return routeID, scheduleID
}

func TestPatrolStatsAndExport(t *testing.T) {
	ts, _, _ := newP13Server(t)
	admin := login(t, ts, "admin", "Admin@123456")
	operator := login(t, ts, "operator", "Operator@123")
	viewer := login(t, ts, "viewer", "Viewer@123")
	routeID, scheduleID := seedPatrolExecutions(t, ts, operator)
	_ = routeID

	status, body := authorized(t, http.MethodGet, ts.URL+"/api/patrol/stats", viewer, nil)
	if status != http.StatusOK {
		t.Fatalf("stats: %d %s", status, body)
	}
	var stats struct {
		Total     int     `json:"executions_total"`
		Done      int     `json:"done"`
		Issue     int     `json:"issue"`
		Complete  float64 `json:"completion_rate"`
		IssueRate float64 `json:"issue_rate"`
		Average   float64 `json:"avg_duration_seconds"`
		ByRoute   []struct {
			RouteID string `json:"route_id"`
			Total   int    `json:"total"`
		} `json:"by_route"`
		ByAssignee []struct {
			Assignee string `json:"assignee"`
			Total    int    `json:"total"`
		} `json:"by_assignee"`
	}
	_ = json.Unmarshal(body, &stats)
	if stats.Total < 2 || stats.Done < 1 || stats.Issue < 1 {
		t.Fatalf("unexpected stats: %s", body)
	}
	if stats.Complete <= 0 || stats.Complete > 1 || stats.IssueRate <= 0 || stats.IssueRate > 1 {
		t.Fatalf("rates out of range: %s", body)
	}
	if stats.Average < 0 {
		t.Fatalf("negative average: %s", body)
	}
	routeFound, assigneeFound := false, false
	for _, route := range stats.ByRoute {
		if route.RouteID == routeID && route.Total >= 2 {
			routeFound = true
		}
	}
	for _, stat := range stats.ByAssignee {
		if stat.Assignee == "operator" && stat.Total >= 2 {
			assigneeFound = true
		}
	}
	if !routeFound || !assigneeFound {
		t.Fatalf("stats grouping missing: %s", body)
	}
	status, _ = authorized(t, http.MethodGet, ts.URL+"/api/patrol/stats?from=not-a-date", viewer, nil)
	if status != http.StatusBadRequest {
		t.Fatalf("invalid from should be 400, got %d", status)
	}

	// CSV export: operator allowed, viewer denied.
	status, body = authorized(t, http.MethodGet, ts.URL+"/api/patrol/executions/export?status=done", operator, nil)
	if status != http.StatusOK || !strings.HasPrefix(string(body), "id,schedule_id,route_name,status,assignee,result,note,scheduled_at,started_at,finished_at,order_id") {
		t.Fatalf("export csv: %d %s", status, body)
	}
	if !strings.Contains(string(body), "done") {
		t.Fatalf("export missing done rows: %s", body)
	}
	status, _ = authorized(t, http.MethodGet, ts.URL+"/api/patrol/executions/export", viewer, nil)
	if status != http.StatusForbidden {
		t.Fatalf("viewer export should be 403, got %d", status)
	}
	_ = scheduleID
	_ = admin
}

func TestPatrolExecutionPurge(t *testing.T) {
	ts, _, _ := newP13Server(t)
	admin := login(t, ts, "admin", "Admin@123456")
	operator := login(t, ts, "operator", "Operator@123")
	viewer := login(t, ts, "viewer", "Viewer@123")
	_, scheduleID := seedPatrolExecutions(t, ts, operator)

	status, _ := authorized(t, http.MethodDelete, ts.URL+"/api/patrol/executions", viewer, nil)
	if status != http.StatusForbidden {
		t.Fatalf("viewer purge should be 403, got %d", status)
	}
	status, _ = authorized(t, http.MethodDelete, ts.URL+"/api/patrol/executions", operator, nil)
	if status != http.StatusForbidden {
		t.Fatalf("operator purge should be 403, got %d", status)
	}
	status, _ = authorized(t, http.MethodDelete, ts.URL+"/api/patrol/executions?status=pending", admin, nil)
	if status != http.StatusBadRequest {
		t.Fatalf("pending purge should be 400, got %d", status)
	}
	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	status, body := authorized(t, http.MethodDelete, ts.URL+"/api/patrol/executions?before="+future+"&status=done", admin, nil)
	if status != http.StatusOK || !strings.Contains(string(body), `"deleted":1`) {
		t.Fatalf("purge done: %d %s", status, body)
	}
	// Issue execution must remain, stats updated.
	status, body = authorized(t, http.MethodGet, ts.URL+"/api/patrol/executions?status=issue&limit=100", admin, nil)
	if status != http.StatusOK || !strings.Contains(string(body), scheduleID) {
		t.Fatalf("issue execution should remain: %d %s", status, body)
	}
	status, body = authorized(t, http.MethodGet, ts.URL+"/api/patrol/stats", admin, nil)
	if status != http.StatusOK || strings.Contains(string(body), `"done":0,"issue":0`) {
		t.Fatalf("stats after purge: %d %s", status, body)
	}
	if !strings.Contains(string(body), `"deleted"`) && !strings.Contains(string(body), `"executions_total"`) {
		t.Fatalf("stats payload: %s", body)
	}
	status, body = authorized(t, http.MethodGet, ts.URL+"/api/audit?limit=100", admin, nil)
	if !strings.Contains(string(body), "patrol.executions.purge") {
		t.Fatalf("purge audit missing: %s", body)
	}
}
