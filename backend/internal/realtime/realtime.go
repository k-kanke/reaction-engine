// Package realtime implements the Realtime Worker responsibilities
// architecture.md's WebSocket Gateway / Realtime Worker section assigns:
// building a 30s mood_wave_window + transcript_window from Redis, gating
// on cooldown, assembling an LLM evidence pack (baseline/evidence frame
// refs included), and producing the feedback_event — LLM stub when
// enabled, deterministic rule fallback otherwise or on LLM timeout/error.
//
// It never recomputes raw features (face/gaze/motion/nod/VAD, mood
// composition): that stays on the Chrome side per architecture.md. This
// package only operates on the mood_wave_sample time series the gateway
// (internal/gateway) has already ingested and cached in Redis.
package realtime

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/k-kanke/reaction-engine/backend/internal/contract"
)

// feedbackCooldownMs matches architecture.md's feedback_event.cooldown_ms
// example (30000) and is reported back to Chrome as-is, so the value the
// gateway used to gate the *next* trigger is visible to the client too.
const feedbackCooldownMs = 30000

// realtimeLLMTimeout mirrors REALTIME_LLM_TIMEOUT_MS in
// backend/.env.example — the same budget internal/gateway used to enforce
// before Step 4/5 of plan/mood-wave-contract-migration.md moved decision
// logic here.
const realtimeLLMTimeout = 1500 * time.Millisecond

const (
	llmStubModelVersion = "llm-stub-v1"
	llmStubConfidence   = 0.6
	ruleConfidence      = 0.5
)

// WaveStore is the Redis boundary this package needs. *internal/redis.Client
// satisfies it structurally; tests can supply a fake.
type WaveStore interface {
	GetRecentMoodWaveSamples(ctx context.Context, sessionID string, sinceTMs int64) ([]contract.MoodWaveSampleMessage, error)
	GetTranscriptWindow(ctx context.Context, sessionID, speaker string, sinceTMs int64) ([]contract.TranscriptChunk, error)
	ListReadyBaselineMediaRefs(ctx context.Context, sessionID string) ([]string, error)
	InFeedbackCooldown(ctx context.Context, sessionID string) (bool, error)
	SetFeedbackCooldown(ctx context.Context, sessionID string, cooldownMs int) error
}

// BaselineFrameRef and EvidenceFrameRefOut are architecture.md's
// evidence_pack.baseline_frames[]/evidence_frames[] entry shapes.
type BaselineFrameRef struct {
	MediaRef string `json:"media_ref"`
}

type EvidenceFrameRefOut struct {
	MediaRef string `json:"media_ref"`
	TMs      int64  `json:"t_ms"`
}

// EvidencePack is architecture.md's リアルタイムFBフロー realtime LLM input
// (the "purpose": "realtime_feedback" JSON example). Step 14 of
// plan/backend-local-docker-runbook.md passes this to a real Gemini Flash
// call; today generateLLMStubCandidate only reads Window/MoodWave/
// TranscriptWindow to build a deterministic message.
type EvidencePack struct {
	Purpose          string                     `json:"purpose"`
	SessionID        string                     `json:"session_id"`
	Window           WindowMeta                 `json:"window"`
	MoodWave         MoodWave                   `json:"mood_wave"`
	TranscriptWindow []contract.TranscriptChunk `json:"transcript_window"`
	BaselineFrames   []BaselineFrameRef         `json:"baseline_frames"`
	EvidenceFrames   []EvidenceFrameRefOut      `json:"evidence_frames"`
}

// FeedbackGenerator is the optional realtime LLM boundary. Implementations
// receive the fully assembled evidence pack and either return an LLM-backed
// feedback_event candidate or an error, in which case HandleTrigger keeps
// the deterministic rule fallback.
type FeedbackGenerator interface {
	GenerateFeedback(ctx context.Context, sessionID string, tMs int64, trigger contract.TriggerInfo, pack EvidencePack) (contract.FeedbackEvent, error)
}

