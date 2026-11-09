package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestPatrolRoutesCRUDAndValidation(t *testing.T) {
	ts := newP7Server(t)
	admin := login(t, ts, "admin", "Admin@123456")
	operator := login(t, ts, "operator", "Operator@123")
	viewer := login(t, ts, "viewer", "Viewer@123")

	status, body := authorized(t, http.MethodGet, ts.URL+"/api/patrol/routes", viewer, nil)
	if status != http.StatusOK || !strings.Contains(string(body), `"items":[]`) {
		t.Fatalf("initial list: %d %s", status, body)
	}

	createBody := map[string]any{
		"name":             "巡检测试",
		"waypoints":        []map[string]any{{"x": -90, "y": -60, "z": 12, "heading": 90}, {"x": 0, "y": 0, "z": 14}},
		"camera_ids":       []string{"cam_001", "cam_006"},
		"interval_seconds": 2,
	}
	status, body = authorized(t, http.MethodPost, ts.URL+"/api/patrol/routes", operator, createBody)
	if status != http.StatusCreated {
		t.Fatalf("create route: %d %s", status, body)
	}
	var route struct {
		ID        string `json:"id"`
		CreatedBy string `json:"created_by"`
	}
	_ = json.Unmarshal(body, &route)
	if route.ID == "" || route.CreatedBy != "operator" {
		t.Fatalf("unexpected route: %s", body)
	}

	status, body = authorized(t, http.MethodGet, ts.URL+"/api/patrol/routes", viewer, nil)
	if status != http.StatusOK || !strings.Contains(string(body), route.ID) {
		t.Fatalf("list after create: %d %s", status, body)
	}
	status, _ = authorized(t, http.MethodPost, ts.URL+"/api/patrol/routes", viewer, createBody)
	if status != http.StatusForbidden {
		t.Fatalf("viewer create should be 403, got %d", status)
	}

	status, body = authorized(t, http.MethodPatch, ts.URL+"/api/patrol/routes/"+route.ID, operator, map[string]any{
		"name": "巡检测试（改）", "interval_seconds": 5,
	})
	if status != http.StatusOK || !strings.Contains(string(body), "巡检测试（改）") || !strings.Contains(string(body), `"interval_seconds":5`) {
		t.Fatalf("patch route: %d %s", status, body)
	}

	// Validation matrix.
	cases := []struct {
		name string
		body map[string]any
	}{
		{"one waypoint", map[string]any{"name": "x", "waypoints": []map[string]any{{"x": 0, "y": 0, "z": 0}}, "interval_seconds": 2}},
		{"interval zero", map[string]any{"name": "x", "waypoints": []map[string]any{{"x": 0, "y": 0, "z": 0}, {"x": 1, "y": 1, "z": 0}}, "interval_seconds": 0}},
		{"unknown camera", map[string]any{"name": "x", "waypoints": []map[string]any{{"x": 0, "y": 0, "z": 0}, {"x": 1, "y": 1, "z": 0}}, "camera_ids": []string{"cam_999"}, "interval_seconds": 2}},
	}
	for _, testCase := range cases {
		status, body = authorized(t, http.MethodPost, ts.URL+"/api/patrol/routes", operator, testCase.body)
		if status != http.StatusBadRequest {
			t.Fatalf("%s should be 400, got %d %s", testCase.name, status, body)
		}
		if testCase.name == "unknown camera" && !strings.Contains(string(body), "unknown_camera") {
			t.Fatalf("unknown camera error code missing: %s", body)
		}
	}
	many := make([]map[string]any, 0, 51)
	for i := 0; i < 51; i++ {
		many = append(many, map[string]any{"x": i, "y": 0, "z": 0})
	}
	status, _ = authorized(t, http.MethodPost, ts.URL+"/api/patrol/routes", operator, map[string]any{"name": "太多", "waypoints": many, "interval_seconds": 2})
	if status != http.StatusBadRequest {
		t.Fatalf("51 waypoints should be 400, got %d", status)
	}

	// Admin can delete another user's route; operator cannot delete admin's.
	status, _ = authorized(t, http.MethodDelete, ts.URL+"/api/patrol/routes/"+route.ID, admin, nil)
	if status != http.StatusNoContent {
		t.Fatalf("admin delete operator route: %d", status)
	}
	status, body = authorized(t, http.MethodPost, ts.URL+"/api/patrol/routes", admin, map[string]any{
		"name": "管理员路线", "waypoints": []map[string]any{{"x": 0, "y": 0, "z": 0}, {"x": 10, "y": 10, "z": 0}}, "interval_seconds": 3,
	})
	if status != http.StatusCreated {
		t.Fatalf("admin create: %d %s", status, body)
	}
	var adminRoute struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(body, &adminRoute)
	status, _ = authorized(t, http.MethodDelete, ts.URL+"/api/patrol/routes/"+adminRoute.ID, operator, nil)
	if status != http.StatusForbidden {
		t.Fatalf("operator delete admin route should be 403, got %d", status)
	}
	status, _ = authorized(t, http.MethodDelete, ts.URL+"/api/patrol/routes/"+adminRoute.ID, admin, nil)
	if status != http.StatusNoContent {
		t.Fatalf("admin cleanup delete: %d", status)
	}

	status, body = authorized(t, http.MethodGet, ts.URL+"/api/audit?limit=100", admin, nil)
	if status != http.StatusOK {
		t.Fatalf("audit: %d", status)
	}
	for _, needle := range []string{"patrol.create", "patrol.update", "patrol.delete"} {
		if !strings.Contains(string(body), needle) {
			t.Fatalf("audit missing %q", needle)
		}
	}
	_ = fmt.Sprint()
}
