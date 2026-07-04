package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/k-kanke/reaction-engine/backend/internal/db"
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

	ctx := context.Background()

	pool, err := db.NewPool(ctx, databaseURL)
	if err != nil {
		log.Fatalf("writer: failed to connect to postgres: %v", err)
	}
	defer pool.Close()

	events := db.NewLocalEventStore(pool)

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
			// Phase 5 stub: only proves the local event bus can be read.
			// Writing compact raw features to JSONL / Cloud SQL and acking
			// lands in Phase 6 (Durable Writer MVP).
			log.Printf("writer: unacked event id=%d event_id=%s topic=%s", e.ID, e.EventID, e.Topic)
		}
	}
}
