package writer

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/k-kanke/reaction-engine/backend/internal/contract"
)

// readJSONLLines reads {baseDir}/{parts...}/part-0001.jsonl (the layout
// appendJSONLine writes) and unmarshals each non-empty line into T. A
// missing file — nothing written yet for this session — returns an empty
// slice, not an error, since Post-session Job (Phase 13) may run against a
// session that never went through every pipeline (e.g. no audio_chunk was
// ever sent, so there's no transcript/ directory).
func readJSONLLines[T any](baseDir string, parts ...string) ([]T, error) {
	dir := filepath.Join(append([]string{baseDir}, parts...)...)
	path := filepath.Join(dir, "part-0001.jsonl")

	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var items []T
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var item T
		if err := json.Unmarshal(line, &item); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

// ReadCompactRawFeatures reads every feature-events payload the writer has
// appended for one session's compact-raw JSONL.
func ReadCompactRawFeatures(baseDir, sessionID string) ([]contract.FeatureEventPayload, error) {
	return readJSONLLines[contract.FeatureEventPayload](baseDir, "sessions", sessionID, "features", "compact-raw")
}

// ReadTranscriptChunks reads every transcript_chunk the writer has appended
// for one session.
func ReadTranscriptChunks(baseDir, sessionID string) ([]contract.TranscriptChunk, error) {
	return readJSONLLines[contract.TranscriptChunk](baseDir, "sessions", sessionID, "transcript")
}

// ReadDecisionLogs reads every decision_log the writer has appended for one
// session.
func ReadDecisionLogs(baseDir, sessionID string) ([]contract.DecisionLog, error) {
	return readJSONLLines[contract.DecisionLog](baseDir, "sessions", sessionID, "features", "decision-log")
}
