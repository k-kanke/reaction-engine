ALTER TABLE media_refs DROP COLUMN IF EXISTS trigger_id;
ALTER TABLE capture_snapshots DROP COLUMN IF EXISTS trigger_id;

DROP TABLE IF EXISTS trigger_events;
