package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"digital-twin-platform/api/internal/bus"
)

func TestBusStatusReplayAndPermissions(t *testing.T) {
	ts, server := newP3Server(t)
	server.AttachBus(bus.NewMemory(1000))
	admin := login(t, ts, "admin", "Admin@123456")
	operator := login(t, ts, "operator", "Operator@123")

	status, body := authorized(t, http.MethodGet, ts.URL+"/api/bus/status", admin, nil)
	if status != http.StatusOK || !strings.Contains(string(body), `"driver":"memory"`) {
		t.Fatalf("bus status: %d %s", status, body)
	}
	var before struct {
		NextOffset int64 `json:"next_offset"`
	}
	_ = json.Unmarshal(body, &before)

	// Ingest one event; the bus offset must advance.
	eventID, accepted := ingestAt(t, ts, "CAM-001", "intrusion", 0.95, time.Now())
	if accepted != 1 {
		t.Fatal("failed to ingest bus test event")
	}
	deadline := time.Now().Add(5 * time.Second)
	var after struct {
		Published  int64 `json:"published"`
		NextOffset int64 `json:"next_offset"`
		Replayed   int64 `json:"replayed"`
	}
	for time.Now().Before(deadline) {
		status, body = authorized(t, http.MethodGet, ts.URL+"/api/bus/status", admin, nil)
		_ = json.Unmarshal(body, &after)
		if status == http.StatusOK && after.NextOffset > before.NextOffset {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if after.NextOffset <= before.NextOffset {
		t.Fatalf("bus offset did not advance: before=%d after=%d body=%s", before.NextOffset, after.NextOffset, body)
	}

	// A replay re-broadcasts the event as an event_replay frame.
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws?token=" + admin
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial ws: %v", err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		var frame struct {
			Type string `json:"type"`
		}
		if err := conn.ReadJSON(&frame); err != nil {
			t.Fatalf("read hello: %v", err)
		}
		if frame.Type == "hello" {
			break
		}
	}

	status, body = authorized(t, http.MethodPost, ts.URL+"/api/bus/replay", admin, map[string]any{
		"from_offset": before.NextOffset, "to_offset": after.NextOffset, "notify": false, "limit": 10,
	})
	if status != http.StatusOK || !strings.Contains(string(body), `"ok":true`) {
		t.Fatalf("replay: %d %s", status, body)
	}
	var replayed struct {
		Replayed int `json:"replayed"`
	}
	_ = json.Unmarshal(body, &replayed)
	if replayed.Replayed < 1 {
		t.Fatalf("expected at least one replayed envelope: %s", body)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	sawReplay := false
	for i := 0; i < 10 && !sawReplay; i++ {
		var frame struct {
			Type string `json:"type"`
			Data struct {
				Replay bool `json:"replay"`
				Event  struct {
					ID string `json:"id"`
				} `json:"event"`
			} `json:"data"`
		}
		if err := conn.ReadJSON(&frame); err != nil {
			break
		}
		if frame.Type == "event_replay" && frame.Data.Replay {
			sawReplay = true
		}
	}
	if !sawReplay {
		t.Fatal("websocket never received an event_replay frame")
	}

	status, body = authorized(t, http.MethodGet, ts.URL+"/api/bus/status", admin, nil)
	if status != http.StatusOK || !strings.Contains(string(body), `"replayed":`) {
		t.Fatalf("bus status after replay: %d %s", status, body)
	}

	// Validation and permissions.
	status, _ = authorized(t, http.MethodPost, ts.URL+"/api/bus/replay", admin, map[string]any{
		"from_offset": 10, "to_offset": 1,
	})
	if status != http.StatusBadRequest {
		t.Fatalf("invalid replay range should be 400, got %d", status)
	}
	status, _ = authorized(t, http.MethodPost, ts.URL+"/api/bus/replay", operator, map[string]any{"from_offset": 0})
	if status != http.StatusForbidden {
		t.Fatalf("operator replay should be 403, got %d", status)
	}
	status, _ = authorized(t, http.MethodGet, ts.URL+"/api/bus/status", operator, nil)
	if status != http.StatusForbidden {
		t.Fatalf("operator bus status should be 403, got %d", status)
	}

	// Metrics expose bus counters.
	status, body = authorized(t, http.MethodGet, ts.URL+"/metrics", admin, nil)
	if status != http.StatusOK {
		t.Fatalf("metrics: %d", status)
	}
	for _, needle := range []string{"dsh_twin_bus_published_total", "dsh_twin_bus_replayed_total", "dsh_twin_bus_errors_total"} {
		if !strings.Contains(string(body), needle) {
			t.Fatalf("metrics missing %q:\n%s", needle, body)
		}
	}
	_ = eventID
}
