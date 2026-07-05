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

	events := db.NewLocalEventStore(pool)
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

	go func() {
		log.Printf("image-analysis-worker started, debug endpoint on :%s", port)
		if err := http.ListenAndServe(":"+port, mux); err != nil {
			log.Fatal(err)
		}
	}()

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
