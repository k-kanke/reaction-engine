package contract

// RealtimeFeatureMessage is the payload the Chrome extension used to send
// over the gateway WebSocket for type "realtime_feature", before Step 4 of
// plan/mood-wave-contract-migration.md replaced that ingress path with
// MoodWaveSampleMessage. Nothing produces this anymore; it (and FaceTrack,
// CompactFeature, FeatureEventPayload.Features/DecisionLogs below) stays
// only because internal/postsession/report.go still consumes it, and is
// removed once Step 10 rebuilds that report from mood-wave JSONL instead.
type RealtimeFeatureMessage struct {
	Type      string           `json:"type"`
	SessionID string           `json:"session_id"`
	TMs       int64            `json:"t_ms"`
	Features  RealtimeFeatures `json:"features"`
}

type RealtimeFeatures struct {
	FaceTracks []FaceTrack `json:"face_tracks"`
}

type FaceTrack struct {
	AudienceID     string  `json:"audience_id"`
	AttentionScore float64 `json:"attention_score"`
}

// FeedbackEvent is returned to the Chrome extension over the same
// WebSocket connection, per architecture.md's "Feedback Event（Gateway ->
// Chrome / Pub/Sub）" contract. Under the mood_wave_sample model feedback is
// keyed by trigger (TriggerID), not by participant: AudienceID is kept as
// an optional field for the transition (see
// plan/mood-wave-contract-migration.md Step 4/5, which decide whether the
// Realtime Worker still populates it once realtime_feature is retired) but
// is omitted from the wire payload when empty. EventID is likewise omitted
// on the wire (architecture.md's Chrome-facing example has none) — the
// gateway only sets it on the copy it hands to Durable Writer for
// idempotent feedback_events persistence (Step 6).
type FeedbackEvent struct {
	Type          string   `json:"type"`
	EventID       string   `json:"event_id,omitempty"`
	SessionID     string   `json:"session_id"`
	AudienceID    string   `json:"audience_id,omitempty"`
	TMs           int64    `json:"t_ms"`
	TriggerID     string   `json:"trigger_id,omitempty"`
	FeedbackType  string   `json:"feedback_type"`
	Severity      string   `json:"severity"`
	Message       string   `json:"message"`
	ReasonCodes   []string `json:"reason_codes,omitempty"`
	EvidenceQuote *string  `json:"evidence_quote,omitempty"`
	Source        string   `json:"source"`
	ModelVersion  string   `json:"model_version,omitempty"`
	Confidence    float64  `json:"confidence,omitempty"`
	CooldownMs    int      `json:"cooldown_ms,omitempty"`
}

// TriggerEvent mirrors one row of the trigger_events table (migration
// 000003_add_trigger_events, Step 1 of
// plan/mood-wave-contract-migration.md). The gateway builds one only for
// triggers the Realtime Worker accepted (i.e. that produced a
// FeedbackEvent) — rejected/cooldown-suppressed triggers are never
// persisted, matching architecture.md's リアルタイムFBフロー step 14
// ("publish trigger_event + feedback_event") being the last step, after
// acceptance.
type TriggerEvent struct {
	EventID   string  `json:"event_id"`
	SessionID string  `json:"session_id"`
	TriggerID string  `json:"trigger_id"`
	Type      string  `json:"type"`
	Source    string  `json:"source"`
	TMs       int64   `json:"t_ms"`
	PeakTMs   int64   `json:"peak_t_ms"`
	Delta     float64 `json:"delta"`
}

// CompactFeature is what the gateway stores in the Redis recent window per
// session_id + audience_id.
type CompactFeature struct {
	EventID            string  `json:"event_id"`
	SessionID          string  `json:"session_id"`
	AudienceID         string  `json:"audience_id"`
	TMs                int64   `json:"t_ms"`
	ServerReceivedAtMs int64   `json:"server_received_at_ms"`
	AttentionScore     float64 `json:"attention_score"`
}

// FeatureEventPayload is the local_events payload the gateway publishes to
// the "feature-events" topic for durable processing (local stand-in for
// Pub/Sub, per plan/backend-local-docker-runbook.md Phase 5). Exactly one
// of MoodWaveSample (from mood_wave_sample), TriggerEvents+FeedbackEvents
// (from an accepted trigger, Step 5/6), TranscriptChunks (from audio_chunk),
// Features + DecisionLogs (legacy realtime_feature, kept only for
// internal/postsession until Step 10) is populated per event, since each
// message/decision is enqueued independently.
type FeatureEventPayload struct {
	EventID            string                 `json:"event_id"`
	SessionID          string                 `json:"session_id"`
	TMs                int64                  `json:"t_ms"`
	ServerReceivedAtMs int64                  `json:"server_received_at_ms"`
	MoodWaveSample     *MoodWaveSampleMessage `json:"mood_wave_sample,omitempty"`
	TriggerEvents      []TriggerEvent         `json:"trigger_events,omitempty"`
	FeedbackEvents     []FeedbackEvent        `json:"feedback_events,omitempty"`
	Features           []CompactFeature       `json:"features,omitempty"`
	TranscriptChunks   []TranscriptChunk      `json:"transcript_chunks,omitempty"`
	DecisionLogs       []DecisionLog          `json:"decision_logs,omitempty"`
}