// HandleTrigger implements architecture.md's steps 9-14 of リアルタイムFB
// フロー for one trigger-carrying mood_wave_sample: check cooldown, build
// the evidence pack, decide feedback (LLM stub when llmEnabled, rule
// fallback otherwise or on timeout/error), and start the next cooldown
// window. accepted is false (with a zero FeedbackEvent and nil error) when
// the session is still within its cooldown — the caller should not emit
// anything to Chrome in that case. A non-nil error means a Redis read
// failed; the caller should log it and, likewise, not emit feedback for
// this trigger rather than emit one built from partial data.
func HandleTrigger(ctx context.Context, store WaveStore, msg contract.MoodWaveSampleMessage, llmEnabled bool) (feedback contract.FeedbackEvent, accepted bool, err error) {
	var generator FeedbackGenerator
	if llmEnabled {
		generator = StubFeedbackGenerator{}
	}
	return HandleTriggerWithGenerator(ctx, store, msg, generator)
}

// HandleTriggerWithGenerator is HandleTrigger with an injected LLM
// generator. It is used by Cloud Run wiring to pass a real Vertex AI Gemini
// adapter while tests and local runs can continue using HandleTrigger's
// boolean stub switch.
func HandleTriggerWithGenerator(ctx context.Context, store WaveStore, msg contract.MoodWaveSampleMessage, generator FeedbackGenerator) (feedback contract.FeedbackEvent, accepted bool, err error) {
	if msg.Trigger == nil {
		return contract.FeedbackEvent{}, false, fmt.Errorf("realtime: HandleTrigger called without a trigger")
	}
	trigger := *msg.Trigger
	sessionID := msg.SessionID

	inCooldown, err := store.InFeedbackCooldown(ctx, sessionID)
	if err != nil {
		return contract.FeedbackEvent{}, false, fmt.Errorf("check feedback cooldown: %w", err)
	}
	if inCooldown {
		return contract.FeedbackEvent{}, false, nil
	}

	pack, err := buildEvidencePack(ctx, store, sessionID, msg, trigger)
	if err != nil {
		return contract.FeedbackEvent{}, false, err
	}

	feedback = ruleFallback(sessionID, msg.TMs, trigger, pack.MoodWave)

	if generator != nil {
		llmCtx, cancel := context.WithTimeout(ctx, realtimeLLMTimeout)
		candidate, llmErr := generator.GenerateFeedback(llmCtx, sessionID, msg.TMs, trigger, pack)
		cancel()
		if llmErr == nil {
			feedback = candidate
		}
	}

	if cooldownErr := store.SetFeedbackCooldown(ctx, sessionID, feedback.CooldownMs); cooldownErr != nil {
		return feedback, true, fmt.Errorf("set feedback cooldown: %w", cooldownErr)
	}

	return feedback, true, nil
}

// buildEvidencePack assembles architecture.md's realtime LLM input: the 30s
// mood_wave_window (re-sliced from whatever GetRecentMoodWaveSamples
// returns), the same-window transcript from both speakers merged and
// sorted, and every participant's baseline frame media_ref. evidence_frames
// only ever contains msg's own evidence_frame media_ref when present.
// Chrome does not wait for the image PUT to complete before sending the
// trigger sample, so a still-uploading object may make the real LLM adapter
// fail and fall back to the rule result.
func buildEvidencePack(ctx context.Context, store WaveStore, sessionID string, msg contract.MoodWaveSampleMessage, trigger contract.TriggerInfo) (EvidencePack, error) {
	windowStartTMs := msg.TMs - int64(WindowDurationSec)*1000

	samples, err := store.GetRecentMoodWaveSamples(ctx, sessionID, windowStartTMs)
	if err != nil {
		return EvidencePack{}, fmt.Errorf("get recent mood wave samples: %w", err)
	}
	meta, moodWave := BuildWindow(samples, msg.TMs, WindowDurationSec)
	meta.TriggerTMs = trigger.PeakTMs
	meta.TriggerType = trigger.Type

	selfChunks, err := store.GetTranscriptWindow(ctx, sessionID, "self", windowStartTMs)
	if err != nil {
		return EvidencePack{}, fmt.Errorf("get self transcript window: %w", err)
	}
	otherChunks, err := store.GetTranscriptWindow(ctx, sessionID, "other", windowStartTMs)
	if err != nil {
		return EvidencePack{}, fmt.Errorf("get other transcript window: %w", err)
	}
	transcript := append(selfChunks, otherChunks...)
	sort.Slice(transcript, func(i, j int) bool { return transcript[i].TStartMs < transcript[j].TStartMs })

	baselineRefs, err := store.ListReadyBaselineMediaRefs(ctx, sessionID)
	if err != nil {
		return EvidencePack{}, fmt.Errorf("list ready baseline media refs: %w", err)
	}
	baselineFrames := make([]BaselineFrameRef, 0, len(baselineRefs))
	for _, ref := range baselineRefs {
		baselineFrames = append(baselineFrames, BaselineFrameRef{MediaRef: ref})
	}

	var evidenceFrames []EvidenceFrameRefOut
	if msg.EvidenceFrame != nil && msg.EvidenceFrame.MediaRef != "" {
		evidenceFrames = []EvidenceFrameRefOut{{
			MediaRef: msg.EvidenceFrame.MediaRef,
			TMs:      msg.EvidenceFrame.SnapshotTMs,
		}}
	}

	return EvidencePack{
		Purpose:          "realtime_feedback",
		SessionID:        sessionID,
		Window:           meta,
		MoodWave:         moodWave,
		TranscriptWindow: transcript,
		BaselineFrames:   baselineFrames,
		EvidenceFrames:   evidenceFrames,
	}, nil
}

