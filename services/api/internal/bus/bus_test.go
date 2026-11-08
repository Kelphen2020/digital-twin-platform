package bus

import (
	"testing"
	"time"

	"digital-twin-platform/api/internal/model"
)

func testEvent() model.Event {
	return model.Event{
		ID: "evt_test", CameraID: "cam_001", CameraCode: "CAM-001", CameraName: "东门入口",
		Type: "person", Severity: "warn", Confidence: 0.9, OccurredAt: time.Now().UTC(), Status: "new",
	}
}

func TestMemoryBusPublishReadAndSubscribe(t *testing.T) {
	bus := NewMemory(10)
	var seen []Envelope
	bus.Subscribe(func(envelope Envelope) { seen = append(seen, envelope) })

	for i := 0; i < 3; i++ {
		if _, err := bus.Publish(KindEventCreated, testEvent(), nil, "test"); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}
	if len(seen) != 3 {
		t.Fatalf("subscriber saw %d envelopes", len(seen))
	}
	if seen[2].Offset != 2 || seen[2].Kind != KindEventCreated {
		t.Fatalf("unexpected envelope: %+v", seen[2])
	}

	envelopes, err := bus.ReadRange(1, 2, 10)
	if err != nil || len(envelopes) != 2 || envelopes[0].Offset != 1 {
		t.Fatalf("read range: %v %d %+v", err, len(envelopes), envelopes)
	}
	status := bus.Status()
	if status.Driver != "memory" || status.Published != 3 || status.NextOffset != 3 || status.Subscribers != 1 {
		t.Fatalf("status: %+v", status)
	}
}

func TestMemoryBusRangeUnavailableAfterTrim(t *testing.T) {
	bus := NewMemory(2)
	for i := 0; i < 4; i++ {
		_, _ = bus.Publish(KindEventCreated, testEvent(), nil, "test")
	}
	if _, err := bus.ReadRange(0, 3, 10); err == nil {
		t.Fatal("expected ErrRangeUnavailable after trimming")
	}
	envelopes, err := bus.ReadRange(2, -1, 10)
	if err != nil || len(envelopes) != 2 {
		t.Fatalf("tail read: %v %d", err, len(envelopes))
	}
}

func TestJSONLBusPersistsAndReplaysAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	bus, err := NewJSONL(dir)
	if err != nil {
		t.Fatalf("new jsonl bus: %v", err)
	}
	for i := 0; i < 2; i++ {
		if _, err := bus.Publish(KindEventCreated, testEvent(), nil, "test"); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}
	first, err := bus.ReadRange(0, -1, 10)
	if err != nil || len(first) != 2 {
		t.Fatalf("read before restart: %v %d", err, len(first))
	}

	reopened, err := NewJSONL(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	status := reopened.Status()
	if status.NextOffset != 2 {
		t.Fatalf("expected restart offset 2, got %+v", status)
	}
	envelope, err := reopened.Publish(KindEventEscalated, testEvent(), nil, "test")
	if err != nil || envelope.Offset != 2 {
		t.Fatalf("publish after restart: %v %+v", err, envelope)
	}
	all, err := reopened.ReadRange(0, -1, 10)
	if err != nil || len(all) != 3 || all[2].Kind != KindEventEscalated {
		t.Fatalf("history after restart: %v %d", err, len(all))
	}
}
