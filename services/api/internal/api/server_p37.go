package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"digital-twin-platform/api/internal/auth"
	"digital-twin-platform/api/internal/model"
	"digital-twin-platform/api/internal/store"
)

// tenantOverview is the in-app operational summary panel (P37.0). It mirrors the
// subset of Prometheus series that operators watch, without exposing /metrics.
type tenantOverview struct {
	Tenant      string           `json:"tenant"`
	GeneratedAt time.Time        `json:"generated_at"`
	Events      modelStatsTotals `json:"events"`
	Cameras     cameraOverview   `json:"cameras"`
	OpenOrders  int64            `json:"open_orders"`
	Patrol      patrolOverview   `json:"patrol"`
	Exports     exportOverview   `json:"exports"`
	Ingest      ingestOverview   `json:"ingest"`
	Health      healthOverview   `json:"health"`
	Window      overviewWindow   `json:"window"`
	// P41: histogram for the selected window (bucket size adapts to it).
	Series        []seriesPoint    `json:"series"`
	BucketSeconds int              `json:"bucket_seconds"`
	RecentHourly  []model.HourStat `json:"recent_hourly"`
}

// seriesPoint is one bucket of the overview histogram.
type seriesPoint struct {
	Bucket string `json:"bucket"`
	Count  int64  `json:"count"`
}

// bucketSizeFor returns the histogram granularity that keeps the chart readable
// for each window: minutes for 1h, hours for 6h/24h, days for 7d (P41.0).
func bucketSizeFor(d time.Duration) time.Duration {
	switch {
	case d <= time.Hour:
		return 5 * time.Minute
	case d <= 6*time.Hour:
		return 30 * time.Minute
	case d <= 24*time.Hour:
		return time.Hour
	default:
		return 24 * time.Hour
	}
}

type modelStatsTotals struct {
	Total      int64            `json:"total"`
	Last24h    int64            `json:"last_24h"`
	BySeverity map[string]int64 `json:"by_severity"`
}

// overviewWindow describes the comparison window requested by the caller (P40.0):
// "window" is the current bucket, "previous" the equally long period before it.
type overviewWindow struct {
	Duration string `json:"duration"`
	From     string `json:"from"`
	To       string `json:"to"`
	Current  int64  `json:"current"`
	Previous int64  `json:"previous"`
}

// overviewWindows are the windows accepted by ?window=.
var overviewWindows = map[string]time.Duration{
	"1h":  time.Hour,
	"6h":  6 * time.Hour,
	"24h": 24 * time.Hour,
	"7d":  7 * 24 * time.Hour,
}

// parseOverviewWindow resolves ?window= (default 24h).
func parseOverviewWindow(r *http.Request) (string, time.Duration) {
	raw := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("window")))
	if d, ok := overviewWindows[raw]; ok {
		return raw, d
	}
	return "24h", 24 * time.Hour
}

type cameraOverview struct {
	Total   int64 `json:"total"`
	Online  int64 `json:"online"`
	Offline int64 `json:"offline"`
}

type patrolOverview struct {
	Routes     int64 `json:"routes"`
	Schedules  int64 `json:"schedules"`
	Pending    int64 `json:"pending"`
	InProgress int64 `json:"in_progress"`
}

type exportOverview struct {
	Jobs      int64  `json:"jobs"`
	Done      int64  `json:"done"`
	Failed    int64  `json:"failed"`
	Active    int64  `json:"active"`
	Artifacts string `json:"artifacts"`
}

type ingestOverview struct {
	Accepted int64 `json:"accepted"`
	Rejected int64 `json:"rejected"`
}

type healthOverview struct {
	BusDriver string `json:"bus_driver"`
	BusNext   int64  `json:"bus_next_offset"`
	WSClents  int    `json:"ws_clients"`
	StoreMode string `json:"store_mode"`
}

