// Package model holds the domain types shared by the store, rules and API
// layers. Field names are the JSON contract in docs/API.md and docs/API_P1.md.
package model

import (
	"errors"
	"time"
)

// Shared repository sentinels.
var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict")
)

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
	ID            string         `json:"id"`
	CameraID      string         `json:"camera_id"`
	CameraCode    string         `json:"camera_code"`
	CameraName    string         `json:"camera_name"`
	Type          string         `json:"type"`
	Severity      string         `json:"severity"`
	Confidence    float64        `json:"confidence"`
	X             float64        `json:"x"`
	Y             float64        `json:"y"`
	Z             float64        `json:"z"`
	OccurredAt    time.Time      `json:"occurred_at"`
	Status        string         `json:"status"`
	ClosedAt      *time.Time     `json:"closed_at,omitempty"`
	EscalatedAt   *time.Time     `json:"escalated_at,omitempty"`
	ReEscalatedAt *time.Time     `json:"re_escalated_at,omitempty"`
	Payload       map[string]any `json:"payload"`
}

// NotificationSettings controls webhook delivery of events and escalations.
type NotificationSettings struct {
	Enabled        bool   `json:"enabled"`
	WebhookURL     string `json:"webhook_url"`
	Format         string `json:"format"`
	MinSeverity    string `json:"min_severity"`
	TimeoutSeconds int    `json:"timeout_seconds"`
	MaxAttempts    int    `json:"max_attempts"`
	BackoffSeconds int    `json:"backoff_seconds"`
	SilenceSeconds int    `json:"silence_seconds"`
}

// DefaultNotificationSettings is the disabled-by-default webhook configuration.
func DefaultNotificationSettings() NotificationSettings {
	return NotificationSettings{
		Enabled: false, WebhookURL: "", Format: "generic", MinSeverity: "warn",
		TimeoutSeconds: 5, MaxAttempts: 3, BackoffSeconds: 2, SilenceSeconds: 30,
	}
}

// EscalationSettings controls the stale-alert escalation worker.
type EscalationSettings struct {
	Enabled            bool     `json:"enabled"`
	AfterSeconds       int      `json:"after_seconds"`
	Severities         []string `json:"severities"`
	AutoDispatch       bool     `json:"auto_dispatch"`
	SecondAfterSeconds int      `json:"second_after_seconds"`
	AutoPatrol         bool     `json:"auto_patrol"`
}

// DefaultEscalationSettings escalates warn/critical alerts after five minutes.
func DefaultEscalationSettings() EscalationSettings {
	return EscalationSettings{Enabled: true, AfterSeconds: 300, Severities: []string{"warn", "critical"}, AutoDispatch: true}
}

// NotificationLog is one webhook delivery attempt.
type NotificationLog struct {
	ID         string    `json:"id"`
	At         time.Time `json:"at"`
	Kind       string    `json:"kind"`
	EventID    string    `json:"event_id"`
	URL        string    `json:"url"`
	Format     string    `json:"format"`
	OK         bool      `json:"ok"`
	StatusCode int       `json:"status_code"`
	Attempts   int       `json:"attempts"`
	Error      string    `json:"error"`
}

// DutyGroup is one on-duty roster bound to a set of areas. An empty area list
// is the global fallback group.
type DutyGroup struct {
	ID        string      `json:"id"`
	Name      string      `json:"name"`
	Areas     []string    `json:"areas"`
	Operators []string    `json:"operators"`
	Rotation  string      `json:"rotation"`
	Shifts    []DutyShift `json:"shifts"`
	Active    bool        `json:"active"`
	CreatedAt time.Time   `json:"created_at"`
	UpdatedAt time.Time   `json:"updated_at"`
}

// DutyShift is one staffing window inside a duty group.
type DutyShift struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Start     string   `json:"start"`
	End       string   `json:"end"`
	Weekdays  []int    `json:"weekdays"`
	Operators []string `json:"operators"`
}

// SeverityRank orders severities for notification thresholds.
func SeverityRank(severity string) int {
	switch severity {
	case "critical":
		return 3
	case "warn":
		return 2
	case "info":
		return 1
	}
	return 0
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
	CameraID  string
	Since     *time.Time
	Limit     int
	CameraIDs []string
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
	Limit     int
	Status    string
	Type      string
	Since     *time.Time
	CameraIDs []string
}

