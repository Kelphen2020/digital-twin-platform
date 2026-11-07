// Seed data for the P0 demo site: a 400x400 m campus with two buildings, a
// gate, a car park and a main road. Coordinates are local ENU metres.
package store

import (
	"time"

	"digital-twin-platform/api/internal/model"
)

// SeedAreas returns the demo zones.
func SeedAreas() []model.Area {
	return []model.Area{
		{ID: "area_building_a", Name: "1号楼", Type: "building", Polygon: [][2]float64{{-70, 20}, {-10, 20}, {-10, 60}, {-70, 60}}},
		{ID: "area_building_b", Name: "2号楼", Type: "building", Polygon: [][2]float64{{35, -35}, {85, -35}, {85, -5}, {35, -5}}},
		{ID: "area_gate", Name: "东门", Type: "zone", Polygon: [][2]float64{{-120, -100}, {-60, -100}, {-60, -20}, {-120, -20}}},
		{ID: "area_parking", Name: "停车场", Type: "zone", Polygon: [][2]float64{{10, -110}, {70, -110}, {70, -50}, {10, -50}}},
	}
}

// SeedCameras returns the six demo cameras.
func SeedCameras() []model.Camera {
	now := time.Now().UTC()
	online := "online"
	stream := "/hls/test.m3u8"
	return []model.Camera{
		{ID: "cam_001", Code: "CAM-001", Name: "东门入口", AreaID: "area_gate", X: -90, Y: -60, Z: 6, Heading: 90, FovH: 60, FovV: 34, RangeM: 120, Status: online, StreamURL: &stream, UpdatedAt: now},
		{ID: "cam_002", Code: "CAM-002", Name: "1号楼北侧", AreaID: "area_building_a", X: -40, Y: 75, Z: 8, Heading: 180, FovH: 55, FovV: 32, RangeM: 100, Status: online, StreamURL: &stream, UpdatedAt: now},
		{ID: "cam_003", Code: "CAM-003", Name: "1号楼西侧", AreaID: "area_building_a", X: -80, Y: 20, Z: 8, Heading: 90, FovH: 55, FovV: 32, RangeM: 100, Status: online, StreamURL: &stream, UpdatedAt: now},
		{ID: "cam_004", Code: "CAM-004", Name: "2号楼东侧", AreaID: "area_building_b", X: 95, Y: -10, Z: 7, Heading: 270, FovH: 55, FovV: 32, RangeM: 90, Status: "degraded", StreamURL: &stream, UpdatedAt: now},
		{ID: "cam_005", Code: "CAM-005", Name: "停车场", AreaID: "area_parking", X: 40, Y: -80, Z: 6, Heading: 0, FovH: 70, FovV: 40, RangeM: 130, Status: online, StreamURL: &stream, UpdatedAt: now},
		{ID: "cam_006", Code: "CAM-006", Name: "主干道", AreaID: "area_gate", X: 0, Y: 0, Z: 9, Heading: 225, FovH: 65, FovV: 36, RangeM: 150, Status: online, StreamURL: &stream, UpdatedAt: now},
	}
}
