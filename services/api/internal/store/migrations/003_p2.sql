CREATE TABLE IF NOT EXISTS camera_rules (
    camera_id            text PRIMARY KEY REFERENCES cameras(id) ON DELETE CASCADE,
    confidence_threshold double precision NOT NULL DEFAULT 0.6,
    dedupe_seconds       integer NOT NULL DEFAULT 5,
    enabled_classes      jsonb NOT NULL DEFAULT '[]'::jsonb,
    roi                  jsonb,
    updated_at           timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS orders (
    id         text PRIMARY KEY,
    event_id   text NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    camera_id  text NOT NULL,
    title      text NOT NULL,
    status     text NOT NULL DEFAULT 'open',
    priority   text NOT NULL DEFAULT 'normal',
    assignee   text NOT NULL DEFAULT '',
    note       text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    closed_at  timestamptz
);
CREATE INDEX IF NOT EXISTS orders_status_idx ON orders (status, created_at DESC);
CREATE INDEX IF NOT EXISTS orders_event_idx ON orders (event_id);
CREATE INDEX IF NOT EXISTS orders_assignee_idx ON orders (assignee);
