package realtime_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"digital-twin-platform/api/internal/realtime"
)

func dial(t *testing.T, server *httptest.Server) *websocket.Conn {
	t.Helper()
	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func readFrame(t *testing.T, conn *websocket.Conn) realtime.Frame {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}
	var frame realtime.Frame
	if err := json.Unmarshal(payload, &frame); err != nil {
		t.Fatalf("decode frame: %v", err)
	}
	return frame
}

func waitClients(t *testing.T, hub *realtime.Hub, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if hub.Clients() == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("clients = %d, want %d", hub.Clients(), want)
}

func TestHubBroadcastAndGreeting(t *testing.T) {
	hub := realtime.NewHub()
	hub.OnConnect = func() realtime.Frame {
		return realtime.Frame{Type: "hello", Data: map[string]string{"server": "test"}}
	}
	server := httptest.NewServer(hub)
	defer server.Close()

	first := dial(t, server)
	greeting := readFrame(t, first)
	if greeting.Type != "hello" {
		t.Fatalf("expected greeting frame, got %+v", greeting)
	}
	waitClients(t, hub, 1)
	second := dial(t, server)
	_ = readFrame(t, second)
	waitClients(t, hub, 2)

	hub.Broadcast(realtime.Frame{Type: "event_created", Data: map[string]any{"id": "evt_1"}})
	for _, conn := range []*websocket.Conn{first, second} {
		frame := readFrame(t, conn)
		if frame.Type != "event_created" {
			t.Fatalf("unexpected frame: %+v", frame)
		}
	}
	if hub.Frames() < 2 {
		t.Fatalf("frames counter = %d, want >= 2", hub.Frames())
	}
	if hub.Dropped() != 0 {
		t.Fatalf("dropped = %d, want 0", hub.Dropped())
	}

	_ = first.Close()
	waitClients(t, hub, 1)
}

func TestHubDropsWhenClientBufferFull(t *testing.T) {
	// A deliberately stalled client (no reader goroutine) has a send queue that
	// fills, so the hub must drop frames instead of blocking the broadcaster.
	hub := realtime.NewHubWithBuffer(16) // clamped minimum
	server := httptest.NewServer(hub)
	defer server.Close()
	_ = dial(t, server)
	waitClients(t, hub, 1)

	for index := 0; index < 5000; index++ {
		hub.Broadcast(realtime.Frame{Type: "track_point", Data: strings.Repeat("x", 512)})
	}
	if hub.Dropped() == 0 {
		t.Fatal("expected dropped frames when the client cannot keep up")
	}
	if hub.Frames() == 0 {
		t.Fatal("expected some frames to be queued before the buffer filled")
	}
	if hub.Clients() != 1 {
		t.Fatalf("slow client must stay connected, clients=%d", hub.Clients())
	}
}

func TestHubNoClientsAndBadFrame(t *testing.T) {
	hub := realtime.NewHub()
	hub.Broadcast(realtime.Frame{Type: "event_created", Data: map[string]any{"id": "x"}})
	if hub.Frames() != 0 || hub.Clients() != 0 {
		t.Fatalf("broadcast without clients should be a no-op: frames=%d clients=%d", hub.Frames(), hub.Clients())
	}
	// A non-marshalable frame is dropped without panicking or counting.
	hub.Broadcast(realtime.Frame{Type: "bad", Data: make(chan int)})
	if hub.Frames() != 0 {
		t.Fatalf("bad frame should not be counted: %d", hub.Frames())
	}

	// A non-websocket request is rejected and never becomes a client.
	server := httptest.NewServer(hub)
	defer server.Close()
	response, err := http.Get(server.URL + "/ws")
	if err != nil {
		t.Fatalf("plain GET: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("plain GET should be 400, got %d", response.StatusCode)
	}
	if hub.Clients() != 0 {
		t.Fatalf("failed upgrade must not register a client: %d", hub.Clients())
	}
}

func TestHubBufferClamp(t *testing.T) {
	hub := realtime.NewHubWithBuffer(1)
	server := httptest.NewServer(hub)
	defer server.Close()
	conn := dial(t, server)
	waitClients(t, hub, 1)
	hub.Broadcast(realtime.Frame{Type: "event_created", Data: "x"})
	if frame := readFrame(t, conn); frame.Type != "event_created" {
		t.Fatalf("unexpected frame: %+v", frame)
	}
}
