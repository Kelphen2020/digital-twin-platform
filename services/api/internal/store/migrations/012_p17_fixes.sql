-- P17: nullable order event_id (patrol-generated orders have no event),
-- and notification retry attempts persisted for observability.
ALTER TABLE orders ALTER COLUMN event_id DROP NOT NULL;
ALTER TABLE notification_log ADD COLUMN IF NOT EXISTS attempts integer NOT NULL DEFAULT 0;
