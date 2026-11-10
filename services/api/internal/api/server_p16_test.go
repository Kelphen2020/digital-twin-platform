package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestPatrolExecutionCancel(t *testing.T) {
	ts, _, _ := newP13Server(t)
	operator := login(t, ts, "operator", "Operator@123")
	viewer := login(t, ts, "viewer", "Viewer@123")
	admin := login(t, ts, "admin", "Admin@123456")
	routeID := createPatrolRouteForSchedule(t, ts, operator)
	scheduleID := createSchedule(t, ts, operator, routeID)

	// pending cancel
	status, body := authorized(t, http.MethodPost, ts.URL+"/api/patrol/schedules/"+scheduleID+"/run", operator, nil)
	if status != http.StatusCreated {
		t.Fatalf("run: %d %s", status, body)
	}
	var execution struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	_ = json.Unmarshal(body, &execution)
	status, _ = authorized(t, http.MethodPost, ts.URL+"/api/patrol/executions/"+execution.ID+"/cancel", viewer, nil)
	if status != http.StatusForbidden {
		t.Fatalf("viewer cancel should be 403, got %d", status)
	}
	status, body = authorized(t, http.MethodPost, ts.URL+"/api/patrol/executions/"+execution.ID+"/cancel", operator, map[string]any{"note": "smoke 取消"})
	if status != http.StatusOK || !strings.Contains(string(body), `"status":"cancelled"`) || !strings.Contains(string(body), "smoke 取消") || !strings.Contains(string(body), "finished_at") {
		t.Fatalf("cancel pending: %d %s", status, body)
	}
	status, _ = authorized(t, http.MethodPost, ts.URL+"/api/patrol/executions/"+execution.ID+"/cancel", operator, nil)
	if status != http.StatusConflict {
		t.Fatalf("cancel again should be 409, got %d", status)
	}

	// running cancel: assignee allowed, other operator forbidden.
	status, body = authorized(t, http.MethodPost, ts.URL+"/api/patrol/schedules/"+scheduleID+"/run", operator, nil)
	if status != http.StatusCreated {
		t.Fatalf("second run: %d %s", status, body)
	}
	_ = json.Unmarshal(body, &execution)
	status, _ = authorized(t, http.MethodPost, ts.URL+"/api/patrol/executions/"+execution.ID+"/claim", operator, nil)
	if status != http.StatusOK {
		t.Fatalf("claim: %d", status)
	}
	status, _ = authorized(t, http.MethodPost, ts.URL+"/api/patrol/executions/"+execution.ID+"/cancel", viewer, nil)
	if status != http.StatusForbidden {
		t.Fatalf("viewer cancel running should be 403, got %d", status)
	}
	status, body = authorized(t, http.MethodPost, ts.URL+"/api/patrol/executions/"+execution.ID+"/cancel", operator, nil)
	if status != http.StatusOK || !strings.Contains(string(body), `"status":"cancelled"`) {
		t.Fatalf("assignee cancel running: %d %s", status, body)
	}

	status, body = authorized(t, http.MethodGet, ts.URL+"/api/patrol/stats", admin, nil)
	if status != http.StatusOK || !strings.Contains(string(body), `"cancelled":2`) {
		t.Fatalf("stats cancelled: %d %s", status, body)
	}
	_, body = authorized(t, http.MethodGet, ts.URL+"/api/audit?limit=200", admin, nil)
	for _, needle := range []string{"patrol.execution.cancel"} {
		if !strings.Contains(string(body), needle) {
			t.Fatalf("audit missing %q", needle)
		}
	}
}

func TestPatrolExecutionExpiry(t *testing.T) {
	ts, server, memory := newP13Server(t)
	operator := login(t, ts, "operator", "Operator@123")
	admin := login(t, ts, "admin", "Admin@123456")
	routeID := createPatrolRouteForSchedule(t, ts, operator)
	scheduleID := createSchedule(t, ts, operator, routeID)
	server.SetPatrolTimeouts(30*time.Minute, 30*time.Minute)

	status, body := authorized(t, http.MethodPost, ts.URL+"/api/patrol/schedules/"+scheduleID+"/run", operator, nil)
	if status != http.StatusCreated {
		t.Fatalf("run: %d %s", status, body)
	}
	var execution struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(body, &execution)
	stored, found, err := memory.GetPatrolExecution(context.Background(), execution.ID)
	if err != nil || !found {
		t.Fatalf("load execution: %v %v", found, err)
	}
	stored.CreatedAt = time.Now().Add(-2 * time.Hour)
	if err := memory.UpdatePatrolExecution(context.Background(), stored); err != nil {
		t.Fatalf("age execution: %v", err)
	}
	server.RunPatrolOnce(context.Background())
	aged, found, _ := memory.GetPatrolExecution(context.Background(), execution.ID)
	if !found || aged.Status != "cancelled" {
		t.Fatalf("pending execution should expire: %+v", aged)
	}
	status, body = authorized(t, http.MethodGet, ts.URL+"/api/audit?limit=200", admin, nil)
	if status != http.StatusOK || !strings.Contains(string(body), "patrol.execution.expired") {
		t.Fatalf("expiry audit missing: %s", body)
	}

	// running execution expiry
	status, body = authorized(t, http.MethodPost, ts.URL+"/api/patrol/schedules/"+scheduleID+"/run", operator, nil)
	if status != http.StatusCreated {
		t.Fatalf("run 2: %d %s", status, body)
	}
	_ = json.Unmarshal(body, &execution)
	if status, _ := authorized(t, http.MethodPost, ts.URL+"/api/patrol/executions/"+execution.ID+"/claim", operator, nil); status != http.StatusOK {
		t.Fatalf("claim 2: %d", status)
	}
	running, _, _ := memory.GetPatrolExecution(context.Background(), execution.ID)
	old := time.Now().Add(-2 * time.Hour)
	running.StartedAt = &old
	_ = memory.UpdatePatrolExecution(context.Background(), running)
	server.RunPatrolOnce(context.Background())
	expired, found, _ := memory.GetPatrolExecution(context.Background(), execution.ID)
	if !found || expired.Status != "cancelled" || !strings.Contains(expired.Note, "超时未完成") {
		t.Fatalf("running execution should expire: %+v", expired)
	}
}
