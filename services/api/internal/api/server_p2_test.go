package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"digital-twin-platform/api/internal/api"
	"digital-twin-platform/api/internal/auth"
	"digital-twin-platform/api/internal/metrics"
	"digital-twin-platform/api/internal/realtime"
	"digital-twin-platform/api/internal/rules"
	"digital-twin-platform/api/internal/store"
)

func newP2Server(t *testing.T, mode, ingestToken string) *httptest.Server {
	t.Helper()
	memory := store.NewMemory(1000)
	memory.Seed(store.SeedCameras(), store.SeedAreas())
	server := api.New(memory, rules.New(5*time.Second), realtime.NewHub(), metrics.New(),
		"test", auth.New(mode, "test-secret"), ingestToken)
	ts := httptest.NewServer(server.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func login(t *testing.T, ts *httptest.Server, username, password string) string {
	t.Helper()
	status, body := doJSON(t, http.MethodPost, ts.URL+"/api/auth/login", map[string]string{
		"username": username, "password": password,
	})
	if status != http.StatusOK {
		t.Fatalf("login %s: %d %s", username, status, body)
	}
	var response struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &response); err != nil || response.Token == "" {
		t.Fatalf("decode login: %v %s", err, body)
	}
	return response.Token
}

func authorized(t *testing.T, method, url, token string, body any) (int, []byte) {
	t.Helper()
	return doJSONWithHeaders(t, method, url, body, map[string]string{"Authorization": "Bearer " + token})
}

func ingestFor(t *testing.T, ts *httptest.Server, camera, class string) (string, int) {
	t.Helper()
	status, body := doJSON(t, http.MethodPost, ts.URL+"/api/ingest/detections", map[string]any{
		"camera_code": camera,
		"detections": []map[string]any{{
			"class": class, "confidence": 0.95, "bbox": []float64{0.4, 0.3, 0.6, 0.8},
			"world": map[string]float64{"x": 1, "y": 2, "z": 0},
		}},
	})
	if status != http.StatusAccepted {
		t.Fatalf("ingest %s: %d %s", camera, status, body)
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

func TestLoginAndRBAC(t *testing.T) {
	ts := newP2Server(t, "on", "")

	// Unauthenticated read is refused.
	status, _ := doJSON(t, http.MethodGet, ts.URL+"/api/cameras", nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", status)
	}
	// Bad credentials are refused.
	status, _ = doJSON(t, http.MethodPost, ts.URL+"/api/auth/login", map[string]string{
		"username": "admin", "password": "wrong",
	})
	if status != http.StatusUnauthorized {
		t.Fatalf("expected 401 for bad password, got %d", status)
	}

	admin := login(t, ts, "admin", "Admin@123456")
	operator := login(t, ts, "operator", "Operator@123")
	viewer := login(t, ts, "viewer", "Viewer@123")

	status, body := authorized(t, http.MethodGet, ts.URL+"/api/auth/me", admin, nil)
	if status != http.StatusOK || !strings.Contains(string(body), `"role":"admin"`) {
		t.Fatalf("me: %d %s", status, body)
	}

	// Create events on an in-scope and an out-of-scope camera.
	eventInScope, _ := ingestFor(t, ts, "CAM-001", "intrusion") // area_gate
	eventOutOfScope, _ := ingestFor(t, ts, "CAM-002", "intrusion")
	if eventInScope == "" || eventOutOfScope == "" {
		t.Fatal("failed to create test events")
	}
	// Give the operator a fresh event to ack (dedupe window already consumed).
	eventForOperator, _ := ingestFor(t, ts, "CAM-005", "smoke") // area_parking
	if eventForOperator == "" {
		t.Fatal("failed to create operator event")
	}

	// viewer: read allowed, writes forbidden.
	if status, _ := authorized(t, http.MethodGet, ts.URL+"/api/cameras", viewer, nil); status != http.StatusOK {
		t.Fatalf("viewer read: %d", status)
	}
	status, body = authorized(t, http.MethodPost, ts.URL+"/api/events/"+eventForOperator+"/ack", viewer, nil)
	if status != http.StatusForbidden {
		t.Fatalf("viewer ack should be 403, got %d %s", status, body)
	}

	// operator: in-scope action allowed, out-of-scope forbidden.
	status, body = authorized(t, http.MethodPost, ts.URL+"/api/events/"+eventForOperator+"/ack", operator, nil)
	if status != http.StatusOK {
		t.Fatalf("operator ack in scope: %d %s", status, body)
	}
	status, body = authorized(t, http.MethodPost, ts.URL+"/api/events/"+eventOutOfScope+"/ack", operator, nil)
	if status != http.StatusForbidden || !strings.Contains(string(body), "out_of_scope") {
		t.Fatalf("operator ack out of scope: %d %s", status, body)
	}
	status, body = authorized(t, http.MethodPatch, ts.URL+"/api/cameras/cam_002", operator, map[string]any{"status": "offline"})
	if status != http.StatusForbidden || !strings.Contains(string(body), "out_of_scope") {
		t.Fatalf("operator patch out of scope: %d %s", status, body)
	}
	status, _ = authorized(t, http.MethodPatch, ts.URL+"/api/cameras/cam_001", operator, map[string]any{"status": "degraded"})
	if status != http.StatusOK {
		t.Fatalf("operator patch in scope: %d", status)
	}

	// rules: operator forbidden, admin allowed + rules affect ingest.
	status, _ = authorized(t, http.MethodPut, ts.URL+"/api/cameras/cam_003/rules", operator, map[string]any{
		"confidence_threshold": 0.6, "dedupe_seconds": 5, "enabled_classes": []string{"person"},
	})
	if status != http.StatusForbidden {
		t.Fatalf("operator rules should be 403, got %d", status)
	}
	status, body = authorized(t, http.MethodPut, ts.URL+"/api/cameras/cam_003/rules", admin, map[string]any{
		"confidence_threshold": 0.99, "dedupe_seconds": 5, "enabled_classes": []string{"person"},
	})
	if status != http.StatusOK {
		t.Fatalf("admin rules: %d %s", status, body)
	}
	if _, accepted := ingestFor(t, ts, "CAM-003", "person"); accepted != 0 {
		t.Fatalf("high threshold should drop detection, accepted=%d", accepted)
	}
	if _, accepted := ingestFor(t, ts, "CAM-003", "vehicle"); accepted != 0 {
		t.Fatalf("disabled class should drop detection, accepted=%d", accepted)
	}
	status, body = authorized(t, http.MethodPut, ts.URL+"/api/cameras/cam_003/rules", admin, map[string]any{
		"confidence_threshold": 0.6, "dedupe_seconds": 5,
		"enabled_classes": []string{"person", "vehicle", "intrusion", "smoke", "no_helmet"},
	})
	if status != http.StatusOK {
		t.Fatalf("restore rules: %d %s", status, body)
	}
	if _, accepted := ingestFor(t, ts, "CAM-003", "person"); accepted != 1 {
		t.Fatalf("restored rules should accept, accepted=%d", accepted)
	}

	// orders: operator creates and drives one through the state machine.
	status, body = authorized(t, http.MethodPost, ts.URL+"/api/orders", operator, map[string]any{
		"event_id": eventInScope, "priority": "high", "assignee": "安保A组", "note": "5分钟内到场",
	})
	if status != http.StatusCreated {
		t.Fatalf("create order: %d %s", status, body)
	}
	var order struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(body, &order); err != nil || order.ID == "" {
		t.Fatalf("decode order: %v %s", err, body)
	}
	status, body = authorized(t, http.MethodPatch, ts.URL+"/api/orders/"+order.ID, operator, map[string]any{
		"status": "processing", "assignee": "安保B组", "note": "已到场",
	})
	if status != http.StatusOK || !strings.Contains(string(body), `"status":"processing"`) {
		t.Fatalf("patch order: %d %s", status, body)
	}
	status, body = authorized(t, http.MethodPatch, ts.URL+"/api/orders/"+order.ID, operator, map[string]any{"status": "done"})
	if status != http.StatusOK || !strings.Contains(string(body), `"closed_at"`) {
		t.Fatalf("close order: %d %s", status, body)
	}
	status, body = authorized(t, http.MethodGet, ts.URL+"/api/orders?status=done", operator, nil)
	if status != http.StatusOK || !strings.Contains(string(body), order.ID) {
		t.Fatalf("list orders: %d %s", status, body)
	}

	// audit records the actor username.
	status, body = authorized(t, http.MethodGet, ts.URL+"/api/audit?limit=100", admin, nil)
	if status != http.StatusOK {
		t.Fatalf("audit: %d %s", status, body)
	}
	for _, needle := range []string{"order.create", "order.update", "camera.rules.put", `"actor":"operator"`, `"actor":"admin"`} {
		if !strings.Contains(string(body), needle) {
			t.Fatalf("audit missing %q: %s", needle, body)
		}
	}
}

func TestIngestTokenGuard(t *testing.T) {
	ts := newP2Server(t, "on", "machine-token")
	body := map[string]any{
		"camera_code": "CAM-004",
		"detections":  []map[string]any{{"class": "person", "confidence": 0.9}},
	}
	status, _ := doJSON(t, http.MethodPost, ts.URL+"/api/ingest/detections", body)
	if status != http.StatusUnauthorized {
		t.Fatalf("expected 401 without ingest token, got %d", status)
	}
	status, response := doJSONWithHeaders(t, http.MethodPost, ts.URL+"/api/ingest/detections", body,
		map[string]string{"X-Ingest-Token": "machine-token"})
	if status != http.StatusAccepted {
		t.Fatalf("expected 202 with ingest token, got %d %s", status, response)
	}
}

func TestWebSocketRequiresTokenWhenAuthOn(t *testing.T) {
	ts := newP2Server(t, "on", "")
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	if _, _, err := websocket.DefaultDialer.Dial(wsURL, nil); err == nil {
		t.Fatal("expected websocket handshake to fail without token")
	}
	token := login(t, ts, "viewer", "Viewer@123")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL+"?token="+token, nil)
	if err != nil {
		t.Fatalf("websocket with token: %v", err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var hello struct {
		Type string `json:"type"`
	}
	if err := conn.ReadJSON(&hello); err != nil || hello.Type != "hello" {
		t.Fatalf("expected hello frame, got %q err=%v", hello.Type, err)
	}
}
