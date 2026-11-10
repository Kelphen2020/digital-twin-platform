-- P28: extend tenant isolation to duty groups and track points.
ALTER TABLE duty_groups  ADD COLUMN IF NOT EXISTS tenant_id text NOT NULL DEFAULT 'default';
ALTER TABLE track_points ADD COLUMN IF NOT EXISTS tenant_id text NOT NULL DEFAULT 'default';

CREATE INDEX IF NOT EXISTS duty_groups_tenant_idx ON duty_groups (tenant_id, created_at);
CREATE INDEX IF NOT EXISTS track_points_tenant_idx ON track_points (tenant_id, ts DESC);
