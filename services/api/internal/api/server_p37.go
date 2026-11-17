package api

import (
	"net/http"
	"time"

	"digital-twin-platform/api/internal/auth"
	"digital-twin-platform/api/internal/model"
	"digital-twin-platform/api/internal/store"
)

// tenantOverview is the in-app operational summary panel (P37.0). It mirrors the
// subset of Prometheus series that operators watch, without exposing /metrics.
type tenantOverview struct {
	Tenant       string           `json:"tenant"`
	GeneratedAt  time.Time        `json:"generated_at"`
	Events       modelStatsTotals `json:"events"`
	Cameras      cameraOverview   `json:"cameras"`
	OpenOrders   int64            `json:"open_orders"`
	Patrol       patrolOverview   `json:"patrol"`
	Exports      exportOverview   `json:"exports"`
	Ingest       ingestOverview   `json:"ingest"`
	Health       healthOverview   `json:"health"`
	RecentHourly []model.HourStat `json:"recent_hourly"`
}

type modelStatsTotals struct {
	Total      int64            `json:"total"`
	Last24h    int64            `json:"last_24h"`
	BySeverity map[string]int64 `json:"by_severity"`
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
