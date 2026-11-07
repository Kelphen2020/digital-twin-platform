ALTER TABLE events ADD COLUMN IF NOT EXISTS escalated_at timestamptz;
CREATE INDEX IF NOT EXISTS events_escalated_idx ON events (escalated_at);

CREATE TABLE IF NOT EXISTS settings (
    key        text PRIMARY KEY,
    value      jsonb NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS notification_log (
    id          text PRIMARY KEY,
    at          timestamptz NOT NULL DEFAULT now(),
    kind        text NOT NULL DEFAULT '',
    event_id    text NOT NULL DEFAULT '',
    url         text NOT NULL DEFAULT '',
    format      text NOT NULL DEFAULT '',
    ok          boolean NOT NULL DEFAULT false,
    status_code integer NOT NULL DEFAULT 0,
    error       text NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS notification_log_at_idx ON notification_log (at DESC);
