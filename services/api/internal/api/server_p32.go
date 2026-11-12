package api

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"digital-twin-platform/api/internal/auth"
	"digital-twin-platform/api/internal/model"
	"digital-twin-platform/api/internal/realtime"
	"digital-twin-platform/api/internal/store"
)

// exportJobAlias keeps the P32 field names readable inside this file.
type exportJob = model.ExportJob

const maxActiveExportJobs = 2

// exportStore keeps the on-disk location and worker budget for export jobs;
// job rows themselves live in the Store so they survive restarts (P33.0).
type exportStore struct {
	dir       string
	workers   int
	retention time.Duration
	maxJobs   int
	slots     chan struct{}
	wg        sync.WaitGroup
}

// newExportStore builds the export runtime with defaults applied.
func newExportStore(dir string, workers int) *exportStore {
	return newExportStoreWithPolicy(dir, workers, 0, 0)
}

// newExportStoreWithPolicy applies defaults and stores the retention policy.
func newExportStoreWithPolicy(dir string, workers int, retention time.Duration, maxJobs int) *exportStore {
	if strings.TrimSpace(dir) == "" {
		dir = "data/exports"
	}
	if workers < 1 {
		workers = 1
	}
	return &exportStore{
		dir: dir, workers: workers, retention: retention, maxJobs: maxJobs,
		slots: make(chan struct{}, workers),
	}
}

// SetExportStore configures the asynchronous export directory, workers and policy.
func (s *Server) SetExportStore(dir string, workers int, retention time.Duration, maxJobs int) {
	s.exports = newExportStoreWithPolicy(dir, workers, retention, maxJobs)
}

func (s *Server) exportState() *exportStore {
	if s.exports == nil {
		s.exports = newExportStore("data/exports", 2)
	}
	return s.exports
}

// activeExports counts jobs still pending or running for a tenant.
func (s *Server) activeExports(ctx context.Context, tenantID string) int {
	jobs, err := s.store.ListExportJobs(ctx, tenantID, 200)
	if err != nil {
		return 0
	}
	count := 0
	for _, job := range jobs {
		if job.Status == model.ExportPending || job.Status == model.ExportRunning {
			count++
		}
	}
	return count
}

// handleCreateExportJob is POST /api/tenants/{id}/exports (admin, P32.0).
func (s *Server) handleCreateExportJob(w http.ResponseWriter, r *http.Request) {
	user, ok := s.allow(w, r, auth.PermTenantAdmin)
	if !ok {
		return
	}
	tenant, ok := s.loadTenant(w, r)
	if !ok {
		return
	}
	var body struct {
		IncludeTracks bool `json:"include_tracks"`
		IncludeAudit  bool `json:"include_audit"`
	}
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
			return
		}
	}
	now := time.Now().UTC()
	job := model.ExportJob{
		ID: newID("exp"), TenantID: tenant.ID, Status: model.ExportPending,
		IncludeTracks: body.IncludeTracks, IncludeAudit: body.IncludeAudit,
		CreatedBy: user.Username, CreatedAt: now, Counts: map[string]int{},
	}
	if s.activeExports(r.Context(), tenant.ID) >= maxActiveExportJobs {
		writeError(w, http.StatusTooManyRequests, "too_many_exports", "two exports are already running for this tenant")
		return
	}
	if err := s.store.CreateExportJob(r.Context(), job); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	s.audit(r, "tenant.export.start", job.ID, map[string]any{
		"tenant_id": tenant.ID, "include_tracks": job.IncludeTracks, "include_audit": job.IncludeAudit,
	})
	state := s.exportState()
	jobCtx := r.Context()
	state.wg.Add(1)
	go func() {
		defer state.wg.Done()
		s.runExportJob(jobCtx, job, tenant)
	}()
	writeJSON(w, http.StatusAccepted, job)
}