// AuditFilter narrows GET /api/audit.
type AuditFilter struct {
	Limit  int
	Action string
	Actor  string
	From   *time.Time
	To     *time.Time
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

// CameraRules is the per-camera detection rule configuration (P2.0).
type CameraRules struct {
	CameraID            string   `json:"camera_id"`
	ConfidenceThreshold float64  `json:"confidence_threshold"`
	DedupeSeconds       int      `json:"dedupe_seconds"`
	EnabledClasses      []string `json:"enabled_classes"`
	// ROI is a normalized image polygon (0..1). Nil means the full frame.
	ROI       [][2]float64 `json:"roi"`
	UpdatedAt *time.Time   `json:"updated_at,omitempty"`
}

// DefaultCameraRules returns the P0 defaults for a camera without stored rules.
func DefaultCameraRules(cameraID string) CameraRules {
	return CameraRules{
		CameraID:            cameraID,
		ConfidenceThreshold: 0.6,
		DedupeSeconds:       5,
		EnabledClasses:      []string{"person", "vehicle", "intrusion", "smoke", "no_helmet"},
		ROI:                 nil,
	}
}

// Order is one work order created from an event (P2.0).
type Order struct {
	ID        string     `json:"id"`
	EventID   string     `json:"event_id"`
	CameraID  string     `json:"camera_id"`
	Title     string     `json:"title"`
	Status    string     `json:"status"`
	Priority  string     `json:"priority"`
	Assignee  string     `json:"assignee"`
	Note      string     `json:"note"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	ClosedAt  *time.Time `json:"closed_at,omitempty"`
}

// OrderFilter narrows GET /api/orders.
type OrderFilter struct {
	Status    string
	Assignee  string
	EventID   string
	Limit     int
	CameraIDs []string
}

// OrderUpdate carries the mutable fields of PATCH /api/orders/{id}.
type OrderUpdate struct {
	Status   *string `json:"status,omitempty"`
	Assignee *string `json:"assignee,omitempty"`
	Note     *string `json:"note,omitempty"`
}

// SiteOrigin is the ENU origin in WGS84 (for future GIS integration).
type SiteOrigin struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
	Alt float64 `json:"alt"`
}

// SiteExtent bounds the scene in local ENU metres.
type SiteExtent struct {
	MinX float64 `json:"min_x"`
	MaxX float64 `json:"max_x"`
	MinY float64 `json:"min_y"`
	MaxY float64 `json:"max_y"`
	MinZ float64 `json:"min_z"`
	MaxZ float64 `json:"max_z"`
}

// SiteModel is one optional 3D asset the client may load.
type SiteModel struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Format    string         `json:"format"`
	URL       string         `json:"url"`
	Transform SiteModelXform `json:"transform"`
}

// SiteModelXform places a model in the scene (three.js axes after ENU mapping).
type SiteModelXform struct {
	X         float64 `json:"x"`
	Y         float64 `json:"y"`
	Z         float64 `json:"z"`
	RotationY float64 `json:"rotation_y"`
	Scale     float64 `json:"scale"`
}

// SiteMap describes the 2.5D base map style.
type SiteMap struct {
	Style    string  `json:"style"`
	ImageURL *string `json:"image_url"`
}

// Site is the P6.0 site/scene configuration served to the client.
type Site struct {
	Name   string      `json:"name"`
	Origin SiteOrigin  `json:"origin"`
	Extent SiteExtent  `json:"extent"`
	Models []SiteModel `json:"models"`
	Map    SiteMap     `json:"map"`
}

// DefaultSite returns the built-in demo site matching the seeded cameras.
func DefaultSite() Site {
	return Site{
		Name:   "示范园区",
		Origin: SiteOrigin{Lat: 30.2741, Lng: 120.1551, Alt: 0},
		Extent: SiteExtent{MinX: -200, MaxX: 200, MinY: -200, MaxY: 200, MinZ: 0, MaxZ: 60},
		Models: []SiteModel{{
			ID: "site-overview", Name: "园区总览模型", Format: "gltf", URL: "/models/site.gltf",
			Transform: SiteModelXform{X: 0, Y: 0, Z: 0, RotationY: 0, Scale: 1},
		}},
		Map: SiteMap{Style: "vector", ImageURL: nil},
	}
}

// UserAccount is one login account. PasswordHash never leaves the server.
type UserAccount struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	DisplayName  string    `json:"display_name"`
	Role         string    `json:"role"`
	Areas        []string  `json:"areas"`
	Active       bool      `json:"active"`
	TokenVersion int       `json:"-"`
	PasswordHash string    `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// RefreshToken is one opaque refresh-token record (only its SHA-256 is stored).
type RefreshToken struct {
	ID        string    `json:"id"`
	UserID    string    `json:"-"`
	TokenHash string    `json:"-"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
	Revoked   bool      `json:"-"`
	IP        string    `json:"ip"`
	UserAgent string    `json:"user_agent"`
}

// Waypoint is one patrol tour stop in local ENU metres.
type Waypoint struct {
	X       float64 `json:"x"`
	Y       float64 `json:"y"`
	Z       float64 `json:"z"`
	Heading float64 `json:"heading"`
}

// PatrolRoute is a saved camera tour over the 3D scene.
type PatrolRoute struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	Waypoints       []Waypoint `json:"waypoints"`
	CameraIDs       []string   `json:"camera_ids"`
	IntervalSeconds int        `json:"interval_seconds"`
	CreatedBy       string     `json:"created_by"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// PatrolSchedule schedules a patrol route to run periodically.
type PatrolSchedule struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	RouteID         string    `json:"route_id"`
	IntervalMinutes int       `json:"interval_minutes"`
	Enabled         bool      `json:"enabled"`
	CreatedBy       string    `json:"created_by"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	NextRunAt       time.Time `json:"next_run_at"`
}

// PatrolExecution is one due/manual patrol run with its workflow state.
type PatrolExecution struct {
	ID          string     `json:"id"`
	ScheduleID  string     `json:"schedule_id"`
	RouteID     string     `json:"route_id"`
	RouteName   string     `json:"route_name"`
	EventID     string     `json:"event_id"`
	Status      string     `json:"status"`
	Assignee    string     `json:"assignee"`
	Result      string     `json:"result"`
	Note        string     `json:"note"`
	OrderID     string     `json:"order_id"`
	ScheduledAt time.Time  `json:"scheduled_at"`
	CreatedAt   time.Time  `json:"created_at"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
}

// PatrolExecutionFilter narrows execution listings.
type PatrolExecutionFilter struct {
	Status     string
	ScheduleID string
	EventID    string
	From       *time.Time
	To         *time.Time
	Limit      int
}
