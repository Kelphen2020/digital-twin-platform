-- P27.0 multi-tenancy foundation: tenants table, per-tenant columns,
-- per-tenant uniqueness and settings key. Idempotent (migrations re-run).

CREATE TABLE IF NOT EXISTS tenants (
    id         text PRIMARY KEY,
    name       text NOT NULL,
    active     boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

INSERT INTO tenants (id, name, active)
VALUES ('default', '默认园区', true)
ON CONFLICT (id) DO NOTHING;

-- Per-tenant columns on every scoped entity (docs/API_P27.md §3).
ALTER TABLE areas              ADD COLUMN IF NOT EXISTS tenant_id text NOT NULL DEFAULT 'default';
ALTER TABLE cameras            ADD COLUMN IF NOT EXISTS tenant_id text NOT NULL DEFAULT 'default';
ALTER TABLE events             ADD COLUMN IF NOT EXISTS tenant_id text NOT NULL DEFAULT 'default';
ALTER TABLE orders             ADD COLUMN IF NOT EXISTS tenant_id text NOT NULL DEFAULT 'default';
ALTER TABLE camera_rules       ADD COLUMN IF NOT EXISTS tenant_id text NOT NULL DEFAULT 'default';
ALTER TABLE audit_log          ADD COLUMN IF NOT EXISTS tenant_id text NOT NULL DEFAULT 'default';
ALTER TABLE notification_log   ADD COLUMN IF NOT EXISTS tenant_id text NOT NULL DEFAULT 'default';
ALTER TABLE patrol_routes      ADD COLUMN IF NOT EXISTS tenant_id text NOT NULL DEFAULT 'default';
ALTER TABLE patrol_schedules   ADD COLUMN IF NOT EXISTS tenant_id text NOT NULL DEFAULT 'default';
ALTER TABLE patrol_executions  ADD COLUMN IF NOT EXISTS tenant_id text NOT NULL DEFAULT 'default';
ALTER TABLE users              ADD COLUMN IF NOT EXISTS tenant_id text NOT NULL DEFAULT 'default';
ALTER TABLE refresh_tokens     ADD COLUMN IF NOT EXISTS tenant_id text NOT NULL DEFAULT 'default';
ALTER TABLE settings           ADD COLUMN IF NOT EXISTS tenant_id text NOT NULL DEFAULT 'default';

-- Tenant-led indexes for the hot read paths.
CREATE INDEX IF NOT EXISTS areas_tenant_idx ON areas (tenant_id);
CREATE INDEX IF NOT EXISTS cameras_tenant_idx ON cameras (tenant_id);
CREATE INDEX IF NOT EXISTS events_tenant_time_idx ON events (tenant_id, occurred_at DESC);
CREATE INDEX IF NOT EXISTS orders_tenant_created_idx ON orders (tenant_id, created_at DESC);
CREATE INDEX IF NOT EXISTS camera_rules_tenant_idx ON camera_rules (tenant_id);
CREATE INDEX IF NOT EXISTS audit_tenant_at_idx ON audit_log (tenant_id, at DESC);
CREATE INDEX IF NOT EXISTS notification_tenant_at_idx ON notification_log (tenant_id, at DESC);
CREATE INDEX IF NOT EXISTS patrol_routes_tenant_idx ON patrol_routes (tenant_id);
CREATE INDEX IF NOT EXISTS patrol_schedules_tenant_idx ON patrol_schedules (tenant_id);
CREATE INDEX IF NOT EXISTS patrol_executions_tenant_idx ON patrol_executions (tenant_id, created_at DESC);
CREATE INDEX IF NOT EXISTS users_tenant_idx ON users (tenant_id);
CREATE INDEX IF NOT EXISTS refresh_tokens_tenant_idx ON refresh_tokens (tenant_id);
CREATE INDEX IF NOT EXISTS settings_tenant_idx ON settings (tenant_id);

-- P27 uniqueness changes: global uniqueness becomes per-tenant uniqueness.
ALTER TABLE cameras DROP CONSTRAINT IF EXISTS cameras_code_key;
CREATE UNIQUE INDEX IF NOT EXISTS cameras_tenant_code_uq ON cameras (tenant_id, code);

ALTER TABLE users DROP CONSTRAINT IF EXISTS users_username_key;
CREATE UNIQUE INDEX IF NOT EXISTS users_tenant_username_uq ON users (tenant_id, username);

-- settings primary key becomes (tenant_id, key).
ALTER TABLE settings DROP CONSTRAINT IF EXISTS settings_pkey;
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint c
        JOIN pg_class t ON t.oid = c.conrelid
        WHERE t.relname = 'settings' AND c.contype = 'p'
    ) THEN
        ALTER TABLE settings ADD PRIMARY KEY (tenant_id, key);
    END IF;
END $$;

-- camera_rules primary key becomes (tenant_id, camera_id).
ALTER TABLE camera_rules DROP CONSTRAINT IF EXISTS camera_rules_pkey;
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint c
        JOIN pg_class t ON t.oid = c.conrelid
        WHERE t.relname = 'camera_rules' AND c.contype = 'p'
    ) THEN
        ALTER TABLE camera_rules ADD PRIMARY KEY (tenant_id, camera_id);
    END IF;
END $$;
