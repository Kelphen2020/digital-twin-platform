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
	result := engine.Evaluate(testCamera(), model.DefaultCameraRules("cam_001"), []model.Detection{{Class: "person", Confidence: 0.4}}, time.Now())
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
	cfg := model.DefaultCameraRules("cam_001")
	cfg.EnabledClasses = nil // allow every class so the mapping itself is tested
	for i, testCase := range cases {
		result := engine.Evaluate(testCamera(), cfg, []model.Detection{{Class: testCase.class, Confidence: 0.9}}, time.Now().Add(time.Duration(i)*time.Minute))
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
	first := engine.Evaluate(testCamera(), model.DefaultCameraRules("cam_001"), []model.Detection{{Class: "person", Confidence: 0.9}}, base)
	if len(first.Events) != 1 {
		t.Fatalf("first event missing")
	}
	second := engine.Evaluate(testCamera(), model.DefaultCameraRules("cam_001"), []model.Detection{{Class: "person", Confidence: 0.9}}, base.Add(2*time.Second))
	if len(second.Events) != 0 || second.Dropped != 1 {
		t.Fatalf("expected dedupe drop, got events=%d dropped=%d", len(second.Events), second.Dropped)
	}
	third := engine.Evaluate(testCamera(), model.DefaultCameraRules("cam_001"), []model.Detection{{Class: "person", Confidence: 0.9}}, base.Add(6*time.Second))
	if len(third.Events) != 1 {
		t.Fatalf("expected new event after window, got %d", len(third.Events))
	}
}

func TestWorldAndFallbackPosition(t *testing.T) {
	engine := New(time.Second)
	withWorld := engine.Evaluate(testCamera(), model.DefaultCameraRules("cam_001"), []model.Detection{{
		Class: "person", Confidence: 0.9, World: &model.World{X: 12, Y: -3, Z: 0},
	}}, time.Now())
	if len(withWorld.Events) != 1 || withWorld.Events[0].X != 12 || withWorld.Events[0].Y != -3 {
		t.Fatalf("world position not used: %+v", withWorld.Events)
	}

	fallback := engine.Evaluate(testCamera(), model.DefaultCameraRules("cam_001"), []model.Detection{{Class: "vehicle", Confidence: 0.9}}, time.Now().Add(time.Minute))
	if len(fallback.Events) != 1 {
		t.Fatalf("expected fallback event")
	}
	event := fallback.Events[0]
	// heading 90° (east) at 30 m: X = 30, Y = 0.
	if event.X < 29 || event.X > 31 || event.Y < -1 || event.Y > 1 {
		t.Fatalf("unexpected fallback position: (%f, %f)", event.X, event.Y)
	}
}

func TestProjectToWorldSources(t *testing.T) {
	camera := model.Camera{ID: "cam_001", X: 10, Y: 20, Z: 6, Heading: 90}

	// 1) explicit world coordinates win.
	x, y, z, source := ProjectToWorld(camera, model.Detection{World: &model.World{X: 1, Y: 2, Z: 3}})
	if source != "world" || x != 1 || y != 2 || z != 3 {
		t.Fatalf("world passthrough: %v %v %v %s", x, y, z, source)
	}

	// 2) calibration matrix is applied to the bbox ground point.
	calibrated := camera
	calibrated.Calibration = &model.Calibration{Matrix: [3][3]float64{{2, 0, 0}, {0, 2, 0}, {0, 0, 1}}}
	x, y, _, source = ProjectToWorld(calibrated, model.Detection{BBox: [4]float64{0.4, 0.2, 0.6, 0.5}})
	if source != "calibration" || x != 1920 || y != 1080 {
		t.Fatalf("calibration projection: %v %v %s", x, y, source)
	}

	// 3) degenerate matrix (w ~ 0) falls back to the optical axis.
	degenerate := camera
	degenerate.Calibration = &model.Calibration{Matrix: [3][3]float64{{1, 0, 0}, {0, 1, 0}, {0, 0, 0}}}
	_, _, _, source = ProjectToWorld(degenerate, model.Detection{BBox: [4]float64{0.5, 0.5, 0.5, 0.5}})
	if source != "fallback" {
		t.Fatalf("degenerate calibration should fall back, got %s", source)
	}

	// 4) fallback places the event FallbackDistanceM along the heading.
	x, y, _, source = ProjectToWorld(camera, model.Detection{BBox: [4]float64{0.5, 0.5, 0.5, 0.5}})
	if source != "fallback" {
		t.Fatalf("expected fallback, got %s", source)
	}
	if x < 10+FallbackDistanceM-0.01 || x > 10+FallbackDistanceM+0.01 {
		t.Fatalf("east offset wrong: x=%v", x)
	}
	if y < 20-0.01 || y > 20+0.01 {
		t.Fatalf("north offset wrong: y=%v", y)
	}
}

