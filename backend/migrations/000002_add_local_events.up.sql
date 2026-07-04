CREATE TABLE local_events (
    id bigserial PRIMARY KEY,
    topic text NOT NULL,
    event_id text NOT NULL,
    payload jsonb NOT NULL,
    available_at timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now(),
    acked_at timestamptz
);

CREATE INDEX idx_local_events_topic_unacked ON local_events (topic, available_at)
    WHERE acked_at IS NULL;
