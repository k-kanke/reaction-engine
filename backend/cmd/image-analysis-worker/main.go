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
	"github.com/k-kanke/reaction-engine/backend/internal/imageanalysis"
	"github.com/k-kanke/reaction-engine/backend/internal/media"
	"github.com/k-kanke/reaction-engine/backend/internal/pubsub"
	"github.com/k-kanke/reaction-engine/backend/internal/redis"
)

const (
	mediaAnalysisEventsTopic = "media-analysis-events"
	pollInterval             = 2 * time.Second
	pollBatchSize            = 50
)

func main() {
	port := os.Getenv("IMAGE_ANALYSIS_WORKER_DEBUG_PORT")
	if port == "" {
		port = "8082"
	}

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		databaseURL = "postgres://reaction:reaction@localhost:5432/reaction?sslmode=disable"
	}

	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "localhost:6379"
	}

	localMediaDir := os.Getenv("LOCAL_MEDIA_DIR")
	if localMediaDir == "" {
		localMediaDir = "./tmp/media"
	}

	ctx := context.Background()

	pool, err := db.NewPool(ctx, databaseURL)
	if err != nil {
		log.Fatalf("image-analysis-worker: failed to connect to postgres: %v", err)
	}
	defer pool.Close()

	redisClient := redis.NewClient(redisAddr)
	defer redisClient.Close()

	store := imageanalysis.NewPGStore(pool)

	mediaStoreBackend := os.Getenv("MEDIA_STORE_BACKEND")
	if mediaStoreBackend == "" {
		mediaStoreBackend = "local"
	}

	var mediaStore media.MediaReader
	switch mediaStoreBackend {
	case "local":
		mediaStore = media.NewLocalMediaStore(localMediaDir, "", 0)
	case "gcs":
		bucket := os.Getenv("GCS_MEDIA_BUCKET")
		if bucket == "" {
			log.Fatal("image-analysis-worker: GCS_MEDIA_BUCKET is required when MEDIA_STORE_BACKEND=gcs")
		}
		gcsStore, err := media.NewGCSMediaStore(ctx, bucket, os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"), 0)
		if err != nil {
			log.Fatalf("image-analysis-worker: failed to create gcs media store: %v", err)
		}
		mediaStore = gcsStore
	default:
		log.Fatalf("image-analysis-worker: unknown MEDIA_STORE_BACKEND %q (want local or gcs)", mediaStoreBackend)
	}

	worker := imageanalysis.NewWorker(store, redisClient, mediaStore)

	mux := http.NewServeMux()
	mux.HandleFunc("/debug/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})

	// EVENT_BUS_BACKEND mirrors writer's split (plan/
	// post-session-report-implementation.md's Pub/Sub migration): "local"
	// (default) keeps polling local_events; "pubsub" registers a push
	// endpoint instead, letting this service scale to zero between
	// baseline-frame uploads instead of staying pinned at
	// min_instance_count=1 to poll Postgres every 2s.
	eventBusBackend := os.Getenv("EVENT_BUS_BACKEND")
	if eventBusBackend == "" {
		eventBusBackend = "local"
	}

	switch eventBusBackend {
	case "local":
		events := db.NewLocalEventStore(pool)
		go pollLocalEvents(ctx, events, worker)
	case "pubsub":
		mux.HandleFunc("/pubsub/push", newPushHandler(worker))
	default:
		log.Fatalf("image-analysis-worker: unknown EVENT_BUS_BACKEND %q (want local or pubsub)", eventBusBackend)
	}

	log.Printf("image-analysis-worker started, event_bus_backend=%s, debug endpoint on :%s", eventBusBackend, port)
	log.Fatal(http.ListenAndServe(":"+port, mux))
}

// newPushHandler builds the POST /pubsub/push handler Pub/Sub calls for
// each "media-analysis-events" message (EVENT_BUS_BACKEND=pubsub). A 200
// response acks the message; any other status tells Pub/Sub to retry per
// the subscription's backoff policy (infra/modules/pubsub).
func newPushHandler(worker *imageanalysis.Worker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		msg, err := pubsub.DecodePush(r.Body)
		if err != nil {
			log.Printf("image-analysis-worker: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		var payload contract.MediaUploadedEventPayload
		if err := json.Unmarshal(msg.Message.Data, &payload); err != nil {
			// Poison message -- retrying won't fix malformed JSON, so ack
			// it (200) rather than let Pub/Sub redeliver it forever.
			log.Printf("image-analysis-worker: invalid payload for message_id=%s: %v", msg.Message.MessageID, err)
			w.WriteHeader(http.StatusOK)
			return
		}

		if err := worker.ProcessMediaUploaded(r.Context(), payload); err != nil {
			log.Printf("image-analysis-worker: process failed for event_id=%s: %v", payload.EventID, err)
			http.Error(w, "processing failed", http.StatusInternalServerError)
			return
		}

		log.Printf("image-analysis-worker: processed + acked event_id=%s session_id=%s audience_id=%s",
			payload.EventID, payload.SessionID, payload.AudienceID)
		w.WriteHeader(http.StatusOK)
	}
}

// pollLocalEvents is the EVENT_BUS_BACKEND=local dev path: the original
// poll loop against db.LocalEventStore, unchanged in behavior.
func pollLocalEvents(ctx context.Context, events *db.LocalEventStore, worker *imageanalysis.Worker) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for range ticker.C {
		unacked, err := events.FetchUnacked(ctx, mediaAnalysisEventsTopic, pollBatchSize)
		if err != nil {
			log.Printf("image-analysis-worker: fetch unacked events failed: %v", err)
			continue
		}
		for _, e := range unacked {
			var payload contract.MediaUploadedEventPayload
			if err := json.Unmarshal(e.Payload, &payload); err != nil {
				log.Printf("image-analysis-worker: invalid payload for event id=%d event_id=%s: %v", e.ID, e.EventID, err)
				continue
			}

			if err := worker.ProcessMediaUploaded(ctx, payload); err != nil {
				log.Printf("image-analysis-worker: process failed for event id=%d event_id=%s: %v", e.ID, e.EventID, err)
				continue
			}

			if err := events.Ack(ctx, e.ID); err != nil {
				log.Printf("image-analysis-worker: ack failed for event id=%d event_id=%s: %v", e.ID, e.EventID, err)
				continue
			}

			log.Printf("image-analysis-worker: processed + acked event id=%d event_id=%s session_id=%s audience_id=%s",
				e.ID, e.EventID, payload.SessionID, payload.AudienceID)
		}
	}
}