// handleTenantOverview is GET /api/tenants/{id}/overview (P37.0).
func (s *Server) handleTenantOverview(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.allow(w, r, auth.PermTenantAdmin); !ok {
		return
	}
	tenant, ok := s.loadTenant(w, r)
	if !ok {
		return
	}
	ctx := store.WithTenant(r.Context(), tenant.ID)
	now := time.Now().UTC()
	windowName, windowLen := parseOverviewWindow(r)

	overview := tenantOverview{Tenant: tenant.ID, GeneratedAt: now}
	overview.Events.BySeverity = map[string]int64{}

	counts, err := s.store.Counts(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	overview.Events.Total = counts.EventsTotal
	overview.Cameras.Online = counts.OnlineCameras

	cameras, err := s.store.ListCameras(ctx)
	if err == nil {
		overview.Cameras.Total = int64(len(cameras))
		overview.Cameras.Offline = overview.Cameras.Total - overview.Cameras.Online
		if overview.Cameras.Offline < 0 {
			overview.Cameras.Offline = 0
		}
	}

	windowStart := now.Add(-windowLen)
	overview.Window = overviewWindow{
		Duration: windowName,
		From:     windowStart.Format(time.RFC3339),
		To:       now.Format(time.RFC3339),
	}
	if summary, statsErr := s.store.Stats(ctx, windowStart, now, nil); statsErr == nil {
		overview.Window.Current = summary.Totals.Events
	}
	if prev, statsErr := s.store.Stats(ctx, now.Add(-2*windowLen), windowStart, nil); statsErr == nil {
		overview.Window.Previous = prev.Totals.Events
	}

	dayAgo := now.Add(-24 * time.Hour)
	summary, err := s.store.Stats(ctx, dayAgo, now, nil)
	if err == nil {
		overview.Events.Last24h = summary.Totals.Events
		for severity, count := range summary.Totals.BySeverity {
			overview.Events.BySeverity[severity] = count
		}
		overview.RecentHourly = summary.ByHour
		if overview.RecentHourly == nil {
			overview.RecentHourly = []model.HourStat{}
		}
	}

	// P41: build the chart series for the selected window with a bucket size
	// that keeps the number of bars reasonable (12–30 bars for every window).
	bucket := bucketSizeFor(windowLen)
	overview.BucketSeconds = int(bucket.Seconds())
	overview.Series = s.buildOverviewSeries(ctx, windowStart, now, bucket)

	if orders, err := s.store.ListOrders(ctx, model.OrderFilter{Limit: 1000}); err == nil {
		for _, order := range orders {
			if order.Status != "closed" && order.Status != "cancelled" {
				overview.OpenOrders++
			}
		}
	}

	if routes, err := s.store.ListPatrolRoutes(ctx); err == nil {
		overview.Patrol.Routes = int64(len(routes))
	}
	if schedules, err := s.store.ListPatrolSchedules(ctx); err == nil {
		overview.Patrol.Schedules = int64(len(schedules))
	}
	if executions, err := s.store.ListPatrolExecutions(ctx, model.PatrolExecutionFilter{Limit: 500}); err == nil {
		for _, execution := range executions {
			switch execution.Status {
			case "pending":
				overview.Patrol.Pending++
			case "running":
				overview.Patrol.InProgress++
			}
		}
	}

	if jobs, err := s.store.ListExportJobs(ctx, tenant.ID, 200); err == nil {
		overview.Exports.Jobs = int64(len(jobs))
		for _, job := range jobs {
			switch job.Status {
			case model.ExportDone:
				overview.Exports.Done++
			case model.ExportFailed:
				overview.Exports.Failed++
			case model.ExportPending, model.ExportRunning:
				overview.Exports.Active++
			}
		}
	}

	overview.Exports.Artifacts = s.exportState().artifacts.Describe()
	overview.Health.WSClents = s.hub.Clients()
	overview.Health.StoreMode = storeModeName(s.store)
	overview.Health.BusDriver = "none"
	if s.bus != nil {
		status := s.bus.Status()
		if status.Driver != "" {
			overview.Health.BusDriver = status.Driver
		}
		overview.Health.BusNext = status.NextOffset
	}
	overview.Ingest.Accepted = s.metrics.TenantIngestCount(tenant.ID)

	writeJSON(w, http.StatusOK, overview)
}

// storeModeName reports which Store backend is active.
func storeModeName(database store.Store) string {
	if _, ok := database.(*store.Postgres); ok {
		return "postgres"
	}
	return "memory"
}

// buildOverviewSeries buckets the tenant's events into fixed-size windows. The
// Store implementation pushes the aggregation down (PostgreSQL uses date_bin), so
// the API process never holds the window's raw events (P42.0).
func (s *Server) buildOverviewSeries(ctx context.Context, from, to time.Time, bucket time.Duration) []seriesPoint {
	buckets, err := s.store.EventHistogram(ctx, from, to, bucket)
	if err != nil || len(buckets) == 0 {
		return []seriesPoint{}
	}
	points := make([]seriesPoint, 0, len(buckets))
	for _, slot := range buckets {
		points = append(points, seriesPoint{
			Bucket: slot.Hour.UTC().Format(time.RFC3339),
			Count:  slot.Count,
		})
	}
	return points
}
