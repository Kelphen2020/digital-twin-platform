// Package rules is the P0 rule engine: it turns raw detections into events.
// The production design replaces this with a configurable rule service fed by
// Kafka; the input/output contract stays the same.
package rules

import (
	"crypto/rand"
	"encoding/hex"
	"math"
	"sync"
	"time"

	"digital-twin-platform/api/internal/model"
)

// FallbackDistanceM is where a detection without world coordinates and without
// camera calibration is placed along the camera's optical axis.
const FallbackDistanceM = 30.0

// P1 assumes a 1920x1080 camera image so normalized bboxes can be turned into
// the pixel coordinates a calibration matrix expects.
const (
	PixelWidth  = 1920.0
	PixelHeight = 1080.0
)

// Result is one evaluation outcome.
type Result struct {
	Events  []model.Event
	Dropped int
}

type classMapping struct {
	eventType string
	severity  string
}

var classMap = map[string]classMapping{
	"person":    {eventType: "person", severity: "info"},
	"vehicle":   {eventType: "vehicle", severity: "info"},
	"intrusion": {eventType: "intrusion", severity: "warn"},
	"smoke":     {eventType: "smoke", severity: "critical"},
	"no_helmet": {eventType: "no_helmet", severity: "warn"},
}

// Engine applies the P0 rules from docs/API.md §0:
// drop confidence < 0.6, dedupe the same camera+class inside the window,
// map class to type/severity, and place the event in world coordinates.
type Engine struct {
	mu     sync.Mutex
	window time.Duration
	last   map[string]time.Time
}

// New creates an engine with the given dedupe window.
func New(window time.Duration) *Engine {
	if window <= 0 {
		window = 5 * time.Second
	}
	return &Engine{window: window, last: make(map[string]time.Time)}
}

// Evaluate processes one ingest batch.
func (e *Engine) Evaluate(camera model.Camera, batch []model.Detection, occurredAt time.Time) Result {
	e.mu.Lock()
	defer e.mu.Unlock()

	result := Result{}
	for _, detection := range batch {
		if detection.Confidence < 0.6 {
			result.Dropped++
			continue
		}
		mapping, known := classMap[detection.Class]
		if !known {
			mapping = classMapping{eventType: "other", severity: "info"}
		}
		key := camera.ID + "|" + detection.Class
		if previous, seen := e.last[key]; seen && occurredAt.Sub(previous) < e.window {
			result.Dropped++
			continue
		}
		e.last[key] = occurredAt
		x, y, z, source := ProjectToWorld(camera, detection)
		result.Events = append(result.Events, model.Event{
			ID:         newEventID(),
			CameraID:   camera.ID,
			CameraCode: camera.Code,
			CameraName: camera.Name,
			Type:       mapping.eventType,
			Severity:   mapping.severity,
			Confidence: detection.Confidence,
			X:          x,
			Y:          y,
			Z:          z,
			OccurredAt: occurredAt.UTC(),
			Status:     "new",
			Payload: map[string]any{
				"class":        detection.Class,
				"bbox":         detection.BBox,
				"confidence":   detection.Confidence,
				"world_source": source,
			},
		})
	}
	e.prune(occurredAt)
	return result
}

// prune bounds the dedupe map so a long-running process cannot leak keys.
func (e *Engine) prune(now time.Time) {
	if len(e.last) < 4096 {
		return
	}
	for key, seen := range e.last {
		if now.Sub(seen) > 10*e.window {
			delete(e.last, key)
		}
	}
}

// ProjectToWorld resolves a detection's world position. Priority follows
// docs/API_P1.md: explicit world coordinates, then the camera calibration
// matrix, then the P0 fallback along the optical axis. The returned source is
// "world", "calibration", "fallback" or "none".
func ProjectToWorld(camera model.Camera, detection model.Detection) (x, y, z float64, source string) {
	if detection.World != nil {
		return detection.World.X, detection.World.Y, detection.World.Z, "world"
	}
	if camera.Calibration != nil {
		px := (detection.BBox[0] + detection.BBox[2]) / 2 * PixelWidth
		py := detection.BBox[3] * PixelHeight
		if wx, wy, ok := project(camera.Calibration.Matrix, px, py); ok {
			return wx, wy, 0, "calibration"
		}
	}
	return camera.X + math.Sin(radians(camera.Heading))*FallbackDistanceM,
		camera.Y + math.Cos(radians(camera.Heading))*FallbackDistanceM, 0, "fallback"
}

func project(matrix [3][3]float64, px, py float64) (float64, float64, bool) {
	w := matrix[2][0]*px + matrix[2][1]*py + matrix[2][2]
	if math.Abs(w) < 1e-9 {
		return 0, 0, false
	}
	x := (matrix[0][0]*px + matrix[0][1]*py + matrix[0][2]) / w
	y := (matrix[1][0]*px + matrix[1][1]*py + matrix[1][2]) / w
	if math.IsNaN(x) || math.IsNaN(y) || math.IsInf(x, 0) || math.IsInf(y, 0) {
		return 0, 0, false
	}
	return x, y, true
}

func radians(degrees float64) float64 { return degrees * math.Pi / 180 }

func newEventID() string {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "evt_task"
	}
	return "evt_" + hex.EncodeToString(raw[:])
}
