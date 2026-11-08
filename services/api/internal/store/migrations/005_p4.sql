CREATE TABLE IF NOT EXISTS duty_groups (
    id         text PRIMARY KEY,
    name       text NOT NULL,
    areas      jsonb NOT NULL DEFAULT '[]'::jsonb,
    operators  jsonb NOT NULL DEFAULT '[]'::jsonb,
    active     boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS duty_groups_active_idx ON duty_groups (active);
