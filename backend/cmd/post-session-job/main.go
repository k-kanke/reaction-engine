package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"os"
	"time"

	"github.com/k-kanke/reaction-engine/backend/internal/db"
	"github.com/k-kanke/reaction-engine/backend/internal/postsession"
	"github.com/k-kanke/reaction-engine/backend/internal/writer"
)

func main() {
	sessionID := flag.String("session-id", "", "session_id to generate a post-session report for")
	flag.Parse()

	if *sessionID == "" {
		log.Fatal("post-session-job: --session-id is required")
	}

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
		log.Fatalf("post-session-job: failed to connect to postgres: %v", err)
	}
	defer pool.Close()

	store := postsession.NewPGStore(pool)

	compactFeatures, err := writer.ReadCompactRawFeatures(jsonlDir, *sessionID)
	if err != nil {
		log.Fatalf("post-session-job: read compact raw features failed: %v", err)
	}

	transcripts, err := store.ListTranscripts(ctx, *sessionID)
	if err != nil {
		log.Fatalf("post-session-job: list transcripts failed: %v", err)
	}

	baselines, err := store.ListParticipantBaselines(ctx, *sessionID)
	if err != nil {
		log.Fatalf("post-session-job: list participant baselines failed: %v", err)
	}

	visualSummaries, err := store.ListVisualSummaries(ctx, *sessionID)
	if err != nil {
		log.Fatalf("post-session-job: list visual summaries failed: %v", err)
	}

	signalSummaries, err := store.ListSignalSummaries(ctx, *sessionID)
	if err != nil {
		log.Fatalf("post-session-job: list signal summaries failed: %v", err)
	}

	report := postsession.BuildReport(*sessionID, compactFeatures, transcripts, baselines, visualSummaries, signalSummaries, time.Now())

	reportJSON, err := json.Marshal(report)
	if err != nil {
		log.Fatalf("post-session-job: marshal report failed: %v", err)
	}

	if err := store.EnsureSession(ctx, *sessionID); err != nil {
		log.Fatalf("post-session-job: ensure session failed: %v", err)
	}

	reportID, err := store.InsertReport(ctx, *sessionID, reportJSON, time.Now())
	if err != nil {
		log.Fatalf("post-session-job: insert report failed: %v", err)
	}

	log.Printf("post-session-job: report generated for session_id=%s report_id=%s participants=%d", *sessionID, reportID, len(report.Participants))
}
