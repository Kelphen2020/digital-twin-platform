package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// These tests exercise read paths and error branches that the behaviour-focused
// suites skip, lifting statement coverage for handlers without duplicating the
// P0–P16 acceptance scenarios.
func TestReadEndpointsAndErrorBranches(t *testing.T) {
	ts := newTestServer(t) // auth off: every request is an implicit admin

	// Areas and site.
	if status, body := doJSON(t, http.MethodGet, ts.URL+"/api/areas", nil); status != http.StatusOK || !strings.Contains(string(body), `"items"`) {
		t.Fatalf("areas: %d %s", status, body)
	}
	if status, body := doJSON(t, http.MethodGet, ts.URL+"/api/site", nil); status != http.StatusOK || !strings.Contains(string(body), `"name"`) {
		t.Fatalf("site: %d %s", status, body)
	}

	// Camera detail: rules + calibration lifecycle.
	if status, body := doJSON(t, http.MethodGet, ts.URL+"/api/cameras/cam_001", nil); status != http.StatusOK {
		t.Fatalf("camera: %d %s", status, body)
	}
	if status, body := doJSON(t, http.MethodGet, ts.URL+"/api/cameras/cam_001/rules", nil); status != http.StatusOK || !strings.Contains(string(body), "confidence_threshold") {
		t.Fatalf("camera rules: %d %s", status, body)
	}
	if status, body := doJSON(t, http.MethodGet, ts.URL+"/api/cameras/cam_999", nil); status != http.StatusNotFound {
		t.Fatalf("unknown camera should be 404, got %d %s", status, body)
	}

	// Orders: create → get → patch → list, plus not-found branches.
	eventID := ingestEvent(t, ts, "CAM-003", "intrusion")
	status, body := doJSON(t, http.MethodPost, ts.URL+"/api/orders", map[string]any{
		"event_id": eventID, "title": "覆盖测试工单", "priority": "normal",
	})
	if status != http.StatusCreated {
		t.Fatalf("create order: %d %s", status, body)
	}
	orderID := extractID(t, body, `"id":"`)
	if status, body := doJSON(t, http.MethodGet, ts.URL+"/api/orders/"+orderID, nil); status != http.StatusOK {
		t.Fatalf("get order: %d %s", status, body)
	}
	if status, body := doJSON(t, http.MethodPatch, ts.URL+"/api/orders/"+orderID, map[string]any{"status": "processing"}); status != http.StatusOK {
		t.Fatalf("patch order: %d %s", status, body)
	}
	if status, body := doJSON(t, http.MethodGet, ts.URL+"/api/orders/ord_missing", nil); status != http.StatusNotFound {
		t.Fatalf("missing order should be 404, got %d %s", status, body)
	}
	if status, _ := doJSON(t, http.MethodGet, ts.URL+"/api/orders", nil); status != http.StatusOK {
		t.Fatalf("list orders: %d", status)
	}

	// Duty groups list (seeded demo group may be absent in a fresh store).
	if status, body := doJSON(t, http.MethodGet, ts.URL+"/api/duty/groups", nil); status != http.StatusOK || !strings.Contains(string(body), "items") {
		t.Fatalf("duty groups: %d %s", status, body)
	}
	if status, _ := doJSON(t, http.MethodGet, ts.URL+"/api/duty/current?area_id=area_gate", nil); status != http.StatusOK {
		t.Fatalf("duty current: %d", status)
	}
	if status, _ := doJSON(t, http.MethodGet, ts.URL+"/api/duty/current", nil); status != http.StatusBadRequest {
		t.Fatalf("duty current without area should be 400, got %d", status)
	}

	// Notification settings read path.
	if status, body := doJSON(t, http.MethodGet, ts.URL+"/api/settings/notifications", nil); status != http.StatusOK || !strings.Contains(string(body), "silence_seconds") {
		t.Fatalf("notification settings: %d %s", status, body)
	}

	// Bus status without an attached bus.
	if status, _ := doJSON(t, http.MethodGet, ts.URL+"/api/bus/status", nil); status != http.StatusOK && status != http.StatusServiceUnavailable {
		t.Fatalf("bus status: %d", status)
	}

	// Unknown API route.
	if status, _ := doJSON(t, http.MethodGet, ts.URL+"/api/does-not-exist", nil); status != http.StatusNotFound {
		t.Fatalf("unknown route: %d", status)
	}
}

