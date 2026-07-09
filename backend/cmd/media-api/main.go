package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/k-kanke/reaction-engine/backend/internal/db"
	"github.com/k-kanke/reaction-engine/backend/internal/media"
	"github.com/k-kanke/reaction-engine/backend/internal/pubsub"
)

func main() {
	port := os.Getenv("MEDIA_API_PORT")
	if port == "" {
		port = "8081"
	}

	publicBaseURL := os.Getenv("MEDIA_API_PUBLIC_BASE_URL")
	if publicBaseURL == "" {
		publicBaseURL = "http://localhost:" + port
	}

	localMediaDir := os.Getenv("LOCAL_MEDIA_DIR")
	if localMediaDir == "" {
		localMediaDir = "./tmp/media"
	}

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		databaseURL = "postgres://reaction:reaction@localhost:5432/reaction?sslmode=disable"
	}

	signedURLTTL := 900 * time.Second
	if raw := os.Getenv("SIGNED_URL_TTL_SECONDS"); raw != "" {
		if seconds, err := strconv.Atoi(raw); err == nil {
			signedURLTTL = time.Duration(seconds) * time.Second
		} else {
			log.Printf("media-api: invalid SIGNED_URL_TTL_SECONDS %q, using default", raw)
		}
	}

	ctx := context.Background()
	pool, err := db.NewPool(ctx, databaseURL)
	if err != nil {
		log.Fatalf("media-api: failed to connect to postgres: %v", err)
	}
	defer pool.Close()

	store := media.NewPGStore(pool)

	// EVENT_BUS_BACKEND mirrors cmd/gateway's split: "local" keeps
	// db.LocalEventStore for docker-compose, "pubsub" publishes
	// media_uploaded to the real "media-analysis-events" topic that
	// image-analysis-worker's push subscription consumes.
	eventBusBackend := os.Getenv("EVENT_BUS_BACKEND")
	if eventBusBackend == "" {
		eventBusBackend = "local"
	}

	var events media.EventPublisher
	switch eventBusBackend {
	case "local":
		events = db.NewLocalEventStore(pool)
	case "pubsub":
		projectID := os.Getenv("GCP_PROJECT")
		publisher, err := pubsub.NewPublisher(ctx, projectID)
		if err != nil {
			log.Fatalf("media-api: initialize pubsub publisher: %v", err)
		}
		events = publisher
	default:
		log.Fatalf("media-api: unknown EVENT_BUS_BACKEND %q (want local or pubsub)", eventBusBackend)
	}

	mediaStoreBackend := os.Getenv("MEDIA_STORE_BACKEND")
	if mediaStoreBackend == "" {
		mediaStoreBackend = "local"
	}

	var mediaStore media.MediaStore
	switch mediaStoreBackend {
	case "local":
		mediaStore = media.NewLocalMediaStore(localMediaDir, publicBaseURL, signedURLTTL)
	case "gcs":
		bucket := os.Getenv("GCS_MEDIA_BUCKET")
		if bucket == "" {
			log.Fatal("media-api: GCS_MEDIA_BUCKET is required when MEDIA_STORE_BACKEND=gcs")
		}
		gcsStore, err := media.NewGCSMediaStore(ctx, bucket, os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"), signedURLTTL)
		if err != nil {
			log.Fatalf("media-api: failed to create gcs media store: %v", err)
		}
		mediaStore = gcsStore
	default:
		log.Fatalf("media-api: unknown MEDIA_STORE_BACKEND %q (want local or gcs)", mediaStoreBackend)
	}

	handler := media.NewHandler(store, events, mediaStore, localMediaDir)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})
	handler.RegisterRoutes(mux)

	log.Printf("media-api listening on :%s", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatal(err)
	}
}
