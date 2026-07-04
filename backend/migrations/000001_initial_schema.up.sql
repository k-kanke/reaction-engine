CREATE TABLE sessions (
    session_id text PRIMARY KEY,
    meeting_provider text NOT NULL,
    status text NOT NULL DEFAULT 'active',
    consent jsonb NOT NULL DEFAULT '{}'::jsonb,
    started_at timestamptz NOT NULL DEFAULT now(),
    ended_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE participants (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id text NOT NULL REFERENCES sessions (session_id),
    audience_id text NOT NULL,
    role text,
    joined_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (session_id, audience_id)
);

CREATE TABLE capture_snapshots (
    capture_id text PRIMARY KEY,
    session_id text NOT NULL REFERENCES sessions (session_id),
    audience_id text NOT NULL,
    tile_id text,
    t_ms bigint NOT NULL,
    media_ref text NOT NULL,
    upload_status text NOT NULL,
    feature_snapshot jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    uploaded_at timestamptz
);

CREATE INDEX idx_capture_snapshots_session ON capture_snapshots (session_id);

CREATE TABLE media_refs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id text NOT NULL REFERENCES sessions (session_id),
    capture_id text REFERENCES capture_snapshots (capture_id),
    media_ref text NOT NULL UNIQUE,
    purpose text NOT NULL,
    content_type text NOT NULL,
    upload_status text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    uploaded_at timestamptz
);

CREATE INDEX idx_media_refs_session ON media_refs (session_id);

CREATE TABLE participant_baselines (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id text NOT NULL REFERENCES sessions (session_id),
    audience_id text NOT NULL,
    baseline jsonb NOT NULL,
    sample_count int NOT NULL,
    confidence double precision NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (session_id, audience_id)
);

CREATE TABLE visual_summaries (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id text NOT NULL REFERENCES sessions (session_id),
    audience_id text NOT NULL,
    capture_id text REFERENCES capture_snapshots (capture_id),
    media_ref text,
    visual_summary jsonb NOT NULL,
    confidence double precision,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_visual_summaries_session_audience ON visual_summaries (session_id, audience_id);

CREATE TABLE signal_summaries (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id text NOT NULL UNIQUE,
    session_id text NOT NULL REFERENCES sessions (session_id),
    audience_id text NOT NULL,
    t_ms bigint NOT NULL,
    summary jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_signal_summaries_session_audience ON signal_summaries (session_id, audience_id);

CREATE TABLE decision_logs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id text NOT NULL UNIQUE,
    session_id text NOT NULL REFERENCES sessions (session_id),
    audience_id text NOT NULL,
    t_ms bigint NOT NULL,
    source text NOT NULL,
    decision jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_decision_logs_session_audience ON decision_logs (session_id, audience_id);

CREATE TABLE transcripts (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id text NOT NULL UNIQUE,
    session_id text NOT NULL REFERENCES sessions (session_id),
    speaker text NOT NULL,
    t_start_ms bigint NOT NULL,
    t_end_ms bigint NOT NULL,
    text text NOT NULL,
    confidence double precision,
    is_final boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_transcripts_session ON transcripts (session_id);

CREATE TABLE feedback_events (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id text NOT NULL UNIQUE,
    session_id text NOT NULL REFERENCES sessions (session_id),
    audience_id text,
    t_ms bigint NOT NULL,
    feedback_type text NOT NULL,
    severity text NOT NULL,
    message text NOT NULL,
    reason_codes jsonb,
    evidence_quote text,
    source text NOT NULL,
    model_version text,
    confidence double precision,
    cooldown_ms integer,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_feedback_events_session ON feedback_events (session_id);

CREATE TABLE reports (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id text NOT NULL REFERENCES sessions (session_id),
    report jsonb NOT NULL,
    generated_at timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_reports_session ON reports (session_id);
