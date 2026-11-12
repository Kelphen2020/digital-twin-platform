-- P33: persist asynchronous tenant export jobs.
CREATE TABLE IF NOT EXISTS export_jobs (
    id             text PRIMARY KEY,
    tenant_id      text NOT NULL,
    status         text NOT NULL,
    include_tracks boolean NOT NULL DEFAULT false,
    include_audit  boolean NOT NULL DEFAULT false,
    created_by     text NOT NULL DEFAULT '',
    created_at     timestamptz NOT NULL,
    started_at     timestamptz,
    finished_at    timestamptz,
    size_bytes     bigint NOT NULL DEFAULT 0,
    counts         jsonb NOT NULL DEFAULT '{}'::jsonb,
    error          text NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS export_jobs_tenant_created_idx ON export_jobs (tenant_id, created_at DESC);
