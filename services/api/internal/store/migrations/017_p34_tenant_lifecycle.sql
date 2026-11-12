-- P34: persist tenant archive state and quotas so PostgreSQL matches the API model.
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS archived     boolean NOT NULL DEFAULT false;
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS archived_at  timestamptz;
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS max_cameras  integer NOT NULL DEFAULT 0;
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS max_users    integer NOT NULL DEFAULT 0;
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS max_events   integer NOT NULL DEFAULT 0;
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS ingest_qps   integer NOT NULL DEFAULT 0;
UPDATE tenants SET archived = NOT active WHERE archived = false AND active = false;
