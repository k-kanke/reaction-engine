CREATE TABLE trigger_events (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id text NOT NULL UNIQUE,
    session_id text NOT NULL REFERENCES sessions (session_id),
    trigger_id text NOT NULL,
    type text NOT NULL,
    source text NOT NULL,
    t_ms bigint NOT NULL,
    peak_t_ms bigint NOT NULL,
    delta double precision NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_trigger_events_session ON trigger_events (session_id);
CREATE UNIQUE INDEX idx_trigger_events_trigger_id ON trigger_events (session_id, trigger_id);

ALTER TABLE capture_snapshots ADD COLUMN trigger_id text;
ALTER TABLE media_refs ADD COLUMN trigger_id text;
