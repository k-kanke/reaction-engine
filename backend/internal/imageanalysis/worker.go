package imageanalysis

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/k-kanke/reaction-engine/backend/internal/contract"
)

// Cache is the Redis boundary the worker publishes baseline / visual
// summary state to.
type Cache interface {
	SetBaselineReady(ctx context.Context, sessionID, audienceID string, baseline, visualSummary []byte) error
}

// visualSummaryConfidence is a fixed stub confidence for the fake visual
// summary below; there is no real vision model backing it yet.
const visualSummaryConfidence = 0.5

// fakeVisualSummary is the deterministic, non-LLM visual summary from
// plan/backend-local-docker-runbook.md Phase 8.
var fakeVisualSummary = map[string]string{
	"face_quality":        "usable",
	"lighting":            "unknown",
	"camera_angle":        "unknown",
	"baseline_expression": "unknown",
	"source":              "local_stub",
}

type Worker struct {
	Store    Store
	Cache    Cache
	MediaDir string
}

func NewWorker(store Store, cache Cache, mediaDir string) *Worker {
	return &Worker{Store: store, Cache: cache, MediaDir: mediaDir}
}

// ProcessMediaUploaded implements the Phase 8 pipeline for one
// media_uploaded event: look up the capture snapshot, confirm the local
// media file is reachable, derive a fake visual summary and an updated
// (deterministic, non-LLM) participant baseline, persist both, and cache
// them in Redis with baseline_status "ready".
func (w *Worker) ProcessMediaUploaded(ctx context.Context, payload contract.MediaUploadedEventPayload) error {
	capture, err := w.Store.GetCaptureSnapshot(ctx, payload.SessionID, payload.CaptureID)
	if err != nil {
		return fmt.Errorf("get capture snapshot: %w", err)
	}

	// Confirm the uploaded frame is actually reachable on local disk. The
	// fake visual summary below doesn't read its bytes; a real vision
	// model call would replace this step with actual image analysis.
	path := localMediaPath(w.MediaDir, capture.SessionID, capture.CaptureID, capture.MediaRef)
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("stat media file %s: %w", path, err)
	}

	prev, hasPrev, err := w.Store.GetParticipantBaseline(ctx, payload.SessionID, payload.AudienceID)
	if err != nil {
		return fmt.Errorf("get participant baseline: %w", err)
	}

	baseline, sampleCount, baselineConfidence := computeBaseline(prev, hasPrev, capture.FeatureSnapshot)
	if err := w.Store.UpsertParticipantBaseline(ctx, payload.SessionID, payload.AudienceID, baseline, sampleCount, baselineConfidence); err != nil {
		return fmt.Errorf("upsert participant baseline: %w", err)
	}

	visualSummary, err := json.Marshal(fakeVisualSummary)
	if err != nil {
		return fmt.Errorf("marshal visual summary: %w", err)
	}
	if err := w.Store.InsertVisualSummary(ctx, payload.SessionID, payload.AudienceID, payload.CaptureID, capture.MediaRef, visualSummary, visualSummaryConfidence); err != nil {
		return fmt.Errorf("insert visual summary: %w", err)
	}

	if err := w.Cache.SetBaselineReady(ctx, payload.SessionID, payload.AudienceID, baseline, visualSummary); err != nil {
		return fmt.Errorf("cache baseline: %w", err)
	}

	return nil
}

// localMediaPath resolves capture_snapshots.media_ref (a local://
// reference, e.g. local://sessions/{session_id}/baseline/frames/{capture_id}.webp)
// to the on-disk path media-api's /local-upload endpoint actually wrote it
// to: {mediaDir}/sessions/{session_id}/{capture_id}{ext}. The two paths
// differ (media_ref carries an extra /baseline/frames/ segment not used on
// disk); this reproduces media-api's own resolution instead of parsing
// media_ref as a literal path.
func localMediaPath(mediaDir, sessionID, captureID, mediaRef string) string {
	ext := filepath.Ext(mediaRef)
	return filepath.Join(mediaDir, "sessions", sessionID, captureID+ext)
}

type compactFeatureSnapshot struct {
	AttentionScore float64 `json:"attention_score"`
}

type baselineFields struct {
	AttentionScoreAvg float64 `json:"attention_score_avg"`
	Source            string  `json:"source"`
}

// computeBaseline derives a deterministic running-average baseline from
// feature_snapshot.attention_score. It is intentionally simple (no LLM /
// vision model): a placeholder until real image-analysis-derived baseline
// signals land. Confidence scales linearly with sample_count up to 8
// samples (matching the shape of architecture.md's example), capped at 1.
func computeBaseline(prev ParticipantBaseline, hasPrev bool, featureSnapshot json.RawMessage) (baseline json.RawMessage, sampleCount int, confidence float64) {
	var fs compactFeatureSnapshot
	_ = json.Unmarshal(featureSnapshot, &fs) // best-effort; missing fields default to zero

	var prevAvg float64
	sampleCount = 1
	if hasPrev {
		var b baselineFields
		_ = json.Unmarshal(prev.Baseline, &b)
		prevAvg = b.AttentionScoreAvg
		sampleCount = prev.SampleCount + 1
	}

	newAvg := prevAvg + (fs.AttentionScore-prevAvg)/float64(sampleCount)

	confidence = float64(sampleCount) / 8.0
	if confidence > 1 {
		confidence = 1
	}

	body, _ := json.Marshal(baselineFields{
		AttentionScoreAvg: newAvg,
		Source:            "local_stub",
	})
	return body, sampleCount, confidence
}
