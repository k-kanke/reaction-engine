package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/k-kanke/reaction-engine/backend/internal/contract"
	"github.com/k-kanke/reaction-engine/backend/internal/db"
	"github.com/k-kanke/reaction-engine/backend/internal/pubsub"
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

	// Step G of plan/gcp-deployment-runbook.md: Cloud Run Services (unlike
	// Jobs) require the container to listen on $PORT and respond, or the
	// revision never becomes healthy. writer previously had no HTTP
	// listener at all (a bare poll loop), unlike image-analysis-worker's
	// /debug/healthz -- this was the last gap in
	// backend-local-docker-runbook.md's Phase 15 "GET /healthz on every
	// service" checklist item.
	port := os.Getenv("WRITER_PORT")
	if port == "" {
		port = "8080"
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})

	// EVENT_BUS_BACKEND mirrors gateway/media-api's split (plan/
	// post-session-report-implementation.md's Pub/Sub migration):
	// "local" (default) keeps polling local_events, matching before;
	// "pubsub" registers a push endpoint instead and never touches
	// local_events -- Pub/Sub calls this service directly, so it no
	// longer needs to stay warm polling Postgres every 2s.
	eventBusBackend := os.Getenv("EVENT_BUS_BACKEND")
	if eventBusBackend == "" {
		eventBusBackend = "local"
	}

	switch eventBusBackend {
	case "local":
		events := db.NewLocalEventStore(pool)
		go pollLocalEvents(ctx, events, jsonlStore, store)
	case "pubsub":
		mux.HandleFunc("/pubsub/push", newPushHandler(jsonlStore, store))
	default:
		log.Fatalf("writer: unknown EVENT_BUS_BACKEND %q (want local or pubsub)", eventBusBackend)
	}

	log.Printf("writer started, event_bus_backend=%s, healthz endpoint on :%s", eventBusBackend, port)
	log.Fatal(http.ListenAndServe(":"+port, mux))
}

// newPushHandler builds the POST /pubsub/push handler Pub/Sub calls for
// each "feature-events" message (EVENT_BUS_BACKEND=pubsub). A 200 response
// acks the message; any other status tells Pub/Sub to retry per the
// subscription's backoff policy (infra/modules/pubsub) -- which is what
// now absorbs the transient GCS 429s internal/writer/gcs_store.go's
// compose-append path can hit under bursty write volume, instead of the
// old poll loop's fixed 2s retry.
func newPushHandler(jsonlStore writer.JSONLStore, store *writer.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		msg, err := pubsub.DecodePush(r.Body)
		if err != nil {
			log.Printf("writer: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		var payload contract.FeatureEventPayload
		if err := json.Unmarshal(msg.Message.Data, &payload); err != nil {
			// Poison message -- retrying won't fix malformed JSON, so ack
			// it (200) rather than let Pub/Sub redeliver it forever.
			log.Printf("writer: invalid payload for message_id=%s: %v", msg.Message.MessageID, err)
			w.WriteHeader(http.StatusOK)
			return
		}

		if !writeEvent(r.Context(), jsonlStore, store, payload) {
			http.Error(w, "processing failed", http.StatusInternalServerError)
			return
		}

		log.Printf("writer: wrote + acked event_id=%s session_id=%s", payload.EventID, payload.SessionID)
		w.WriteHeader(http.StatusOK)
	}
}

// pollLocalEvents is the EVENT_BUS_BACKEND=local dev path: the original
// Phase 5 poll loop against db.LocalEventStore, unchanged in behavior.
func pollLocalEvents(ctx context.Context, events *db.LocalEventStore, jsonlStore writer.JSONLStore, store *writer.Store) {
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

			if !writeEvent(ctx, jsonlStore, store, payload) {
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
// caller (poll loop: leaves the event unacked for retry; push handler:
// returns a 5xx so Pub/Sub retries) knows to retry.
func writeEvent(ctx context.Context, jsonlStore writer.JSONLStore, store *writer.Store, payload contract.FeatureEventPayload) bool {
	if payload.MoodWaveSample != nil {
		if err := writer.AppendMoodWaveSample(ctx, jsonlStore, payload.SessionID, payload.EventID, payload.MoodWaveSample); err != nil {
			log.Printf("writer: write mood wave sample jsonl failed for event_id=%s: %v", payload.EventID, err)
			return false
		}
	}

	for _, trigger := range payload.TriggerEvents {
		if err := store.EnsureSession(ctx, trigger.SessionID); err != nil {
			log.Printf("writer: ensure session failed for event_id=%s: %v", payload.EventID, err)
			return false
		}
		if err := store.InsertTriggerEvent(ctx, trigger); err != nil {
			log.Printf("writer: insert trigger event failed for event_id=%s: %v", payload.EventID, err)
			return false
		}
		if err := writer.AppendTriggerEvent(ctx, jsonlStore, trigger.SessionID, trigger.EventID, trigger); err != nil {
			log.Printf("writer: write trigger event jsonl failed for event_id=%s: %v", payload.EventID, err)
			return false
		}
	}

	for _, feedback := range payload.FeedbackEvents {
		if err := store.EnsureSession(ctx, feedback.SessionID); err != nil {
			log.Printf("writer: ensure session failed for event_id=%s: %v", payload.EventID, err)
			return false
		}
		if err := store.InsertFeedbackEvent(ctx, feedback); err != nil {
			log.Printf("writer: insert feedback event failed for event_id=%s: %v", payload.EventID, err)
			return false
		}
		if err := writer.AppendFeedbackEvent(ctx, jsonlStore, feedback.SessionID, feedback.EventID, feedback); err != nil {
			log.Printf("writer: write feedback event jsonl failed for event_id=%s: %v", payload.EventID, err)
			return false
		}
	}

	for _, chunk := range payload.TranscriptChunks {
		if err := store.EnsureSession(ctx, chunk.SessionID); err != nil {
			log.Printf("writer: ensure session failed for event_id=%s: %v", payload.EventID, err)
			return false
		}
		if err := store.InsertTranscriptChunk(ctx, chunk); err != nil {
			log.Printf("writer: insert transcript failed for event_id=%s: %v", payload.EventID, err)
			return false
		}
		if err := writer.AppendTranscriptChunk(ctx, jsonlStore, chunk.SessionID, chunk.EventID, chunk); err != nil {
			log.Printf("writer: write transcript jsonl failed for event_id=%s: %v", payload.EventID, err)
			return false
		}
	}

	return true
}
