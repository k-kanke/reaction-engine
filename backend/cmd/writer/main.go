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
	store := writer.NewStore(pool)

	// JSONL_STORE_BACKEND mirrors MEDIA_STORE_BACKEND (internal/media):
	// "local" writes to LOCAL_JSONL_DIR on this instance's own disk (fine
	// for the single docker compose container that also runs
	// post-session-job/pdf-renderer against the same volume); "gcs" is
	// required once writer and post-session-job/pdf-renderer run as
	// separate Cloud Run instances, since they don't share a filesystem
	// (Step F of plan/gcp-deployment-runbook.md).
	jsonlStoreBackend := os.Getenv("JSONL_STORE_BACKEND")
	if jsonlStoreBackend == "" {
		jsonlStoreBackend = "local"
	}

	var jsonlStore writer.JSONLStore
	switch jsonlStoreBackend {
	case "local":
		jsonlStore = writer.NewLocalJSONLStore(jsonlDir)
	case "gcs":
		bucket := os.Getenv("GCS_JSONL_BUCKET")
		if bucket == "" {
			log.Fatal("writer: GCS_JSONL_BUCKET is required when JSONL_STORE_BACKEND=gcs")
		}
		gcsStore, err := writer.NewGCSJSONLStore(ctx, bucket, os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"))
		if err != nil {
			log.Fatalf("writer: failed to create gcs jsonl store: %v", err)
		}
		jsonlStore = gcsStore
	default:
		log.Fatalf("writer: unknown JSONL_STORE_BACKEND %q (want local or gcs)", jsonlStoreBackend)
	}

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

			if ok := writeEvent(ctx, jsonlStore, store, e.ID, payload); !ok {
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

// writeEvent persists one feature-events payload: mood_wave_sample (if any,
// Step 6 of plan/mood-wave-contract-migration.md) as JSONL only —
// architecture.md's "Cloud SQL に mood_wave_sample 全件を insert しない"
// policy means no Postgres row for it — trigger_events/feedback_events (if
// any, from an accepted trigger) as both JSONL and their Postgres rows, and
// transcript_chunks (if any, Phase 11) as both JSONL and a `transcripts`
// Postgres row. Each event carries exactly one of these (see
// contract.FeatureEventPayload). Returns false if any step failed, so the
// caller leaves the event unacked for retry.
func writeEvent(ctx context.Context, jsonlStore writer.JSONLStore, store *writer.Store, eventID int64, payload contract.FeatureEventPayload) bool {
	if payload.MoodWaveSample != nil {
		if err := writer.AppendMoodWaveSample(ctx, jsonlStore, payload.SessionID, payload.EventID, payload.MoodWaveSample); err != nil {
			log.Printf("writer: write mood wave sample jsonl failed for event id=%d event_id=%s: %v", eventID, payload.EventID, err)
			return false
		}
	}

	for _, trigger := range payload.TriggerEvents {
		if err := store.EnsureSession(ctx, trigger.SessionID); err != nil {
			log.Printf("writer: ensure session failed for event id=%d event_id=%s: %v", eventID, payload.EventID, err)
			return false
		}
		if err := store.InsertTriggerEvent(ctx, trigger); err != nil {
			log.Printf("writer: insert trigger event failed for event id=%d event_id=%s: %v", eventID, payload.EventID, err)
			return false
		}
		if err := writer.AppendTriggerEvent(ctx, jsonlStore, trigger.SessionID, trigger.EventID, trigger); err != nil {
			log.Printf("writer: write trigger event jsonl failed for event id=%d event_id=%s: %v", eventID, payload.EventID, err)
			return false
		}
	}

	for _, feedback := range payload.FeedbackEvents {
		if err := store.EnsureSession(ctx, feedback.SessionID); err != nil {
			log.Printf("writer: ensure session failed for event id=%d event_id=%s: %v", eventID, payload.EventID, err)
			return false
		}
		if err := store.InsertFeedbackEvent(ctx, feedback); err != nil {
			log.Printf("writer: insert feedback event failed for event id=%d event_id=%s: %v", eventID, payload.EventID, err)
			return false
		}
		if err := writer.AppendFeedbackEvent(ctx, jsonlStore, feedback.SessionID, feedback.EventID, feedback); err != nil {
			log.Printf("writer: write feedback event jsonl failed for event id=%d event_id=%s: %v", eventID, payload.EventID, err)
			return false
		}
	}

	for _, chunk := range payload.TranscriptChunks {
		if err := store.EnsureSession(ctx, chunk.SessionID); err != nil {
			log.Printf("writer: ensure session failed for event id=%d event_id=%s: %v", eventID, payload.EventID, err)
			return false
		}
		if err := store.InsertTranscriptChunk(ctx, chunk); err != nil {
			log.Printf("writer: insert transcript failed for event id=%d event_id=%s: %v", eventID, payload.EventID, err)
			return false
		}
		if err := writer.AppendTranscriptChunk(ctx, jsonlStore, chunk.SessionID, chunk.EventID, chunk); err != nil {
			log.Printf("writer: write transcript jsonl failed for event id=%d event_id=%s: %v", eventID, payload.EventID, err)
			return false
		}
	}

	return true
}
