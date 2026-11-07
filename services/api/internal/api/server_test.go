package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"digital-twin-platform/api/internal/api"
	"digital-twin-platform/api/internal/metrics"
	"digital-twin-platform/api/internal/realtime"
	"digital-twin-platform/api/internal/rules"
	"digital-twin-platform/api/internal/store"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	memory := store.NewMemory(1000)
	memory.Seed(store.SeedCameras(), store.SeedAreas())
	server := api.New(memory, rules.New(5*time.Second), realtime.NewHub(), metrics.New(), "test", "")
	ts := httptest.NewServer(server.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func doJSON(t *testing.T, method, url string, body any) (int, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	request, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return response.StatusCode, payload
}

func TestHealthAndCameras(t *testing.T) {
	ts := newTestServer(t)
	status, body := doJSON(t, http.MethodGet, ts.URL+"/api/health", nil)
	if status != http.StatusOK || !strings.Contains(string(body), `"status":"ok"`) {
		t.Fatalf("health: %d %s", status, body)
	}
	status, body = doJSON(t, http.MethodGet, ts.URL+"/api/cameras", nil)
	if status != http.StatusOK {
		t.Fatalf("cameras: %d %s", status, body)
	}
	var list struct {
		Items []struct {
			Code string `json:"code"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("decode cameras: %v", err)
	}
	if len(list.Items) != 6 {
		t.Fatalf("expected 6 seeded cameras, got %d", len(list.Items))
	}
}

func TestIngestEventAckAndMetrics(t *testing.T) {
	ts := newTestServer(t)
	detection := map[string]any{
		"camera_code": "CAM-001",
		"detections": []map[string]any{{
			"class":      "intrusion",
			"confidence": 0.93,
			"bbox":       []float64{0.4, 0.3, 0.6, 0.8},
		}},
	}
	status, body := doJSON(t, http.MethodPost, ts.URL+"/api/ingest/detections", detection)
	if status != http.StatusAccepted {
		t.Fatalf("ingest: %d %s", status, body)
	}
	var ingest struct {
		Accepted int `json:"accepted"`
		Events   []struct {
			ID       string `json:"id"`
			Type     string `json:"type"`
			Severity string `json:"severity"`
			Status   string `json:"status"`
		} `json:"events"`
	}
	if err := json.Unmarshal(body, &ingest); err != nil {
		t.Fatalf("decode ingest: %v", err)
	}
	if ingest.Accepted != 1 || len(ingest.Events) != 1 {
		t.Fatalf("unexpected ingest result: %s", body)
	}
	event := ingest.Events[0]
	if event.Type != "intrusion" || event.Severity != "warn" || event.Status != "new" {
		t.Fatalf("unexpected event: %+v", event)
	}

	status, body = doJSON(t, http.MethodGet, ts.URL+"/api/events?limit=10", nil)
	if status != http.StatusOK || !strings.Contains(string(body), event.ID) {
		t.Fatalf("events list missing event: %d %s", status, body)
	}

	status, body = doJSON(t, http.MethodPost, fmt.Sprintf("%s/api/events/%s/ack", ts.URL, event.ID), nil)
	if status != http.StatusOK || !strings.Contains(string(body), `"status":"acked"`) {
		t.Fatalf("ack: %d %s", status, body)
	}

	status, body = doJSON(t, http.MethodGet, ts.URL+"/metrics", nil)
	if status != http.StatusOK {
		t.Fatalf("metrics: %d", status)
	}
	for _, needle := range []string{"dsh_twin_events_total{severity=\"warn\"} 1", "dsh_twin_detections_total 1", "dsh_twin_ws_clients 0"} {
		if !strings.Contains(string(body), needle) {
			t.Fatalf("metrics missing %q:\n%s", needle, body)
		}
	}
}

func TestIngestUnknownCamera(t *testing.T) {
	ts := newTestServer(t)
	status, body := doJSON(t, http.MethodPost, ts.URL+"/api/ingest/detections", map[string]any{
		"camera_code": "CAM-999",
		"detections":  []map[string]any{{"class": "person", "confidence": 0.9}},
	})
	if status != http.StatusNotFound {
		t.Fatalf("expected 404, got %d %s", status, body)
	}
}

func TestPatchCameraStatusBroadcasts(t *testing.T) {
	ts := newTestServer(t)
	status, body := doJSON(t, http.MethodPatch, ts.URL+"/api/cameras/cam_001", map[string]any{"status": "offline"})
	if status != http.StatusOK || !strings.Contains(string(body), `"status":"offline"`) {
		t.Fatalf("patch: %d %s", status, body)
	}
	status, body = doJSON(t, http.MethodPatch, ts.URL+"/api/cameras/cam_001", map[string]any{"status": "broken"})
	if status != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid status, got %d %s", status, body)
	}
}

func TestWebSocketReceivesHelloAndEvents(t *testing.T) {
	ts := newTestServer(t)
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial ws: %v", err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))

	var hello struct {
		Type string `json:"type"`
	}
	if err := conn.ReadJSON(&hello); err != nil {
		t.Fatalf("read hello: %v", err)
	}
	if hello.Type != "hello" {
		t.Fatalf("expected hello, got %q", hello.Type)
	}

	doJSON(t, http.MethodPost, ts.URL+"/api/ingest/detections", map[string]any{
		"camera_code": "CAM-005",
		"detections":  []map[string]any{{"class": "smoke", "confidence": 0.97}},
	})

	var frame struct {
		Type string         `json:"type"`
		Data map[string]any `json:"data"`
	}
	if err := conn.ReadJSON(&frame); err != nil {
		t.Fatalf("read event: %v", err)
	}
	if frame.Type != "event" {
		t.Fatalf("expected event frame, got %q", frame.Type)
	}
	if frame.Data["type"] != "smoke" || frame.Data["severity"] != "critical" {
		t.Fatalf("unexpected event frame: %+v", frame.Data)
	}
}
