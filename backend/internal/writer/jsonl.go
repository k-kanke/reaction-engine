package writer

import "context"

// AppendMoodWaveSample appends one mood_wave_sample as a JSON line to
// sessions/{sessionID}/mood-wave/part-0001.jsonl, matching architecture.md's
// Cloud Storage layout
// (gs://reaction-engine-sessions/sessions/{session_id}/mood-wave/part-0001.jsonl)
// and its "Cloud SQL に mood_wave_sample 全件を insert しない" storage
// policy — this JSONL file is mood_wave_sample's only durable copy
// (Step 6 of plan/mood-wave-contract-migration.md).
func AppendMoodWaveSample(ctx context.Context, store JSONLStore, sessionID, eventID string, sample any) error {
	return store.Append(ctx, sessionID, eventID, sample, "mood-wave")
}

// AppendTranscriptChunk appends one finalized transcript_chunk as a JSON
// line to sessions/{sessionID}/transcript/part-0001.jsonl, matching
// architecture.md's Cloud Storage layout.
func AppendTranscriptChunk(ctx context.Context, store JSONLStore, sessionID, eventID string, chunk any) error {
	return store.Append(ctx, sessionID, eventID, chunk, "transcript")
}

// AppendTriggerEvent appends one trigger_event as a JSON line to
// sessions/{sessionID}/triggers/part-0001.jsonl, matching architecture.md's
// Cloud Storage layout.
func AppendTriggerEvent(ctx context.Context, store JSONLStore, sessionID, eventID string, trigger any) error {
	return store.Append(ctx, sessionID, eventID, trigger, "triggers")
}

// AppendFeedbackEvent appends one feedback_event as a JSON line to
// sessions/{sessionID}/feedback/part-0001.jsonl, matching architecture.md's
// Cloud Storage layout.
func AppendFeedbackEvent(ctx context.Context, store JSONLStore, sessionID, eventID string, feedback any) error {
	return store.Append(ctx, sessionID, eventID, feedback, "feedback")
}
