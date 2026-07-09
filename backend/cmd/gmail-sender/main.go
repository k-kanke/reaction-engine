package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/k-kanke/reaction-engine/backend/internal/db"
	"github.com/k-kanke/reaction-engine/backend/internal/gmail"
	"github.com/k-kanke/reaction-engine/backend/internal/media"
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

	// GMAIL_SEND_BACKEND mirrors the rest of backend's *_BACKEND flags
	// (EVENT_BUS_BACKEND, MEDIA_STORE_BACKEND, JSONL_STORE_BACKEND):
	// "stub" (default) keeps local dev/docker-compose working with no
	// Google credentials; "real" sends through the Gmail API using OAuth2
	// user credentials minted by cmd/gmail-oauth-setup (see
	// cmd/gmail-sender/README.md).
	sendBackend := os.Getenv("GMAIL_SEND_BACKEND")
	if sendBackend == "" {
		sendBackend = "stub"
	}

	var sentAt *time.Time
	var deliveryErr *string

	switch sendBackend {
	case "stub":
		log.Printf("gmail-sender: [STUB] would send %s to %s (report_id=%s)", pdfPath, *recipient, reportID)
		now := time.Now()
		sentAt = &now
	case "real":
		if err := sendReal(ctx, *sessionID, pdfPath, *recipient); err != nil {
			log.Printf("gmail-sender: send failed: %v", err)
			msg := err.Error()
			deliveryErr = &msg
		} else {
			now := time.Now()
			sentAt = &now
		}
	default:
		log.Fatalf("gmail-sender: unknown GMAIL_SEND_BACKEND %q (want stub or real)", sendBackend)
	}

	status := "sent"
	if deliveryErr != nil {
		status = "failed"
	}

	deliveryID, err := store.InsertReportDelivery(ctx, reportID, *recipient, status, sentAt, deliveryErr)
	if err != nil {
		log.Fatalf("gmail-sender: insert report delivery failed: %v", err)
	}

	if deliveryErr != nil {
		log.Fatalf("gmail-sender: delivery recorded delivery_id=%s report_id=%s recipient=%s status=failed error=%s", deliveryID, reportID, *recipient, *deliveryErr)
	}
	log.Printf("gmail-sender: delivery recorded delivery_id=%s report_id=%s recipient=%s status=sent", deliveryID, reportID, *recipient)
}

// sendReal fetches the report PDF bytes and sends it through the real
// Gmail API. mediaReader construction mirrors cmd/pdf-renderer's
// MEDIA_STORE_BACKEND local/gcs split (pdfPath is a media_ref the same
// pdf-renderer wrote, so the same backend selection must be able to read
// it back).
func sendReal(ctx context.Context, sessionID, pdfPath, recipient string) error {
	clientID := os.Getenv("GMAIL_OAUTH_CLIENT_ID")
	clientSecret := os.Getenv("GMAIL_OAUTH_CLIENT_SECRET")
	refreshToken := os.Getenv("GMAIL_OAUTH_REFRESH_TOKEN")
	from := os.Getenv("GMAIL_SENDER_FROM")
	if clientID == "" || clientSecret == "" || refreshToken == "" || from == "" {
		return fmt.Errorf("GMAIL_OAUTH_CLIENT_ID, GMAIL_OAUTH_CLIENT_SECRET, GMAIL_OAUTH_REFRESH_TOKEN, and GMAIL_SENDER_FROM are all required when GMAIL_SEND_BACKEND=real")
	}

	localMediaDir := os.Getenv("LOCAL_MEDIA_DIR")
	if localMediaDir == "" {
		localMediaDir = "./tmp/media"
	}

	mediaStoreBackend := os.Getenv("MEDIA_STORE_BACKEND")
	if mediaStoreBackend == "" {
		mediaStoreBackend = "local"
	}

	var mediaReader media.MediaReader
	switch mediaStoreBackend {
	case "local":
		mediaReader = media.NewLocalMediaStore(localMediaDir, "", 0)
	case "gcs":
		bucket := os.Getenv("GCS_MEDIA_BUCKET")
		if bucket == "" {
			return fmt.Errorf("GCS_MEDIA_BUCKET is required when MEDIA_STORE_BACKEND=gcs")
		}
		gcsStore, err := media.NewGCSMediaStore(ctx, bucket, os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"), 0)
		if err != nil {
			return fmt.Errorf("create gcs media store: %w", err)
		}
		mediaReader = gcsStore
	default:
		return fmt.Errorf("unknown MEDIA_STORE_BACKEND %q (want local or gcs)", mediaStoreBackend)
	}

	pdfBytes, err := mediaReader.Read(ctx, pdfPath)
	if err != nil {
		return fmt.Errorf("read report pdf %q: %w", pdfPath, err)
	}

	sender, err := gmail.NewGmailSender(ctx, clientID, clientSecret, refreshToken, from)
	if err != nil {
		return fmt.Errorf("new gmail sender: %w", err)
	}

	subject := fmt.Sprintf("Reaction Engine セッションレポート: %s", sessionID)
	body := "セッションのフィードバックレポートを添付します。"
	if err := sender.Send(ctx, recipient, subject, body, pdfBytes, "report.pdf"); err != nil {
		return fmt.Errorf("send: %w", err)
	}

	return nil
}
