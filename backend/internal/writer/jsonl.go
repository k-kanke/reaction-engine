package writer

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// AppendCompactRawFeature appends payload as one JSON line to
// {baseDir}/sessions/{sessionID}/features/compact-raw/part-0001.jsonl,
// creating parent directories as needed. Each line carries its own
// event_id so duplicate lines (e.g. from an unacked event reprocessed
// after a restart) can be deduped downstream during post-session
// analysis, per architecture.md's storage policy for Cloud Storage JSONL.
func AppendCompactRawFeature(baseDir, sessionID string, payload any) error {
	dir := filepath.Join(baseDir, "sessions", sessionID, "features", "compact-raw")
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
