package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/k-kanke/reaction-engine/backend/internal/db"
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

	dir := filepath.Join(localMediaDir, "sessions", *sessionID, "reports")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Fatalf("pdf-renderer: prepare storage dir failed: %v", err)
	}
	pdfPath := filepath.Join(dir, "report.pdf")
	if err := os.WriteFile(pdfPath, pdfBytes, 0o644); err != nil {
		log.Fatalf("pdf-renderer: write pdf failed: %v", err)
	}

	// local:// scheme mirrors internal/media's media_ref convention; in
	// Cloud Run this becomes a gs:// object path instead (see
	// plan/gcp-adapter-migration-phase14.md).
	mediaRef := fmt.Sprintf("local://sessions/%s/reports/report.pdf", *sessionID)
	generatedAt := time.Now()
	if err := store.UpdateReportPDF(ctx, reportID, mediaRef, generatedAt); err != nil {
		log.Fatalf("pdf-renderer: update report pdf path failed: %v", err)
	}

	log.Printf("pdf-renderer: rendered report_id=%s session_id=%s media_ref=%s bytes=%d", reportID, *sessionID, mediaRef, len(pdfBytes))
}

// reportToLines flattens a postsession.Report into the plain-text lines
// internal/pdf.Render expects. Free-text fields (transcript_summary,
// feedback message) may contain Japanese; pdf.Render's sanitizeLine keeps
// the ASCII structure and flags what it dropped rather than corrupting the
// PDF — see internal/pdf's doc comment.
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
