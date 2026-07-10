package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/k-kanke/reaction-engine/backend/internal/db"
	"github.com/k-kanke/reaction-engine/backend/internal/gateway"
	"github.com/k-kanke/reaction-engine/backend/internal/postsessiontrigger"
	"github.com/k-kanke/reaction-engine/backend/internal/pubsub"
	"github.com/k-kanke/reaction-engine/backend/internal/realtime"
	"github.com/k-kanke/reaction-engine/backend/internal/redis"
	"github.com/k-kanke/reaction-engine/backend/internal/speech"
)

func main() {
	port := os.Getenv("GATEWAY_PORT")
	if port == "" {
		port = "8080"
	}

	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "localhost:6379"
	}

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		databaseURL = "postgres://reaction:reaction@localhost:5432/reaction?sslmode=disable"
	}

	redisClient := redis.NewClient(redisAddr)
	defer redisClient.Close()

	pool, err := db.NewPool(context.Background(), databaseURL)
	if err != nil {
		log.Fatalf("gateway: failed to connect to postgres: %v", err)
	}
	defer pool.Close()

	// EVENT_BUS_BACKEND mirrors JSONL_STORE_BACKEND/MEDIA_STORE_BACKEND's
	// local/gcs split: "local" keeps the pre-Pub/Sub db.LocalEventStore
	// stand-in (docker-compose, no live GCP project needed), "pubsub"
	// publishes to the real "feature-events" topic that writer's push
	// subscription consumes (plan/post-session-report-implementation.md's
	// Pub/Sub migration).
	eventBusBackend := os.Getenv("EVENT_BUS_BACKEND")
	if eventBusBackend == "" {
		eventBusBackend = "local"
	}

	var events gateway.EventPublisher
	switch eventBusBackend {
	case "local":
		events = db.NewLocalEventStore(pool)
	case "pubsub":
		projectID := firstNonEmpty(os.Getenv("GCP_PROJECT"), os.Getenv("GOOGLE_CLOUD_PROJECT"))
		publisher, err := pubsub.NewPublisher(context.Background(), projectID)
		if err != nil {
			log.Fatalf("gateway: initialize pubsub publisher: %v", err)
		}
		events = publisher
	default:
		log.Fatalf("gateway: unknown EVENT_BUS_BACKEND %q (want local or pubsub)", eventBusBackend)
	}

	var generator realtime.FeedbackGenerator
	if os.Getenv("ENABLE_REAL_LLM") == "true" {
		projectID := firstNonEmpty(os.Getenv("VERTEX_PROJECT"), os.Getenv("GOOGLE_CLOUD_PROJECT"))
		location := firstNonEmpty(os.Getenv("VERTEX_LOCATION"), os.Getenv("GCP_REGION"), "asia-northeast1")
		model := firstNonEmpty(os.Getenv("VERTEX_REALTIME_MODEL"), "gemini-2.5-flash")
		vertexGenerator, err := realtime.NewVertexFeedbackGenerator(context.Background(), projectID, location, model)
		if err != nil {
			log.Fatalf("gateway: initialize realtime vertex generator: %v", err)
		}
		generator = vertexGenerator
	}

	realtime.DebugLogEvidencePack = os.Getenv("REALTIME_DEBUG_LOG") == "true"

	var recognizer speech.Recognizer
	sttLanguageCode := firstNonEmpty(os.Getenv("STT_LANGUAGE_CODE"), "ja-JP")
	if os.Getenv("ENABLE_REAL_STT") == "true" {
		googleRecognizer, err := speech.NewGoogleRecognizer(context.Background(), os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"))
		if err != nil {
			log.Fatalf("gateway: initialize speech recognizer: %v", err)
		}
		recognizer = googleRecognizer
	}

	var postSessionTrigger postsessiontrigger.Trigger
	if os.Getenv("ENABLE_POST_SESSION_JOBS") == "true" {
		projectID := firstNonEmpty(os.Getenv("GCP_PROJECT"), os.Getenv("GOOGLE_CLOUD_PROJECT"))
		region := firstNonEmpty(os.Getenv("GCP_REGION"), "asia-northeast1")
		cloudRunTrigger, err := postsessiontrigger.NewCloudRunTrigger(context.Background(), projectID, region)
		if err != nil {
			log.Fatalf("gateway: initialize post-session trigger: %v", err)
		}
		postSessionTrigger = cloudRunTrigger
	}

	handler := gateway.NewHandlerWithPostSessionTrigger(redisClient, events, generator, recognizer, sttLanguageCode, postSessionTrigger)

	if interval := os.Getenv("PERIODIC_FEEDBACK_INTERVAL"); interval != "" {
		d, err := time.ParseDuration(interval)
		if err != nil {
			log.Fatalf("gateway: invalid PERIODIC_FEEDBACK_INTERVAL %q: %v", interval, err)
		}
		handler.SetPeriodicFeedbackInterval(d)
		log.Printf("gateway: periodic feedback interval set to %v", d)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("/ws", handler.ServeWS)

	log.Printf("gateway listening on :%s", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatal(err)
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