// ruleFallback is the deterministic, LLM-free feedback decision: it reads
// only mood_wave.summary.overall (and, for "nod" triggers, the trigger
// itself) — matching architecture.md's "LLM が間に合わない場合は rule
// fallback で feedback を作る" and Phase 12's precedent that feedback keeps
// flowing with the LLM disabled or unavailable.
func ruleFallback(sessionID string, tMs int64, trigger contract.TriggerInfo, moodWave MoodWave) contract.FeedbackEvent {
	feedback := contract.FeedbackEvent{
		Type:       "feedback_event",
		SessionID:  sessionID,
		TMs:        tMs,
		TriggerID:  trigger.TriggerID,
		Source:     "rule",
		Confidence: ruleConfidence,
		CooldownMs: feedbackCooldownMs,
	}

	switch moodWave.Summary.Overall {
	case "declined":
		feedback.FeedbackType = "reaction_down_candidate"
		feedback.Severity = "low"
		feedback.Message = "直近30秒で反応が下がっている可能性があります。ここで一度確認を挟むとよさそうです。"
		feedback.ReasonCodes = []string{"mood_wave_drop"}
	case "improved":
		feedback.FeedbackType = "reaction_up_candidate"
		feedback.Severity = "info"
		feedback.Message = "直近30秒で反応が上がっています。この調子で進めて問題なさそうです。"
		feedback.ReasonCodes = []string{"mood_wave_rise"}
	default:
		feedback.FeedbackType = "on_track"
		feedback.Severity = "info"
		feedback.Message = "反応は概ね安定しています。"
	}

	if trigger.Type == "nod" {
		feedback.ReasonCodes = append(feedback.ReasonCodes, "nod_trigger")
	}

	return feedback
}

// generateLLMStubCandidate simulates calling Gemini Flash with the
// EvidencePack (architecture.md's realtime LLM path): deterministic, no
// network call. It still runs behind ctx's timeout budget, exercising the
// same fallback path a real timeout/error would take once Phase 14 wires
// an actual adapter in. The only difference from ruleFallback's message
// selection is that this quotes the most recent transcript chunk in the
// window as evidence, when one exists.
type StubFeedbackGenerator struct{}

func (StubFeedbackGenerator) GenerateFeedback(ctx context.Context, sessionID string, tMs int64, trigger contract.TriggerInfo, pack EvidencePack) (contract.FeedbackEvent, error) {
	return generateLLMStubCandidate(ctx, sessionID, tMs, trigger, pack)
}

func generateLLMStubCandidate(ctx context.Context, sessionID string, tMs int64, trigger contract.TriggerInfo, pack EvidencePack) (contract.FeedbackEvent, error) {
	select {
	case <-ctx.Done():
		return contract.FeedbackEvent{}, ctx.Err()
	default:
	}

	feedback := ruleFallback(sessionID, tMs, trigger, pack.MoodWave)
	feedback.Source = "llm_stub"
	feedback.ModelVersion = llmStubModelVersion
	feedback.Confidence = llmStubConfidence

	if quote := latestTranscriptText(pack.TranscriptWindow); quote != "" {
		feedback.EvidenceQuote = &quote
	}

	return feedback, nil
}

// latestTranscriptText returns the text of the chunk with the highest
// t_start_ms in chunks, or "" if chunks is empty.
func latestTranscriptText(chunks []contract.TranscriptChunk) string {
	if len(chunks) == 0 {
		return ""
	}
	latest := chunks[0]
	for _, c := range chunks[1:] {
		if c.TStartMs > latest.TStartMs {
			latest = c
		}
	}
	return latest.Text
}
