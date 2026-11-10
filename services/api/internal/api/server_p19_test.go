package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestIngestFallbackForwarding verifies a replica without the camera forwards
// the raw ingest payload and relays the upstream response.
func TestIngestFallbackForwarding(t *testing.T) {
	var forwarded atomic.Int64
	var received atomic.Value
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/ingest/detections" {
			http.NotFound(w, r)
			return
		}
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		received.Store(payload)
		forwarded.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"accepted":1,"dropped":0}`))
	}))
	defer fallback.Close()

	ts, server := newP9Server(t)
	server.SetIngestFallback(fallback.URL)

	status, body := doJSON(t, http.MethodPost, ts.URL+"/api/ingest/detections", map[string]any{
		"camera_code": "CAM-001",
		"detections":  []map[string]any{{"class": "intrusion", "confidence": 0.91}},
	})
	if status != http.StatusAccepted || !strings.Contains(string(body), `"accepted":1`) {
		t.Fatalf("fallback response not relayed: %d %s", status, body)
	}
	if forwarded.Load() != 1 {
		t.Fatalf("expected 1 forwarded request, got %d", forwarded.Load())
	}
	payload, _ := received.Load().(map[string]any)
	if payload["camera_code"] != "CAM-001" {
		t.Fatalf("payload not forwarded verbatim: %+v", payload)
	}

	// An unreachable fallback yields 502.
	server.SetIngestFallback("http://127.0.0.1:1")
	status, body = doJSON(t, http.MethodPost, ts.URL+"/api/ingest/detections", map[string]any{
		"camera_code": "CAM-001",
		"detections":  []map[string]any{{"class": "intrusion", "confidence": 0.9}},
	})
	if status != http.StatusBadGateway {
		t.Fatalf("unreachable fallback should be 502, got %d %s", status, body)
	}
}

// TestIngestBodyLimitWithoutGuard ensures the default 1 MiB cap still applies.
func TestIngestBodyLimitWithoutGuard(t *testing.T) {
	ts, server := newP9Server(t)
	server.SetIngestGuard(nil, 0)
	big := strings.Repeat("x", (1<<20)+1024)
	status, _ := doJSON(t, http.MethodPost, ts.URL+"/api/ingest/detections", map[string]any{
		"camera_code": "CAM-001", "pad": big,
	})
	if status != http.StatusRequestEntityTooLarge {
		t.Fatalf("default body cap should yield 413, got %d", status)
	}
	_ = time.Now()
}