func TestEventDetailAndInvalidInputs(t *testing.T) {
	ts := newTestServer(t)

	status, response := doJSON(t, http.MethodPost, ts.URL+"/api/ingest/detections", map[string]any{
		"camera_code": "CAM-004",
		"detections": []map[string]any{{
			"class": "smoke", "confidence": 0.97,
			"world": map[string]float64{"x": 1, "y": 2, "z": 0},
		}},
	})
	if status != http.StatusAccepted {
		t.Fatalf("ingest: %d %s", status, response)
	}
	var ingest struct {
		Events []struct {
			ID string `json:"id"`
		} `json:"events"`
	}
	_ = json.Unmarshal(response, &ingest)
	if len(ingest.Events) == 0 {
		t.Fatalf("no event produced: %s", response)
	}
	eventID := ingest.Events[0].ID

	if status, body := doJSON(t, http.MethodGet, ts.URL+"/api/events/"+eventID, nil); status != http.StatusOK || !strings.Contains(string(body), eventID) {
		t.Fatalf("event detail: %d %s", status, body)
	}
	if status, _ := doJSON(t, http.MethodGet, ts.URL+"/api/events/evt_missing", nil); status != http.StatusNotFound {
		t.Fatalf("missing event: %d", status)
	}

	// Malformed JSON bodies map to 400 across handlers.
	for _, path := range []string{"/api/orders", "/api/duty/groups", "/api/patrol/routes", "/api/patrol/schedules"} {
		request, _ := http.NewRequest(http.MethodPost, ts.URL+path, strings.NewReader("{"))
		request.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s with malformed JSON should be 400, got %d", path, response.StatusCode)
		}
	}

	// Order patch validation and camera patch validation.
	if status, _ := doJSON(t, http.MethodPatch, ts.URL+"/api/orders/"+orderIDFor(t, ts), map[string]any{"status": "bogus"}); status != http.StatusBadRequest {
		t.Fatalf("invalid order status should be 400, got %d", status)
	}
	if status, _ := doJSON(t, http.MethodPatch, ts.URL+"/api/cameras/cam_001", map[string]any{"status": "bogus"}); status != http.StatusBadRequest {
		t.Fatalf("invalid camera status should be 400, got %d", status)
	}
	if status, _ := doJSON(t, http.MethodPatch, ts.URL+"/api/cameras/cam_001", map[string]any{"status": "offline"}); status != http.StatusOK {
		t.Fatalf("valid camera patch: %d", status)
	}
}

func extractID(t *testing.T, body []byte, prefix string) string {
	t.Helper()
	text := string(body)
	index := strings.Index(text, prefix)
	if index < 0 {
		t.Fatalf("id not found in %s", text)
	}
	rest := text[index+len(prefix):]
	end := strings.IndexAny(rest, `",`)
	if end < 0 {
		t.Fatalf("malformed id in %s", text)
	}
	return rest[:end]
}

func orderIDFor(t *testing.T, ts *httptest.Server) string {
	t.Helper()
	eventID := ingestEvent(t, ts, "CAM-006", "smoke")
	status, body := doJSON(t, http.MethodPost, ts.URL+"/api/orders", map[string]any{
		"event_id": eventID, "title": "临时工单", "priority": "normal",
	})
	if status != http.StatusCreated {
		t.Fatalf("create temp order: %d %s", status, body)
	}
	return extractID(t, body, `"id":"`)
}

// ingestEvent posts one high-confidence detection and returns the event id.
func ingestEvent(t *testing.T, ts *httptest.Server, cameraCode, class string) string {
	t.Helper()
	status, body := doJSON(t, http.MethodPost, ts.URL+"/api/ingest/detections", map[string]any{
		"camera_code": cameraCode,
		"detections": []map[string]any{{
			"class": class, "confidence": 0.97,
			"world": map[string]float64{"x": 1, "y": 2, "z": 0},
		}},
	})
	if status != http.StatusAccepted {
		t.Fatalf("ingest %s/%s: %d %s", cameraCode, class, status, body)
	}
	var payload struct {
		Events []struct {
			ID string `json:"id"`
		} `json:"events"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || len(payload.Events) == 0 {
		t.Fatalf("ingest produced no event: %s", body)
	}
	return payload.Events[0].ID
}
