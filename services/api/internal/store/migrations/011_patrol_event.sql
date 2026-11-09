ALTER TABLE patrol_executions ADD COLUMN IF NOT EXISTS event_id text NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS patrol_executions_event_idx ON patrol_executions (event_id);
