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

// ReadTranscriptChunks reads every transcript_chunk the writer has appended
// for one session.
func ReadTranscriptChunks(baseDir, sessionID string) ([]contract.TranscriptChunk, error) {
	return readJSONLLines[contract.TranscriptChunk](baseDir, "sessions", sessionID, "transcript")
}

// ReadMoodWaveSamples reads every mood_wave_sample the writer has appended
// for one session (Step 6/10 of plan/mood-wave-contract-migration.md) — the
// mood-wave JSONL is mood_wave_sample's only durable copy, since Cloud SQL
// never gets one row per sample.
func ReadMoodWaveSamples(baseDir, sessionID string) ([]contract.MoodWaveSampleMessage, error) {
	return readJSONLLines[contract.MoodWaveSampleMessage](baseDir, "sessions", sessionID, "mood-wave")
}
