ALTER TABLE cameras ADD COLUMN IF NOT EXISTS calibration jsonb;
ALTER TABLE events ADD COLUMN IF NOT EXISTS closed_at timestamptz;

CREATE TABLE IF NOT EXISTS track_points (
    id        bigserial PRIMARY KEY,
    camera_id text NOT NULL,
    track_id  text NOT NULL,
    class     text NOT NULL,
    x         double precision NOT NULL,
    y         double precision NOT NULL,
    z         double precision NOT NULL DEFAULT 0,
    ts        timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS track_points_ts_idx ON track_points (ts DESC);
CREATE INDEX IF NOT EXISTS track_points_camera_idx ON track_points (camera_id, ts DESC);
CREATE INDEX IF NOT EXISTS track_points_track_idx ON track_points (track_id, ts DESC);

CREATE TABLE IF NOT EXISTS event_actions (
    id       text PRIMARY KEY,
    event_id text NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    action   text NOT NULL,
    actor    text NOT NULL DEFAULT '',
    target   text NOT NULL DEFAULT '',
    note     text NOT NULL DEFAULT '',
    at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS event_actions_event_idx ON event_actions (event_id, at);

CREATE TABLE IF NOT EXISTS audit_log (
    id       text PRIMARY KEY,
    at       timestamptz NOT NULL DEFAULT now(),
    actor    text NOT NULL DEFAULT '',
    action   text NOT NULL,
    resource text NOT NULL DEFAULT '',
    detail   jsonb NOT NULL DEFAULT '{}'::jsonb
);
CREATE INDEX IF NOT EXISTS audit_log_at_idx ON audit_log (at DESC);

-- P1 demo stream: point every seed camera at the local HLS test stream.
UPDATE cameras SET stream_url = '/hls/test.m3u8' WHERE stream_url IS NULL;
