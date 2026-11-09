CREATE TABLE IF NOT EXISTS patrol_schedules (
    id               text PRIMARY KEY,
    name             text NOT NULL,
    route_id         text NOT NULL,
    interval_minutes integer NOT NULL DEFAULT 60,
    enabled          boolean NOT NULL DEFAULT true,
    created_by       text NOT NULL DEFAULT '',
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    next_run_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS patrol_schedules_due_idx ON patrol_schedules (enabled, next_run_at);

CREATE TABLE IF NOT EXISTS patrol_executions (
    id           text PRIMARY KEY,
    schedule_id  text NOT NULL DEFAULT '',
    route_id     text NOT NULL,
    route_name   text NOT NULL DEFAULT '',
    status       text NOT NULL DEFAULT 'pending',
    assignee     text NOT NULL DEFAULT '',
    result       text NOT NULL DEFAULT '',
    note         text NOT NULL DEFAULT '',
    order_id     text NOT NULL DEFAULT '',
    scheduled_at timestamptz NOT NULL DEFAULT now(),
    created_at   timestamptz NOT NULL DEFAULT now(),
    started_at   timestamptz,
    finished_at  timestamptz
);
CREATE INDEX IF NOT EXISTS patrol_executions_status_idx ON patrol_executions (status, created_at DESC);
