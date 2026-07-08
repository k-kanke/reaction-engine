package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/k-kanke/reaction-engine/backend/internal/db"
	"github.com/k-kanke/reaction-engine/backend/internal/media"
	"github.com/k-kanke/reaction-engine/backend/internal/pdf"
	"github.com/k-kanke/reaction-engine/backend/internal/postsession"
)

func main() {
	sessionID := flag.String("session-id", "", "session_id whose latest report to render as PDF")
	flag.Parse()

	if *sessionID == "" {
		log.Fatal("pdf-renderer: --session-id is required")
	}

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		databaseURL = "postgres://reaction:reaction@localhost:5432/reaction?sslmode=disable"
	}

	localMediaDir := os.Getenv("LOCAL_MEDIA_DIR")
	if localMediaDir == "" {
		localMediaDir = "./tmp/media"
	}

	ctx := context.Background()

	pool, err := db.NewPool(ctx, databaseURL)
	if err != nil {
		log.Fatalf("pdf-renderer: failed to connect to postgres: %v", err)
	}
	defer pool.Close()

	store := postsession.NewPGStore(pool)

	// MEDIA_STORE_BACKEND mirrors media-api/image-analysis-worker's flag
	// (Step F of plan/gcp-deployment-runbook.md): report.pdf now goes
	// through internal/media's existing Local/GCS split instead of the
	// os.WriteFile this used before, which always wrote to local disk even
	// when deployed as a separate Cloud Run instance from whatever reads
	// report.pdf back.
	mediaStoreBackend := os.Getenv("MEDIA_STORE_BACKEND")
	if mediaStoreBackend == "" {
		mediaStoreBackend = "local"
	}

	var mediaWriter media.MediaWriter
	switch mediaStoreBackend {
	case "local":
		mediaWriter = media.NewLocalMediaStore(localMediaDir, "", 0)
	case "gcs":
		bucket := os.Getenv("GCS_MEDIA_BUCKET")
		if bucket == "" {
			log.Fatal("pdf-renderer: GCS_MEDIA_BUCKET is required when MEDIA_STORE_BACKEND=gcs")
		}
		gcsStore, err := media.NewGCSMediaStore(ctx, bucket, os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"), 0)
		if err != nil {
			log.Fatalf("pdf-renderer: failed to create gcs media store: %v", err)
		}
		mediaWriter = gcsStore
	default:
		log.Fatalf("pdf-renderer: unknown MEDIA_STORE_BACKEND %q (want local or gcs)", mediaStoreBackend)
	}

	reportID, reportJSON, err := store.GetLatestReport(ctx, *sessionID)
	if err != nil {
		log.Fatalf("pdf-renderer: get latest report failed: %v", err)
	}

	var report postsession.Report
	if err := json.Unmarshal(reportJSON, &report); err != nil {
		log.Fatalf("pdf-renderer: unmarshal report failed: %v", err)
	}

	title := fmt.Sprintf("Reaction Engine Session Report: %s", report.Session.SessionID)
	pdfBytes, err := pdf.Render(title, reportToLines(report))
	if err != nil {
		log.Fatalf("pdf-renderer: render pdf failed: %v", err)
	}

	mediaRef, err := mediaWriter.Write(ctx, *sessionID, []string{"reports", "report.pdf"}, pdfBytes, "application/pdf")
	if err != nil {
		log.Fatalf("pdf-renderer: write pdf failed: %v", err)
	}

	generatedAt := time.Now()
	if err := store.UpdateReportPDF(ctx, reportID, mediaRef, generatedAt); err != nil {
		log.Fatalf("pdf-renderer: update report pdf path failed: %v", err)
	}

	log.Printf("pdf-renderer: rendered report_id=%s session_id=%s media_ref=%s bytes=%d", reportID, *sessionID, mediaRef, len(pdfBytes))
}

// reportToLines flattens a postsession.Report into the plain-text lines
// internal/pdf.Render expects. Free-text fields (transcript_summary,
// feedback message) routinely contain Japanese; pdf.Render embeds a CJK
// font and word-wraps each line, so these render as-is — see internal/pdf's
// doc comment.
func reportToLines(r postsession.Report) []string {
	var lines []string

	lines = append(lines,
		fmt.Sprintf("Session: %s", r.Session.SessionID),
		fmt.Sprintf("Duration: %.1f min", r.Session.DurationMin),
		fmt.Sprintf("Generated: %s", r.GeneratedAt),
		fmt.Sprintf("Source: %s", r.Source),
		"",
		fmt.Sprintf("Wave overview: %s", r.WaveOverview.Overall),
	)
	for _, s := range r.WaveOverview.DropSections {
		lines = append(lines, fmt.Sprintf("  drop:  %s", s))
	}
	for _, s := range r.WaveOverview.PeakPositiveSections {
		lines = append(lines, fmt.Sprintf("  peak:  %s", s))
	}

	lines = append(lines, "", fmt.Sprintf("Important windows: %d", len(r.ImportantWindows)))
	for i, w := range r.ImportantWindows {
		lines = append(lines, fmt.Sprintf("  [%d] %s - %s (%s, slope_per_sec=%.4f)", i+1, w.Start, w.End, w.MoodWaveSummary.Overall, w.MoodWaveSummary.SlopePerSec))
		if w.TranscriptSummary != "" {
			lines = append(lines, fmt.Sprintf("      transcript: %s", w.TranscriptSummary))
		}
		if len(w.EvidenceRefs) > 0 {
			lines = append(lines, fmt.Sprintf("      evidence_frames: %d", len(w.EvidenceRefs)))
		}
	}

	lines = append(lines, "", fmt.Sprintf("Realtime feedback events: %d", len(r.RealtimeFeedbackHistory)))
	for _, f := range r.RealtimeFeedbackHistory {
		lines = append(lines, fmt.Sprintf("  t_ms=%d type=%s severity=%s source=%s", f.TMs, f.FeedbackType, f.Severity, f.Source))
	}

	lines = append(lines, "", fmt.Sprintf("Participants: %d", len(r.BaselineContext.Participants)))
	for _, p := range r.BaselineContext.Participants {
		lines = append(lines, fmt.Sprintf("  %s (baseline_confidence=%.2f)", p.AudienceID, p.Confidence))
	}

	return lines
}
