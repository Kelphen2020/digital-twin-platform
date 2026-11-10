package store_test

import (
	"testing"

	"digital-twin-platform/api/internal/store"
)

func TestSeedDataIsConsistent(t *testing.T) {
	cameras := store.SeedCameras()
	if len(cameras) != 6 {
		t.Fatalf("expected 6 seeded cameras, got %d", len(cameras))
	}
	seen := map[string]bool{}
	for _, camera := range cameras {
		if camera.ID == "" || camera.Code == "" || camera.AreaID == "" {
			t.Fatalf("camera missing required fields: %+v", camera)
		}
		if seen[camera.ID] || seen[camera.Code] {
			t.Fatalf("duplicate camera id/code: %+v", camera)
		}
		seen[camera.ID] = true
		seen[camera.Code] = true
		if camera.FovH <= 0 || camera.FovV <= 0 || camera.RangeM <= 0 {
			t.Fatalf("camera optics out of range: %+v", camera)
		}
	}
	areas := store.SeedAreas()
	if len(areas) == 0 {
		t.Fatal("expected seeded areas")
	}
	known := map[string]bool{}
	for _, area := range areas {
		if area.ID == "" || area.Name == "" {
			t.Fatalf("area missing fields: %+v", area)
		}
		known[area.ID] = true
	}
	for _, camera := range cameras {
		if !known[camera.AreaID] {
			t.Fatalf("camera %s references unknown area %q", camera.Code, camera.AreaID)
		}
	}
}