// runExportJob renders the tenant package to disk in the background.
func (s *Server) runExportJob(ctx context.Context, job model.ExportJob, tenant model.Tenant) {
	state := s.exportState()
	state.slots <- struct{}{}
	defer func() { <-state.slots }()
	jobID := job.ID

	started := time.Now().UTC()
	job.Status = model.ExportRunning
	job.StartedAt = &started
	if err := s.store.UpdateExportJob(ctx, job); err != nil {
		return
	}
	s.broadcastExport(job)

	payload, err := s.buildTenantExport(ctx, tenant, job.IncludeTracks, job.IncludeAudit)
	if err == nil {
		if mkErr := os.MkdirAll(state.dir, 0o755); mkErr != nil {
			err = mkErr
		}
	}
	var size int64
	if err == nil {
		encoded, encodeErr := json.Marshal(payload)
		if encodeErr != nil {
			err = encodeErr
		} else if writeErr := os.WriteFile(filepath.Join(state.dir, jobID+".json"), encoded, 0o644); writeErr != nil {
			err = writeErr
		} else {
			size = int64(len(encoded))
		}
	}

	finished := time.Now().UTC()
	job.FinishedAt = &finished
	if err != nil {
		job.Status = model.ExportFailed
		job.Error = err.Error()
	} else {
		job.Status = model.ExportDone
		job.SizeBytes = size
		job.Counts = payload.Counts
	}
	_ = s.store.UpdateExportJob(ctx, job)
	s.broadcastExport(job)

	action := "tenant.export.done"
	detail := map[string]any{"tenant_id": tenant.ID, "size_bytes": size}
	if err != nil {
		action = "tenant.export.failed"
		detail["error"] = err.Error()
	}
	_ = s.store.AddAudit(store.WithTenant(ctx, tenant.ID), model.AuditEntry{
		ID: newID("aud"), At: finished, Actor: "system", Action: action, Resource: jobID, Detail: detail,
	})
}

func (s *Server) broadcastExport(job exportJob) {
	s.hub.Broadcast(realtime.Frame{Type: "export_status", Data: job})
}

