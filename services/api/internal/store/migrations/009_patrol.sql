CREATE TABLE IF NOT EXISTS patrol_routes (
    id               text PRIMARY KEY,
    name             text NOT NULL,
    waypoints        jsonb NOT NULL DEFAULT '[]'::jsonb,
    camera_ids       jsonb NOT NULL DEFAULT '[]'::jsonb,
    interval_seconds integer NOT NULL DEFAULT 3,
    created_by       text NOT NULL DEFAULT '',
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS patrol_routes_created_idx ON patrol_routes (created_at DESC);
