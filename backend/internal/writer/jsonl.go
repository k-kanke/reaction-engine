package writer

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// appendJSONLine marshals payload as one JSON line and appends it to
// {baseDir}/{parts...}/part-0001.jsonl, creating parent directories as
// needed.
func appendJSONLine(payload any, baseDir string, parts ...string) error {
	dir := filepath.Join(append([]string{baseDir}, parts...)...)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	body = append(body, '\n')

	path := filepath.Join(dir, "part-0001.jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.Write(body)
	return err
}

// AppendTranscriptChunk appends one finalized transcript_chunk as a JSON
// line to {baseDir}/sessions/{sessionID}/transcript/part-0001.jsonl,
// matching architecture.md's Cloud Storage layout.
func AppendTranscriptChunk(baseDir, sessionID string, chunk any) error {
	return appendJSONLine(chunk, baseDir, "sessions", sessionID, "transcript")
}

// AppendMoodWaveSample appends one mood_wave_sample as a JSON line to
// {baseDir}/sessions/{sessionID}/mood-wave/part-0001.jsonl, matching
// architecture.md's Cloud Storage layout
// (gs://reaction-engine-sessions/sessions/{session_id}/mood-wave/part-0001.jsonl)
// and its "Cloud SQL に mood_wave_sample 全件を insert しない" storage
// policy — this JSONL file is mood_wave_sample's only durable copy
// (Step 6 of plan/mood-wave-contract-migration.md).
func AppendMoodWaveSample(baseDir, sessionID string, sample any) error {
	return appendJSONLine(sample, baseDir, "sessions", sessionID, "mood-wave")
}

// AppendTriggerEvent appends one trigger_event as a JSON line to
// {baseDir}/sessions/{sessionID}/triggers/part-0001.jsonl, matching
// architecture.md's Cloud Storage layout.
func AppendTriggerEvent(baseDir, sessionID string, trigger any) error {
	return appendJSONLine(trigger, baseDir, "sessions", sessionID, "triggers")
}

// AppendFeedbackEvent appends one feedback_event as a JSON line to
// {baseDir}/sessions/{sessionID}/feedback/part-0001.jsonl, matching
// architecture.md's Cloud Storage layout.
func AppendFeedbackEvent(baseDir, sessionID string, feedback any) error {
	return appendJSONLine(feedback, baseDir, "sessions", sessionID, "feedback")
}
