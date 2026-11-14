-- P38: cross-replica export job ownership + heartbeat lease.
ALTER TABLE export_jobs ADD COLUMN IF NOT EXISTS owner        text NOT NULL DEFAULT '';
ALTER TABLE export_jobs ADD COLUMN IF NOT EXISTS heartbeat_at timestamptz;
CREATE INDEX IF NOT EXISTS export_jobs_status_idx ON export_jobs (status, created_at);
