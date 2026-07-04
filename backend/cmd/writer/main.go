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

			if err := writer.AppendCompactRawFeature(jsonlDir, payload.SessionID, payload); err != nil {
				log.Printf("writer: write compact raw feature failed for event id=%d event_id=%s: %v", e.ID, e.EventID, err)
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
