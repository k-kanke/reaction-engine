package main

import (
	"context"
	"log"
	"net/http"
	"os"

	"github.com/k-kanke/reaction-engine/backend/internal/db"
	"github.com/k-kanke/reaction-engine/backend/internal/gateway"
	"github.com/k-kanke/reaction-engine/backend/internal/redis"
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

	llmEnabled := os.Getenv("ENABLE_REAL_LLM") == "true"

	redisClient := redis.NewClient(redisAddr)
	defer redisClient.Close()

	pool, err := db.NewPool(context.Background(), databaseURL)
	if err != nil {
		log.Fatalf("gateway: failed to connect to postgres: %v", err)
	}
	defer pool.Close()

	events := db.NewLocalEventStore(pool)
	handler := gateway.NewHandler(redisClient, events, llmEnabled)

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
