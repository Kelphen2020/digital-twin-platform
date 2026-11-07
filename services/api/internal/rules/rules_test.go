package rules

import (
	"testing"
	"time"

	"digital-twin-platform/api/internal/model"
)

func testCamera() model.Camera {
	return model.Camera{ID: "cam_001", Code: "CAM-001", Name: "东门入口", X: 0, Y: 0, Z: 6, Heading: 90}
}

func TestDropsLowConfidence(t *testing.T) {
	engine := New(5 * time.Second)
	result := engine.Evaluate(testCamera(), []model.Detection{{Class: "person", Confidence: 0.4}}, time.Now())
	if len(result.Events) != 0 || result.Dropped != 1 {
		t.Fatalf("expected drop, got events=%d dropped=%d", len(result.Events), result.Dropped)
	}
}

func TestMapsClassToTypeAndSeverity(t *testing.T) {
	cases := []struct {
		class    string
		wantType string
		wantSev  string
	}{
		{"person", "person", "info"},
		{"vehicle", "vehicle", "info"},
		{"intrusion", "intrusion", "warn"},
		{"smoke", "smoke", "critical"},
		{"no_helmet", "no_helmet", "warn"},
		{"weird", "other", "info"},
	}
	engine := New(time.Second)
	for i, testCase := range cases {
		result := engine.Evaluate(testCamera(), []model.Detection{{Class: testCase.class, Confidence: 0.9}}, time.Now().Add(time.Duration(i)*time.Minute))
		if len(result.Events) != 1 {
			t.Fatalf("%s: expected one event, got %d", testCase.class, len(result.Events))
		}
		event := result.Events[0]
		if event.Type != testCase.wantType || event.Severity != testCase.wantSev {
			t.Fatalf("%s: got %s/%s want %s/%s", testCase.class, event.Type, event.Severity, testCase.wantType, testCase.wantSev)
		}
	}
}

func TestDedupeWindow(t *testing.T) {
	engine := New(5 * time.Second)
	base := time.Now()
	first := engine.Evaluate(testCamera(), []model.Detection{{Class: "person", Confidence: 0.9}}, base)
	if len(first.Events) != 1 {
		t.Fatalf("first event missing")
	}
	second := engine.Evaluate(testCamera(), []model.Detection{{Class: "person", Confidence: 0.9}}, base.Add(2*time.Second))
	if len(second.Events) != 0 || second.Dropped != 1 {
		t.Fatalf("expected dedupe drop, got events=%d dropped=%d", len(second.Events), second.Dropped)
	}
	third := engine.Evaluate(testCamera(), []model.Detection{{Class: "person", Confidence: 0.9}}, base.Add(6*time.Second))
	if len(third.Events) != 1 {
		t.Fatalf("expected new event after window, got %d", len(third.Events))
	}
}

func TestWorldAndFallbackPosition(t *testing.T) {
	engine := New(time.Second)
	withWorld := engine.Evaluate(testCamera(), []model.Detection{{
		Class: "person", Confidence: 0.9, World: &model.World{X: 12, Y: -3, Z: 0},
	}}, time.Now())
	if len(withWorld.Events) != 1 || withWorld.Events[0].X != 12 || withWorld.Events[0].Y != -3 {
		t.Fatalf("world position not used: %+v", withWorld.Events)
	}

	fallback := engine.Evaluate(testCamera(), []model.Detection{{Class: "vehicle", Confidence: 0.9}}, time.Now().Add(time.Minute))
	if len(fallback.Events) != 1 {
		t.Fatalf("expected fallback event")
	}
	event := fallback.Events[0]
	// heading 90° (east) at 30 m: X = 30, Y = 0.
	if event.X < 29 || event.X > 31 || event.Y < -1 || event.Y > 1 {
		t.Fatalf("unexpected fallback position: (%f, %f)", event.X, event.Y)
	}
}
