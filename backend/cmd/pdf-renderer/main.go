package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
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

// reportToLines flattens a postsession.Report into Markdown-ish lines
// internal/pdf.Render understands ("## "/"### " headings, "- " bullets,
// blank lines as paragraph spacing -- see internal/pdf's doc comment).
// Free-text fields (WaveOverview.Overall, transcript_summary, feedback
// message) routinely contain Japanese; pdf.Render embeds a CJK font and
// word-wraps each line, so these render as-is.
func reportToLines(r postsession.Report) []string {
	var lines []string

	lines = append(lines,
		fmt.Sprintf("Session: %s", r.Session.SessionID),
		fmt.Sprintf("Duration: %.1f min", r.Session.DurationMin),
		fmt.Sprintf("Generated: %s", r.GeneratedAt),
		fmt.Sprintf("Source: %s", r.Source),
		"",
	)

	// WaveOverview.Overall is either the LLM's Markdown-formatted overall
	// feedback (cmd/post-session-job's ENABLE_REPORT_LLM path -- already
	// starts with its own "## " heading, possibly multi-line) or, on
	// LLM failure/timeout, postsession.describeOverall()'s single-line
	// deterministic fallback (no heading of its own, hence the "## 総評"
	// fallback heading below so the section still reads sensibly either
	// way).
	if !strings.HasPrefix(strings.TrimSpace(r.WaveOverview.Overall), "#") {
		lines = append(lines, "## 総評")
	}
	lines = append(lines, strings.Split(r.WaveOverview.Overall, "\n")...)

	if len(r.WaveOverview.DropSections) > 0 || len(r.WaveOverview.PeakPositiveSections) > 0 {
		lines = append(lines, "", "### 区間データ")
		for _, s := range r.WaveOverview.DropSections {
			lines = append(lines, fmt.Sprintf("- 低下区間: %s", s))
		}
		for _, s := range r.WaveOverview.PeakPositiveSections {
			lines = append(lines, fmt.Sprintf("- 上昇区間: %s", s))
		}
	}

	lines = append(lines, "", fmt.Sprintf("## 重要な瞬間 (%d件)", len(r.ImportantWindows)))
	for i, w := range r.ImportantWindows {
		lines = append(lines, fmt.Sprintf("- [%d] %s - %s (%s, slope_per_sec=%.4f)", i+1, w.Start, w.End, w.MoodWaveSummary.Overall, w.MoodWaveSummary.SlopePerSec))
		if w.TranscriptSummary != "" {
			lines = append(lines, fmt.Sprintf("  - 発言: %s", w.TranscriptSummary))
		}
		if len(w.EvidenceRefs) > 0 {
			lines = append(lines, fmt.Sprintf("  - 証拠画像: %d件", len(w.EvidenceRefs)))
		}
	}

	lines = append(lines, "", fmt.Sprintf("## リアルタイムフィードバック履歴 (%d件)", len(r.RealtimeFeedbackHistory)))
	for _, f := range r.RealtimeFeedbackHistory {
		lines = append(lines, fmt.Sprintf("- t_ms=%d type=%s severity=%s source=%s", f.TMs, f.FeedbackType, f.Severity, f.Source))
	}

	lines = append(lines, "", fmt.Sprintf("## 参加者 (%d名)", len(r.BaselineContext.Participants)))
	for _, p := range r.BaselineContext.Participants {
		lines = append(lines, fmt.Sprintf("- %s (baseline_confidence=%.2f)", p.AudienceID, p.Confidence))
	}

	return lines
}
