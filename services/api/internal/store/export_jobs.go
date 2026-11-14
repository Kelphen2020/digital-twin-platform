package store

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"digital-twin-platform/api/internal/model"
)

// ---- in-memory export jobs (P33.0) ----

// CreateExportJob stores one job; ids are unique per store.
func (m *Memory) CreateExportJob(_ context.Context, job model.ExportJob) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.exportJobs[job.ID]; exists {
		return model.ErrConflict
	}
	m.exportJobs[job.ID] = job
	return nil
}

// UpdateExportJob overwrites an existing job.
func (m *Memory) UpdateExportJob(_ context.Context, job model.ExportJob) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.exportJobs[job.ID]; !exists {
		return model.ErrNotFound
	}
	m.exportJobs[job.ID] = job
	return nil
}

// GetExportJob returns one job by id.
func (m *Memory) GetExportJob(_ context.Context, id string) (model.ExportJob, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	job, ok := m.exportJobs[id]
	return job, ok, nil
}

// ListExportJobs returns a tenant's jobs newest first.
func (m *Memory) ListExportJobs(_ context.Context, tenantID string, limit int) ([]model.ExportJob, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	items := make([]model.ExportJob, 0, len(m.exportJobs))
	for _, job := range m.exportJobs {
		if job.TenantID == tenantID {
			items = append(items, job)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

// ClaimExportJob marks a pending job as running for owner (in-process: always wins).
func (m *Memory) ClaimExportJob(_ context.Context, id, owner string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.exportJobs[id]
	if !ok {
		return false, model.ErrNotFound
	}
	if job.Status != model.ExportPending {
		return false, nil
	}
	now := time.Now().UTC()
	job.Status = model.ExportRunning
	job.StartedAt = &now
	job.Owner = owner
	m.exportJobs[id] = job
	return true, nil
}

// ReclaimStaleExportJobs resets running jobs whose owner stopped heartbeating
// (in-process store: never needed, returns 0).
func (m *Memory) ReclaimStaleExportJobs(_ context.Context, _ time.Duration) (int, error) {
	return 0, nil
}

// DeleteExportJob removes one job.
func (m *Memory) DeleteExportJob(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.exportJobs[id]; !exists {
		return model.ErrNotFound
	}
	delete(m.exportJobs, id)
	return nil
}

// PurgeExportJobs removes finished jobs older than before (or beyond keep) and
// returns the removed ids.
func (m *Memory) PurgeExportJobs(_ context.Context, tenantID string, before time.Time, keep int) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	items := make([]model.ExportJob, 0, len(m.exportJobs))
	for _, job := range m.exportJobs {
		if job.TenantID == tenantID {
			items = append(items, job)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
	removed := make([]string, 0, 4)
	for index, job := range items {
		if job.Status != model.ExportDone && job.Status != model.ExportFailed {
			continue
		}
		expired := !job.CreatedAt.After(before)
		overflow := keep > 0 && index >= keep
		if expired || overflow {
			delete(m.exportJobs, job.ID)
			removed = append(removed, job.ID)
		}
	}
	return removed, nil
}

// FailPendingExportJobs marks interrupted jobs as failed on startup.
func (m *Memory) FailPendingExportJobs(_ context.Context, reason string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for id, job := range m.exportJobs {
		if job.Status == model.ExportPending || job.Status == model.ExportRunning {
			job.Status = model.ExportFailed
			job.Error = reason
			m.exportJobs[id] = job
			count++
		}
	}
	return count, nil
}

// ---- PostgreSQL export jobs (P33.0) ----

// CreateExportJob inserts one job row.
func (p *Postgres) CreateExportJob(ctx context.Context, job model.ExportJob) error {
	counts, err := marshalCounts(job.Counts)
	if err != nil {
		return err
	}
	_, err = p.pool.Exec(ctx, `
        INSERT INTO export_jobs (id, tenant_id, status, include_tracks, include_audit, created_by,
            created_at, started_at, finished_at, size_bytes, counts, error, owner, heartbeat_at)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
		job.ID, job.TenantID, job.Status, job.IncludeTracks, job.IncludeAudit, job.CreatedBy,
		job.CreatedAt, job.StartedAt, job.FinishedAt, job.SizeBytes, counts, job.Error, job.Owner, job.HeartbeatAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return model.ErrConflict
		}
	}
	return err
}

// UpdateExportJob rewrites a job row.
func (p *Postgres) UpdateExportJob(ctx context.Context, job model.ExportJob) error {
	counts, err := marshalCounts(job.Counts)
	if err != nil {
		return err
	}
	tag, err := p.pool.Exec(ctx, `
        UPDATE export_jobs SET status=$2, started_at=$3, finished_at=$4, size_bytes=$5, counts=$6, error=$7,
            owner=$8, heartbeat_at=$9
        WHERE id=$1`, job.ID, job.Status, job.StartedAt, job.FinishedAt, job.SizeBytes, counts, job.Error,
		job.Owner, job.HeartbeatAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return model.ErrNotFound
	}
	return nil
}

// GetExportJob loads one job row.
func (p *Postgres) GetExportJob(ctx context.Context, id string) (model.ExportJob, bool, error) {
	row := p.pool.QueryRow(ctx, `SELECT `+exportJobColumns+` FROM export_jobs WHERE id=$1`, id)
	job, err := scanExportJob(row)
	if err != nil {
		if err == model.ErrNotFound {
			return model.ExportJob{}, false, nil
		}
		return model.ExportJob{}, false, err
	}
	return job, true, nil
}

// ListExportJobs returns a tenant's rows newest first.
func (p *Postgres) ListExportJobs(ctx context.Context, tenantID string, limit int) ([]model.ExportJob, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := p.pool.Query(ctx, `SELECT `+exportJobColumns+` FROM export_jobs WHERE tenant_id=$1
        ORDER BY created_at DESC LIMIT $2`, tenantID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]model.ExportJob, 0, limit)
	for rows.Next() {
		job, scanErr := scanExportJob(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, job)
	}
	return items, rows.Err()
}

// DeleteExportJob removes one job row.
func (p *Postgres) DeleteExportJob(ctx context.Context, id string) error {
	tag, err := p.pool.Exec(ctx, `DELETE FROM export_jobs WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return model.ErrNotFound
	}
	return nil
}

// PurgeExportJobs removes finished rows older than before or beyond keep.
func (p *Postgres) PurgeExportJobs(ctx context.Context, tenantID string, before time.Time, keep int) ([]string, error) {
	if keep < 0 {
		keep = 0
	}
	rows, err := p.pool.Query(ctx, `
        WITH ranked AS (
            SELECT id, created_at, status,
                   row_number() OVER (ORDER BY created_at DESC) AS position
            FROM export_jobs WHERE tenant_id=$1
        )
        DELETE FROM export_jobs WHERE id IN (
            SELECT id FROM ranked
            WHERE status IN ('done','failed')
              AND (created_at <= $2 OR ($3 > 0 AND position > $3))
        )
        RETURNING id`, tenantID, before, keep)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	removed := make([]string, 0, 4)
	for rows.Next() {
		var id string
		if scanErr := rows.Scan(&id); scanErr != nil {
			return nil, scanErr
		}
		removed = append(removed, id)
	}
	return removed, rows.Err()
}

// ClaimExportJob atomically transitions pending -> running for this replica.
// A conditional UPDATE is the row-level lock: two replicas racing on the same id
// produce exactly one winner (the loser sees zero rows affected).
func (p *Postgres) ClaimExportJob(ctx context.Context, id, owner string) (bool, error) {
	tag, err := p.pool.Exec(ctx, `
        UPDATE export_jobs
           SET status='running', started_at=now(), owner=$2, heartbeat_at=now()
         WHERE id=$1 AND status='pending'`, id, owner)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() > 0 {
		return true, nil
	}
	// Either the job is gone or somebody else owns it.
	var exists bool
	if err := p.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM export_jobs WHERE id=$1)`, id).Scan(&exists); err != nil {
		return false, err
	}
	if !exists {
		return false, model.ErrNotFound
	}
	return false, nil
}

// ReclaimStaleExportJobs returns abandoned running jobs to the pending pool.
func (p *Postgres) ReclaimStaleExportJobs(ctx context.Context, staleAfter time.Duration) (int, error) {
	if staleAfter <= 0 {
		staleAfter = 2 * time.Minute
	}
	tag, err := p.pool.Exec(ctx, `
        UPDATE export_jobs
           SET status='pending', owner='', started_at=NULL, heartbeat_at=NULL
         WHERE status='running'
           AND (heartbeat_at IS NULL OR heartbeat_at < now() - $1::interval)`,
		staleAfter.String())
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// HeartbeatExportJob refreshes the lease for a running job and reports whether
// this replica still owns it (P38.0).
func (p *Postgres) HeartbeatExportJob(ctx context.Context, id, owner string) (bool, error) {
	tag, err := p.pool.Exec(ctx, `
        UPDATE export_jobs SET heartbeat_at=now()
         WHERE id=$1 AND owner=$2 AND status='running'`, id, owner)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// FailPendingExportJobs marks interrupted jobs as failed on startup.
func (p *Postgres) FailPendingExportJobs(ctx context.Context, reason string) (int, error) {
	tag, err := p.pool.Exec(ctx, `
        UPDATE export_jobs SET status='failed', error=$1, finished_at=now()
        WHERE status IN ('pending','running')`, reason)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

const exportJobColumns = `id, tenant_id, status, include_tracks, include_audit, created_by,
    created_at, started_at, finished_at, size_bytes, counts, error, owner, heartbeat_at`

func scanExportJob(row interface{ Scan(dest ...any) error }) (model.ExportJob, error) {
	var job model.ExportJob
	var counts []byte
	err := row.Scan(&job.ID, &job.TenantID, &job.Status, &job.IncludeTracks, &job.IncludeAudit,
		&job.CreatedBy, &job.CreatedAt, &job.StartedAt, &job.FinishedAt, &job.SizeBytes, &counts, &job.Error,
		&job.Owner, &job.HeartbeatAt)
	if err != nil {
		if isNoRows(err) {
			return model.ExportJob{}, model.ErrNotFound
		}
		return model.ExportJob{}, err
	}
	if len(counts) > 0 {
		if unmarshalErr := unmarshalCounts(counts, &job.Counts); unmarshalErr != nil {
			return model.ExportJob{}, unmarshalErr
		}
	}
	if job.Counts == nil {
		job.Counts = map[string]int{}
	}
	return job, nil
}

func marshalCounts(counts map[string]int) ([]byte, error) {
	if counts == nil {
		counts = map[string]int{}
	}
	return json.Marshal(counts)
}

func unmarshalCounts(raw []byte, out *map[string]int) error { return json.Unmarshal(raw, out) }

func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }
