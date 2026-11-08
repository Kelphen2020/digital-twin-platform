ALTER TABLE duty_groups ADD COLUMN IF NOT EXISTS rotation text NOT NULL DEFAULT 'group';
ALTER TABLE duty_groups ADD COLUMN IF NOT EXISTS shifts jsonb NOT NULL DEFAULT '[]'::jsonb;
ALTER TABLE events ADD COLUMN IF NOT EXISTS re_escalated_at timestamptz;
CREATE INDEX IF NOT EXISTS events_re_escalated_idx ON events (re_escalated_at);
