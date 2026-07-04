package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"time"

	"github.com/k-kanke/reaction-engine/backend/internal/contract"
	"github.com/k-kanke/reaction-engine/backend/internal/db"
	"github.com/k-kanke/reaction-engine/backend/internal/writer"
)

const (
	featureEventsTopic = "feature-events"
	pollInterval       = 2 * time.Second
	pollBatchSize      = 50
)

func main() {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		databaseURL = "postgres://reaction:reaction@localhost:5432/reaction?sslmode=disable"
	}

	jsonlDir := os.Getenv("LOCAL_JSONL_DIR")
	if jsonlDir == "" {
		jsonlDir = "./tmp/jsonl"
	}

	ctx := context.Background()

	pool, err := db.NewPool(ctx, databaseURL)
	if err != nil {
		log.Fatalf("writer: failed to connect to postgres: %v", err)
	}
	defer pool.Close()

	events := db.NewLocalEventStore(pool)
	transcripts := writer.NewTranscriptStore(pool)

	log.Println("writer started")

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for range ticker.C {
		unacked, err := events.FetchUnacked(ctx, featureEventsTopic, pollBatchSize)
		if err != nil {
			log.Printf("writer: fetch unacked events failed: %v", err)
			continue
		}
		for _, e := range unacked {
			var payload contract.FeatureEventPayload
			if err := json.Unmarshal(e.Payload, &payload); err != nil {
				log.Printf("writer: invalid payload for event id=%d event_id=%s: %v", e.ID, e.EventID, err)
				continue
			}

			if ok := writeEvent(ctx, jsonlDir, transcripts, e.ID, payload); !ok {
				continue
			}

			if err := events.Ack(ctx, e.ID); err != nil {
				log.Printf("writer: ack failed for event id=%d event_id=%s: %v", e.ID, e.EventID, err)
				continue
			}

			log.Printf("writer: wrote + acked event id=%d event_id=%s session_id=%s", e.ID, e.EventID, payload.SessionID)
		}
	}
}

// writeEvent persists one feature-events payload: compact raw features (if
// any) as JSONL, and transcript_chunks (if any, Phase 11) as both JSONL and
// a `transcripts` Postgres row. A payload from realtime_feature carries
// only Features; one from audio_chunk carries only TranscriptChunks (see
// contract.FeatureEventPayload). Returns false if any step failed, so the
// caller leaves the event unacked for retry.
func writeEvent(ctx context.Context, jsonlDir string, transcripts *writer.TranscriptStore, eventID int64, payload contract.FeatureEventPayload) bool {
	if len(payload.Features) > 0 {
		if err := writer.AppendCompactRawFeature(jsonlDir, payload.SessionID, payload); err != nil {
			log.Printf("writer: write compact raw feature failed for event id=%d event_id=%s: %v", eventID, payload.EventID, err)
			return false
		}
	}

	for _, chunk := range payload.TranscriptChunks {
		if err := transcripts.EnsureSession(ctx, chunk.SessionID); err != nil {
			log.Printf("writer: ensure session failed for event id=%d event_id=%s: %v", eventID, payload.EventID, err)
			return false
		}
		if err := transcripts.InsertTranscriptChunk(ctx, chunk); err != nil {
			log.Printf("writer: insert transcript failed for event id=%d event_id=%s: %v", eventID, payload.EventID, err)
			return false
		}
		if err := writer.AppendTranscriptChunk(jsonlDir, chunk.SessionID, chunk); err != nil {
			log.Printf("writer: write transcript jsonl failed for event id=%d event_id=%s: %v", eventID, payload.EventID, err)
			return false
		}
	}

	return true
}
