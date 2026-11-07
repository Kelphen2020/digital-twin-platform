// Package model holds the P0 domain types shared by the store, rules and API
// layers. Field names are the JSON contract in docs/API.md.
package model

import "time"

// Camera is one fixed camera on the site. Coordinates are local ENU metres:
// X east, Y north, Z up. Heading is degrees clockwise from north.
type Camera struct {
	ID        string    `json:"id"`
	Code      string    `json:"code"`
	Name      string    `json:"name"`
	AreaID    string    `json:"area_id"`
	X         float64   `json:"x"`
	Y         float64   `json:"y"`
	Z         float64   `json:"z"`
	Heading   float64   `json:"heading"`
	FovH      float64   `json:"fov_h"`
	FovV      float64   `json:"fov_v"`
	RangeM    float64   `json:"range_m"`
	Status    string    `json:"status"`
	StreamURL *string   `json:"stream_url"`
	UpdatedAt time.Time `json:"updated_at"`
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
	ID      string      `json:"id"`
	Name    string      `json:"name"`
	Type    string      `json:"type"`
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
	Payload    map[string]any `json:"payload"`
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

// Counts is the cheap aggregate the stats frame and the sidebar need.
type Counts struct {
	EventsTotal   int64 `json:"events_total"`
	OnlineCameras int64 `json:"online_cameras"`
}