// handleListExportJobs is GET /api/tenants/{id}/exports (admin).
func (s *Server) handleListExportJobs(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.allow(w, r, auth.PermTenantAdmin); !ok {
		return
	}
	tenant, ok := s.loadTenant(w, r)
	if !ok {
		return
	}
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, convErr := strconv.Atoi(raw); convErr == nil && parsed > 0 {
			limit = min(parsed, 200)
		}
	}
	items, err := s.store.ListExportJobs(r.Context(), tenant.ID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	if items == nil {
		items = []model.ExportJob{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) exportJob(w http.ResponseWriter, r *http.Request) (exportJob, bool) {
	tenant, ok := s.loadTenant(w, r)
	if !ok {
		return exportJob{}, false
	}
	job, found, err := s.store.GetExportJob(r.Context(), r.PathValue("exportId"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return model.ExportJob{}, false
	}
	if !found || job.TenantID != tenant.ID {
		writeError(w, http.StatusNotFound, "export_not_found", "export job not found")
		return exportJob{}, false
	}
	return job, true
}

// handleGetExportJob is GET /api/tenants/{id}/exports/{exportId}.
func (s *Server) handleGetExportJob(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.allow(w, r, auth.PermTenantAdmin); !ok {
		return
	}
	job, ok := s.exportJob(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, job)
}

// handleDownloadExportJob is GET /api/tenants/{id}/exports/{exportId}/download.
func (s *Server) handleDownloadExportJob(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.allow(w, r, auth.PermTenantAdmin); !ok {
		return
	}
	job, ok := s.exportJob(w, r)
	if !ok {
		return
	}
	if job.Status != model.ExportDone {
		writeError(w, http.StatusConflict, "invalid_state", "export job is not finished")
		return
	}
	state := s.exportState()
	path := filepath.Join(state.dir, job.ID+".json")
	content, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeError(w, http.StatusNotFound, "export_not_found", "export artifact is missing")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="tenant-`+job.TenantID+`-`+job.ID+`.json"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(content)
}

// handleDeleteExportJob is DELETE /api/tenants/{id}/exports/{exportId}.
func (s *Server) handleDeleteExportJob(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.allow(w, r, auth.PermTenantAdmin); !ok {
		return
	}
	job, ok := s.exportJob(w, r)
	if !ok {
		return
	}
	state := s.exportState()
	_ = os.Remove(filepath.Join(state.dir, job.ID+".json"))
	if err := s.store.DeleteExportJob(r.Context(), job.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	s.audit(r, "tenant.export.delete", job.ID, map[string]any{"tenant_id": job.TenantID})
	w.WriteHeader(http.StatusNoContent)
}

// WaitForExports blocks until every queued export worker has finished. Tests and
// graceful shutdown use it so background writers cannot outlive their data dir.
func (s *Server) WaitForExports() {
	if s.exports != nil {
		s.exports.wg.Wait()
	}
}

// handlePurgeExportJobs is POST /api/tenants/{id}/exports/purge (admin, P33.0).
func (s *Server) handlePurgeExportJobs(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.allow(w, r, auth.PermTenantAdmin); !ok {
		return
	}
	tenant, ok := s.loadTenant(w, r)
	if !ok {
		return
	}
	before := time.Now().Add(-7 * 24 * time.Hour)
	if raw := r.URL.Query().Get("before"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "before must be RFC3339")
			return
		}
		before = parsed
	}
	state := s.exportState()
	removed, err := s.store.PurgeExportJobs(r.Context(), tenant.ID, before, state.maxJobs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	for _, id := range removed {
		_ = os.Remove(filepath.Join(state.dir, id+".json"))
	}
	s.metrics.ExportJobsPurged(int64(len(removed)))
	s.audit(r, "tenant.export.purge", tenant.ID, map[string]any{"purged": len(removed), "before": before})
	writeJSON(w, http.StatusOK, map[string]any{"purged": len(removed)})
}

// StartExportRetention runs the periodic retention sweep until ctx ends.
func (s *Server) StartExportRetention(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 10 * time.Minute
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.SweepExportJobs(ctx)
			}
		}
	}()
}

// SweepExportJobs prunes finished jobs older than the retention window.
func (s *Server) SweepExportJobs(ctx context.Context) {
	state := s.exportState()
	if state.retention <= 0 && state.maxJobs <= 0 {
		return
	}
	tenants, err := s.store.ListTenants(ctx)
	if err != nil {
		log.Printf("api: export retention: list tenants: %v", err)
		return
	}
	before := time.Now().UTC()
	if state.retention > 0 {
		before = before.Add(-state.retention)
	}
	purged := 0
	for _, tenant := range tenants {
		removed, purgeErr := s.store.PurgeExportJobs(ctx, tenant.ID, before, state.maxJobs)
		if purgeErr != nil {
			log.Printf("api: export retention: purge %s: %v", tenant.ID, purgeErr)
			continue
		}
		for _, id := range removed {
			_ = os.Remove(filepath.Join(state.dir, id+".json"))
		}
		purged += len(removed)
	}
	if purged > 0 {
		s.metrics.ExportJobsPurged(int64(purged))
		log.Printf("api: export retention purged %d jobs", purged)
	}
}

// RecoverInterruptedExportJobs marks jobs left running by a previous process.
func (s *Server) RecoverInterruptedExportJobs(ctx context.Context) {
	type failedMover interface {
		FailPendingExportJobs(context.Context, string) (int, error)
	}
	mover, ok := s.store.(failedMover)
	if !ok {
		return
	}
	count, err := mover.FailPendingExportJobs(ctx, "interrupted by restart")
	if err != nil {
		log.Printf("api: export recovery: %v", err)
		return
	}
	if count > 0 {
		log.Printf("api: export recovery marked %d interrupted jobs as failed", count)
	}
}
