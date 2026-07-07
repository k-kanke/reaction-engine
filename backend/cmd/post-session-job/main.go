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

	moodWaveSamples, err := writer.ReadMoodWaveSamples(jsonlDir, *sessionID)
	if err != nil {
		log.Fatalf("post-session-job: read mood wave samples failed: %v", err)
	}

	triggerEvents, err := store.ListTriggerEvents(ctx, *sessionID)
	if err != nil {
		log.Fatalf("post-session-job: list trigger events failed: %v", err)
	}

	feedbackEvents, err := store.ListFeedbackEvents(ctx, *sessionID)
	if err != nil {
		log.Fatalf("post-session-job: list feedback events failed: %v", err)
	}

	transcripts, err := store.ListTranscripts(ctx, *sessionID)
	if err != nil {
		log.Fatalf("post-session-job: list transcripts failed: %v", err)
	}

	evidenceRefs, err := store.ListEvidenceMediaRefs(ctx, *sessionID)
	if err != nil {
		log.Fatalf("post-session-job: list evidence media refs failed: %v", err)
	}

	baselines, err := store.ListParticipantBaselines(ctx, *sessionID)
	if err != nil {
		log.Fatalf("post-session-job: list participant baselines failed: %v", err)
	}

	visualSummaries, err := store.ListVisualSummaries(ctx, *sessionID)
	if err != nil {
		log.Fatalf("post-session-job: list visual summaries failed: %v", err)
	}

	report := postsession.BuildReport(*sessionID, moodWaveSamples, triggerEvents, feedbackEvents, transcripts, evidenceRefs, baselines, visualSummaries, time.Now())

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

	log.Printf("post-session-job: report generated for session_id=%s report_id=%s important_windows=%d", *sessionID, reportID, len(report.ImportantWindows))
}
