package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"digital-twin-platform/api/internal/auth"
	"digital-twin-platform/api/internal/model"
	"digital-twin-platform/api/internal/realtime"
	"digital-twin-platform/api/internal/store"
)

// exportJob is one asynchronous tenant export (P32.0).
type exportJob struct {
	ID            string         `json:"id"`
	TenantID      string         `json:"tenant_id"`
	Status        string         `json:"status"`
	IncludeTracks bool           `json:"include_tracks"`
	IncludeAudit  bool           `json:"include_audit"`
	CreatedBy     string         `json:"created_by"`
	CreatedAt     time.Time      `json:"created_at"`
	StartedAt     *time.Time     `json:"started_at,omitempty"`
	FinishedAt    *time.Time     `json:"finished_at,omitempty"`
	SizeBytes     int64          `json:"size_bytes"`
	Counts        map[string]int `json:"counts"`
	Error         string         `json:"error"`
}

const maxActiveExportJobs = 2

type exportStore struct {
	mu      sync.Mutex
	jobs    map[string]exportJob
	dir     string
	workers int
	slots   chan struct{}
}

func newExportStore(dir string, workers int) *exportStore {
	if workers < 1 {
		workers = 1
	}
	return &exportStore{
		jobs: make(map[string]exportJob), dir: dir, workers: workers,
		slots: make(chan struct{}, workers),
	}
}

// SetExportStore configures the asynchronous export directory and workers.
func (s *Server) SetExportStore(dir string, workers int) {
	if strings.TrimSpace(dir) == "" {
		dir = "data/exports"
	}
	s.exports = newExportStore(dir, workers)
}

func (s *Server) exportState() *exportStore {
	if s.exports == nil {
		s.exports = newExportStore("data/exports", 2)
	}
	return s.exports
}

// activeExports counts jobs still pending or running for a tenant.
func (e *exportStore) activeExports(tenantID string) int {
	count := 0
	for _, job := range e.jobs {
		if job.TenantID == tenantID && (job.Status == "pending" || job.Status == "running") {
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
	state := s.exportState()
	now := time.Now().UTC()
	job := exportJob{
		ID: newID("exp"), TenantID: tenant.ID, Status: "pending",
		IncludeTracks: body.IncludeTracks, IncludeAudit: body.IncludeAudit,
		CreatedBy: user.Username, CreatedAt: now, Counts: map[string]int{},
	}
	state.mu.Lock()
	if state.activeExports(tenant.ID) >= maxActiveExportJobs {
		state.mu.Unlock()
		writeError(w, http.StatusTooManyRequests, "too_many_exports", "two exports are already running for this tenant")
		return
	}
	state.jobs[job.ID] = job
	state.mu.Unlock()

	s.audit(r, "tenant.export.start", job.ID, map[string]any{
		"tenant_id": tenant.ID, "include_tracks": job.IncludeTracks, "include_audit": job.IncludeAudit,
	})
	go s.runExportJob(r.Context(), job.ID, tenant)
	writeJSON(w, http.StatusAccepted, job)
}

// runExportJob renders the tenant package to disk in the background.
func (s *Server) runExportJob(ctx context.Context, jobID string, tenant model.Tenant) {
	state := s.exportState()
	state.slots <- struct{}{}
	defer func() { <-state.slots }()

	started := time.Now().UTC()
	state.mu.Lock()
	job, ok := state.jobs[jobID]
	if !ok {
		state.mu.Unlock()
		return
	}
	job.Status = "running"
	job.StartedAt = &started
	state.jobs[jobID] = job
	state.mu.Unlock()
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
	state.mu.Lock()
	job = state.jobs[jobID]
	job.FinishedAt = &finished
	if err != nil {
		job.Status = "failed"
		job.Error = err.Error()
	} else {
		job.Status = "done"
		job.SizeBytes = size
		job.Counts = payload.Counts
	}
	state.jobs[jobID] = job
	state.mu.Unlock()
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
	state := s.exportState()
	state.mu.Lock()
	items := make([]exportJob, 0, len(state.jobs))
	for _, job := range state.jobs {
		if job.TenantID == tenant.ID {
			items = append(items, job)
		}
	}
	state.mu.Unlock()
	sortExportJobs(items)
	if len(items) > 50 {
		items = items[:50]
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) exportJob(w http.ResponseWriter, r *http.Request) (exportJob, bool) {
	tenant, ok := s.loadTenant(w, r)
	if !ok {
		return exportJob{}, false
	}
	state := s.exportState()
	state.mu.Lock()
	job, found := state.jobs[r.PathValue("exportId")]
	state.mu.Unlock()
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
	if job.Status != "done" {
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
	state.mu.Lock()
	delete(state.jobs, job.ID)
	state.mu.Unlock()
	s.audit(r, "tenant.export.delete", job.ID, map[string]any{"tenant_id": job.TenantID})
	w.WriteHeader(http.StatusNoContent)
}

func sortExportJobs(items []exportJob) {
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && items[j].CreatedAt.After(items[j-1].CreatedAt); j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
}
