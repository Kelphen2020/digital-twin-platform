package bus

import (
	"os"
	"path/filepath"
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

func tenantEvent(tenantID string) model.Event {
	event := testEvent()
	event.TenantID = tenantID
	return event
}

func TestMemoryBusTenantIsolation(t *testing.T) {
	bus := NewMemory(20)
	for i := 0; i < 2; i++ {
		if _, err := bus.Publish(KindEventCreated, tenantEvent("tenant_b"), nil, "test"); err != nil {
			t.Fatalf("publish tenant_b: %v", err)
		}
	}
	if _, err := bus.Publish(KindEventCreated, testEvent(), nil, "test"); err != nil {
		t.Fatalf("publish default: %v", err)
	}
	tenantB, err := bus.ReadRangeForTenant(0, -1, 10, "tenant_b", false)
	if err != nil || len(tenantB) != 2 {
		t.Fatalf("tenant_b read: %v %d", err, len(tenantB))
	}
	for _, envelope := range tenantB {
		if envelope.TenantID != "tenant_b" {
			t.Fatalf("unexpected tenant envelope: %+v", envelope)
		}
	}
	all, err := bus.ReadRangeForTenant(0, -1, 10, "tenant_b", true)
	if err != nil || len(all) != 3 {
		t.Fatalf("all_tenants read: %v %d", err, len(all))
	}
	defaultOnly, err := bus.ReadRangeForTenant(0, -1, 10, "default", false)
	if err != nil || len(defaultOnly) != 1 || defaultOnly[0].TenantID != "default" {
		t.Fatalf("default read: %v %+v", err, defaultOnly)
	}
	status := bus.Status()
	if status.Tenants["tenant_b"] != 2 || status.Tenants["default"] != 1 {
		t.Fatalf("status tenants: %+v", status.Tenants)
	}
}

func TestJSONLBusTenantBackwardCompatibility(t *testing.T) {
	dir := t.TempDir()
	// Simulate a pre-P29 jsonl line without tenant_id.
	legacy := `{"offset":0,"kind":"event_created","at":"2026-11-10T00:00:00Z","event":{"id":"evt_legacy","camera_id":"cam_001"},"source":"test"}`
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(legacy+"\n"), 0o644); err != nil {
		t.Fatalf("write legacy line: %v", err)
	}
	bus, err := NewJSONL(dir)
	if err != nil {
		t.Fatalf("new jsonl bus: %v", err)
	}
	legacyEnvelopes, err := bus.ReadRangeForTenant(0, -1, 10, "default", false)
	if err != nil || len(legacyEnvelopes) != 1 || legacyEnvelopes[0].Event.ID != "evt_legacy" {
		t.Fatalf("legacy line should map to default tenant: %v %+v", err, legacyEnvelopes)
	}
	if _, err := bus.Publish(KindEventCreated, tenantEvent("tenant_b"), nil, "test"); err != nil {
		t.Fatalf("publish tenant_b: %v", err)
	}
	tenantB, err := bus.ReadRangeForTenant(0, -1, 10, "tenant_b", false)
	if err != nil || len(tenantB) != 1 || tenantB[0].TenantID != "tenant_b" {
		t.Fatalf("tenant_b read after legacy line: %v %+v", err, tenantB)
	}
	// The new line must persist tenant_id and survive a restart.
	reopened, err := NewJSONL(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	tenantB, err = reopened.ReadRangeForTenant(0, -1, 10, "tenant_b", false)
	if err != nil || len(tenantB) != 1 || tenantB[0].TenantID != "tenant_b" {
		t.Fatalf("tenant_b after reopen: %v %+v", err, tenantB)
	}
}

// P36: replay cursor drives consumer lag reporting.
func TestBusLagTracking(t *testing.T) {
	bus := NewMemory(100)
	if lag, ok := bus.Lag(); ok || lag != 0 {
		t.Fatalf("fresh bus should have no cursor: lag=%d ok=%v", lag, ok)
	}
	for i := 0; i < 3; i++ {
		if _, err := bus.Publish("event", model.Event{ID: "evt_lag_" + string(rune('a'+i))}, nil, "test"); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}
	bus.SetReplayCursor(1)
	lag, ok := bus.Lag()
	if !ok || lag != 1 {
		t.Fatalf("lag after cursor=1 of 3: %d %v", lag, ok)
	}
	bus.SetReplayCursor(2)
	if lag, _ := bus.Lag(); lag != 0 {
		t.Fatalf("lag should be 0 at the tail, got %d", lag)
	}
}