func TestROIFiltersDetections(t *testing.T) {
	engine := New(time.Second)
	cfg := model.DefaultCameraRules("cam_001")
	cfg.ROI = [][2]float64{{0.2, 0.2}, {0.8, 0.2}, {0.8, 0.8}, {0.2, 0.8}}

	inside := engine.Evaluate(testCamera(), cfg, []model.Detection{
		{Class: "person", Confidence: 0.9, BBox: [4]float64{0.4, 0.3, 0.6, 0.5}},
	}, time.Now())
	if len(inside.Events) != 1 {
		t.Fatalf("detection inside ROI should pass, got %d events / %d dropped", len(inside.Events), inside.Dropped)
	}
	outside := engine.Evaluate(testCamera(), cfg, []model.Detection{
		{Class: "vehicle", Confidence: 0.9, BBox: [4]float64{0.05, 0.05, 0.15, 0.15}},
	}, time.Now().Add(time.Second))
	if len(outside.Events) != 0 || outside.Dropped != 1 {
		t.Fatalf("detection outside ROI should be dropped, got %d/%d", len(outside.Events), outside.Dropped)
	}
}

func TestEnabledClassesFilter(t *testing.T) {
	engine := New(time.Second)
	cfg := model.DefaultCameraRules("cam_001")
	cfg.EnabledClasses = []string{"person"}
	result := engine.Evaluate(testCamera(), cfg, []model.Detection{
		{Class: "vehicle", Confidence: 0.95},
		{Class: "person", Confidence: 0.95},
	}, time.Now())
	if len(result.Events) != 1 || result.Events[0].Type != "person" || result.Dropped != 1 {
		t.Fatalf("enabled classes filter: %+v", result)
	}
}

func TestDedupeWindowDisabled(t *testing.T) {
	engine := New(time.Minute)
	cfg := model.DefaultCameraRules("cam_001")
	cfg.DedupeSeconds = 0 // explicit zero disables dedupe for a configured camera
	base := time.Now()
	first := engine.Evaluate(testCamera(), cfg, []model.Detection{{Class: "person", Confidence: 0.9}}, base)
	second := engine.Evaluate(testCamera(), cfg, []model.Detection{{Class: "person", Confidence: 0.9}}, base.Add(time.Millisecond))
	if len(first.Events) != 1 || len(second.Events) != 1 {
		t.Fatalf("dedupe should be disabled: %d/%d", len(first.Events), len(second.Events))
	}
}

func TestPruneBoundsDedupeMap(t *testing.T) {
	engine := New(time.Millisecond)
	base := time.Now()
	// Populate more than the prune threshold with distinct cameras.
	for index := 0; index < 4200; index++ {
		camera := testCamera()
		camera.ID = "cam_" + string(rune('a'+index%26)) + "-" + time.Duration(index).String()
		cfg := model.DefaultCameraRules(camera.ID)
		cfg.DedupeSeconds = 0
		engine.Evaluate(camera, cfg, []model.Detection{{Class: "person", Confidence: 0.9}}, base)
	}
	// A later evaluation far in the future must shrink the map.
	future := base.Add(time.Hour)
	engine.Evaluate(testCamera(), model.CameraRules{}, []model.Detection{{Class: "person", Confidence: 0.9}}, future)
	if len(engine.last) >= 4200 {
		t.Fatalf("dedupe map was not pruned: %d keys", len(engine.last))
	}
}

func TestNewClampsWindow(t *testing.T) {
	engine := New(0)
	if engine.window != 5*time.Second {
		t.Fatalf("zero window should default to 5s, got %v", engine.window)
	}
	engine = New(-time.Second)
	if engine.window != 5*time.Second {
		t.Fatalf("negative window should default to 5s, got %v", engine.window)
	}
}
