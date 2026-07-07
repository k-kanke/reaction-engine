package main

import (
	"context"
	"flag"
	"log"
	"os"
	"time"

	"github.com/k-kanke/reaction-engine/backend/internal/db"
	"github.com/k-kanke/reaction-engine/backend/internal/postsession"
)

func main() {
	sessionID := flag.String("session-id", "", "session_id whose report PDF to send")
	recipient := flag.String("to", "", "recipient email address")
	flag.Parse()

	if *sessionID == "" {
		log.Fatal("gmail-sender: --session-id is required")
	}
	if *recipient == "" {
		log.Fatal("gmail-sender: --to is required")
	}

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		databaseURL = "postgres://reaction:reaction@localhost:5432/reaction?sslmode=disable"
	}

	ctx := context.Background()

	pool, err := db.NewPool(ctx, databaseURL)
	if err != nil {
		log.Fatalf("gmail-sender: failed to connect to postgres: %v", err)
	}
	defer pool.Close()

	store := postsession.NewPGStore(pool)

	reportID, pdfPath, err := store.GetReportPDFPath(ctx, *sessionID)
	if err != nil {
		log.Fatalf("gmail-sender: get report pdf path failed: %v", err)
	}
	if pdfPath == "" {
		log.Fatalf("gmail-sender: report_id=%s has no pdf_path yet; run pdf-renderer first", reportID)
	}

	// Local stub: no real Gmail API call. Phase 14 of
	// plan/backend-local-docker-runbook.md wires that in behind the same
	// shape (see cmd/gmail-sender/README.md).
	log.Printf("gmail-sender: [STUB] would send %s to %s (report_id=%s)", pdfPath, *recipient, reportID)

	sentAt := time.Now()
	deliveryID, err := store.InsertReportDelivery(ctx, reportID, *recipient, "sent", &sentAt, nil)
	if err != nil {
		log.Fatalf("gmail-sender: insert report delivery failed: %v", err)
	}

	log.Printf("gmail-sender: delivery recorded delivery_id=%s report_id=%s recipient=%s status=sent", deliveryID, reportID, *recipient)
}
