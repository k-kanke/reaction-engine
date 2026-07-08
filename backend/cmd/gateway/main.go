package main

import (
	"context"
	"log"
	"net/http"
	"os"

	"github.com/k-kanke/reaction-engine/backend/internal/db"
	"github.com/k-kanke/reaction-engine/backend/internal/gateway"
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

	events := db.NewLocalEventStore(pool)

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

	handler := gateway.NewHandlerWithSTT(redisClient, events, generator, recognizer, sttLanguageCode)

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
