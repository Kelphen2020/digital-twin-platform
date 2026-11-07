// Package model holds the domain types shared by the store, rules and API
// layers. Field names are the JSON contract in docs/API.md and docs/API_P1.md.
package model

import "time"

// Calibration is the pixel → world homography of one camera.
type Calibration struct {
	// Matrix maps homogeneous pixel coordinates to world ENU metres.
	Matrix      [3][3]float64 `json:"matrix"`
	RMSE        float64       `json:"rmse"`
	MaxResidual float64       `json:"max_residual"`
	PointsCount int           `json:"points_count"`
	SolvedAt    *time.Time    `json:"solved_at,omitempty"`
}

// Camera is one fixed camera on the site. Coordinates are local ENU metres:
// X east, Y north, Z up. Heading is degrees clockwise from north.
type Camera struct {
	ID          string       `json:"id"`
	Code        string       `json:"code"`
	Name        string       `json:"name"`
	AreaID      string       `json:"area_id"`
	X           float64      `json:"x"`
	Y           float64      `json:"y"`
	Z           float64      `json:"z"`
	Heading     float64      `json:"heading"`
	FovH        float64      `json:"fov_h"`
	FovV        float64      `json:"fov_v"`
	RangeM      float64      `json:"range_m"`
	Status      string       `json:"status"`
	StreamURL   *string      `json:"stream_url"`
	Calibration *Calibration `json:"calibration"`
	UpdatedAt   time.Time    `json:"updated_at"`
}

// CameraPatch carries the mutable camera fields of PATCH /api/cameras/{id}.
type CameraPatch struct {
	Status  *string  `json:"status,omitempty"`
	X       *float64 `json:"x,omitempty"`
	Y       *float64 `json:"y,omitempty"`
	Z       *float64 `json:"z,omitempty"`
	Heading *float64 `json:"heading,omitempty"`
}

// Area is a site zone. The polygon is a closed ring of ENU points.
type Area struct {
	ID      string       `json:"id"`
	Name    string       `json:"name"`
	Type    string       `json:"type"`
	Polygon [][2]float64 `json:"polygon"`
}

// Event is one produced event (P0 stand-in for the Kafka event envelope).
type Event struct {
	ID         string         `json:"id"`
	CameraID   string         `json:"camera_id"`
	CameraCode string         `json:"camera_code"`
	CameraName string         `json:"camera_name"`
	Type       string         `json:"type"`
	Severity   string         `json:"severity"`
	Confidence float64        `json:"confidence"`
	X          float64        `json:"x"`
	Y          float64        `json:"y"`
	Z          float64        `json:"z"`
	OccurredAt time.Time      `json:"occurred_at"`
	Status     string         `json:"status"`
	ClosedAt   *time.Time     `json:"closed_at,omitempty"`
	Payload    map[string]any `json:"payload"`
}

// EventAction is one workflow action recorded against an event.
type EventAction struct {
	ID      string    `json:"id"`
	EventID string    `json:"event_id"`
	Action  string    `json:"action"`
	Actor   string    `json:"actor"`
	Target  string    `json:"target"`
	Note    string    `json:"note"`
	At      time.Time `json:"at"`
}

// AuditEntry is one recorded mutation.
type AuditEntry struct {
	ID       string         `json:"id"`
	At       time.Time      `json:"at"`
	Actor    string         `json:"actor"`
	Action   string         `json:"action"`
	Resource string         `json:"resource"`
	Detail   map[string]any `json:"detail"`
}

// TrackPoint is one sampled position of a tracked target.
type TrackPoint struct {
	TrackID  string    `json:"track_id"`
	CameraID string    `json:"camera_id"`
	Class    string    `json:"class"`
	X        float64   `json:"x"`
	Y        float64   `json:"y"`
	Z        float64   `json:"z"`
	TS       time.Time `json:"ts"`
}

// TrackFilter narrows GET /api/tracks.
type TrackFilter struct {
	CameraID string
	Since    *time.Time
	Limit    int
}

// World is an optional pre-computed ground position for a detection.
type World struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	Z float64 `json:"z"`
}

// Detection is one model output posted to /api/ingest/detections.
type Detection struct {
	Class      string     `json:"class"`
	Confidence float64    `json:"confidence"`
	BBox       [4]float64 `json:"bbox"`
	TrackID    string     `json:"track_id,omitempty"`
	World      *World     `json:"world,omitempty"`
}

// IngestRequest is the body of POST /api/ingest/detections.
type IngestRequest struct {
	CameraCode string      `json:"camera_code"`
	TS         *time.Time  `json:"ts,omitempty"`
	Detections []Detection `json:"detections"`
}

// EventFilter narrows GET /api/events.
type EventFilter struct {
	Limit  int
	Status string
	Type   string
	Since  *time.Time
}

// AuditFilter narrows GET /api/audit.
type AuditFilter struct {
	Limit  int
	Action string
	Actor  string
}

// Counts is the cheap aggregate the stats frame and the sidebar need.
type Counts struct {
	EventsTotal   int64 `json:"events_total"`
	OnlineCameras int64 `json:"online_cameras"`
}

// StatsTotals is the aggregate block of GET /api/stats/summary.
type StatsTotals struct {
	Events     int64            `json:"events"`
	BySeverity map[string]int64 `json:"by_severity"`
	ByType     map[string]int64 `json:"by_type"`
}

// CameraStat is one row of the per-camera breakdown.
type CameraStat struct {
	CameraID string `json:"camera_id"`
	Code     string `json:"code"`
	Name     string `json:"name"`
	Count    int64  `json:"count"`
}

// HourStat is one row of the hourly histogram.
type HourStat struct {
	Hour  time.Time `json:"hour"`
	Count int64     `json:"count"`
}

// StatsSummary is the body of GET /api/stats/summary.
type StatsSummary struct {
	From     time.Time    `json:"from"`
	To       time.Time    `json:"to"`
	Totals   StatsTotals  `json:"totals"`
	ByCamera []CameraStat `json:"by_camera"`
	ByHour   []HourStat   `json:"by_hour"`
}

// SolveRequest is the body of POST /api/calibration/solve.
type SolveRequest struct {
	CameraID string       `json:"camera_id"`
	Points   []SolvePoint `json:"points"`
}

// SolvePoint is one pixel↔world control point.
type SolvePoint struct {
	PX float64 `json:"px"`
	PY float64 `json:"py"`
	X  float64 `json:"x"`
	Y  float64 `json:"y"`
}

// SolveResult is the body of POST /api/calibration/solve.
type SolveResult struct {
	Matrix      [3][3]float64 `json:"matrix"`
	RMSE        float64       `json:"rmse"`
	MaxResidual float64       `json:"max_residual"`
	PointsCount int           `json:"points_count"`
}
