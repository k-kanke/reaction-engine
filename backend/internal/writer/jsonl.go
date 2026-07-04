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

// AppendCompactRawFeature appends payload as one JSON line to
// {baseDir}/sessions/{sessionID}/features/compact-raw/part-0001.jsonl.
// Each line carries its own event_id so duplicate lines (e.g. from an
// unacked event reprocessed after a restart) can be deduped downstream
// during post-session analysis, per architecture.md's storage policy for
// Cloud Storage JSONL.
func AppendCompactRawFeature(baseDir, sessionID string, payload any) error {
	return appendJSONLine(payload, baseDir, "sessions", sessionID, "features", "compact-raw")
}

// AppendTranscriptChunk appends one finalized transcript_chunk as a JSON
// line to {baseDir}/sessions/{sessionID}/transcript/part-0001.jsonl,
// matching architecture.md's Cloud Storage layout
// (.../transcript/part-0001.jsonl, separate from the features/ prefix).
func AppendTranscriptChunk(baseDir, sessionID string, chunk any) error {
	return appendJSONLine(chunk, baseDir, "sessions", sessionID, "transcript")
}

// AppendDecisionLog appends one decision_log as a JSON line to
// {baseDir}/sessions/{sessionID}/features/decision-log/part-0001.jsonl,
// matching architecture.md's Cloud Storage layout (Phase 12 of
// plan/backend-local-docker-runbook.md).
func AppendDecisionLog(baseDir, sessionID string, log any) error {
	return appendJSONLine(log, baseDir, "sessions", sessionID, "features", "decision-log")
}
