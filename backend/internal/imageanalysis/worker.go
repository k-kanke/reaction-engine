package imageanalysis

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/k-kanke/reaction-engine/backend/internal/contract"
	"github.com/k-kanke/reaction-engine/backend/internal/media"
)

// Cache is the Redis boundary the worker publishes baseline / visual
// summary state to.
type Cache interface {
	SetBaselineReady(ctx context.Context, sessionID, audienceID string, baseline, visualSummary []byte, mediaRef string) error
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
	Store      Store
	Cache      Cache
	MediaStore media.MediaReader
}

func NewWorker(store Store, cache Cache, mediaStore media.MediaReader) *Worker {
	return &Worker{Store: store, Cache: cache, MediaStore: mediaStore}
}

// ProcessMediaUploaded implements the Phase 8 pipeline for one
// media_uploaded event: look up the capture snapshot, confirm the local
// media file is reachable, derive a fake visual summary and an updated
// (deterministic, non-LLM) participant baseline, persist both, and cache
// them in Redis with baseline_status "ready".
//
// Step 8 of plan/mood-wave-contract-migration.md restricts this to
// purpose=baseline_frame only. architecture.md: "Realtime Worker は
// evidence frame を画像解析 worker に通さない...Image Analysis Worker は
// baseline frame から baseline_visual_profile を作るのが主責務で、evidence
// frame の非同期解析は全体FBや監査で必要になった場合だけ行う". Before this
// guard, an evidence_frame media_uploaded event (now possible since Step 7
// wired Media API to accept them) would have been processed as if it were
// a baseline_frame — overwriting that participant's baseline with a
// feature_snapshot from a totally different capture context (a
// trigger-driven moment, not baseline calibration).
func (w *Worker) ProcessMediaUploaded(ctx context.Context, payload contract.MediaUploadedEventPayload) error {
	if payload.Purpose != "baseline_frame" {
		return nil
	}

	capture, err := w.Store.GetCaptureSnapshot(ctx, payload.SessionID, payload.CaptureID)
	if err != nil {
		return fmt.Errorf("get capture snapshot: %w", err)
	}

	// Confirm the uploaded frame is actually reachable. The fake visual
	// summary below doesn't read its bytes; a real vision model call would
	// replace this step with actual image analysis via MediaStore.Read.
	exists, err := w.MediaStore.Exists(ctx, capture.MediaRef)
	if err != nil {
		return fmt.Errorf("check media exists for ref %s: %w", capture.MediaRef, err)
	}
	if !exists {
		return fmt.Errorf("media not found for ref %s", capture.MediaRef)
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

	if err := w.Cache.SetBaselineReady(ctx, payload.SessionID, payload.AudienceID, baseline, visualSummary, capture.MediaRef); err != nil {
		return fmt.Errorf("cache baseline: %w", err)
	}

	return nil
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
