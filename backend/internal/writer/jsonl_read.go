package writer

import (
	"context"
	"encoding/json"

	"github.com/k-kanke/reaction-engine/backend/internal/contract"
)

// readTyped reads every line Store has under sessions/{sessionID}/{parts...}/
// and unmarshals each into T. A backend with nothing written yet for this
// session returns an empty slice, not an error, since Post-session Job may
// run against a session that never went through every pipeline (e.g. no
// audio_chunk was ever sent, so there's no transcript/ prefix).
func readTyped[T any](ctx context.Context, store JSONLStore, sessionID string, parts ...string) ([]T, error) {
	lines, err := store.ReadAll(ctx, sessionID, parts...)
	if err != nil {
		return nil, err
	}

	items := make([]T, 0, len(lines))
	for _, line := range lines {
		var item T
		if err := json.Unmarshal(line, &item); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

// ReadTranscriptChunks reads every transcript_chunk the writer has appended
// for one session.
func ReadTranscriptChunks(ctx context.Context, store JSONLStore, sessionID string) ([]contract.TranscriptChunk, error) {
	return readTyped[contract.TranscriptChunk](ctx, store, sessionID, "transcript")
}

// ReadMoodWaveSamples reads every mood_wave_sample the writer has appended
// for one session (Step 6/10 of plan/mood-wave-contract-migration.md) — the
// mood-wave JSONL is mood_wave_sample's only durable copy, since Cloud SQL
// never gets one row per sample.
func ReadMoodWaveSamples(ctx context.Context, store JSONLStore, sessionID string) ([]contract.MoodWaveSampleMessage, error) {
	return readTyped[contract.MoodWaveSampleMessage](ctx, store, sessionID, "mood-wave")
}
