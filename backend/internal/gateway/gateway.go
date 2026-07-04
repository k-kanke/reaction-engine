package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"

	"github.com/k-kanke/reaction-engine/backend/internal/contract"
	"github.com/k-kanke/reaction-engine/backend/internal/db"
	gwredis "github.com/k-kanke/reaction-engine/backend/internal/redis"
)

const featureEventsTopic = "feature-events"

// transcriptFlushIntervalMs is how much audio_chunk t_ms range accumulates
// per speaker before the stub STT flushes a fake transcript_chunk.
// fakeTranscriptConfidence is a fixed stub confidence; there is no real STT
// model behind it yet (Phase 11 of plan/backend-local-docker-runbook.md).
const (
	transcriptFlushIntervalMs = 5000
	fakeTranscriptConfidence  = 0.6
)

// audioAccumulator tracks one speaker's audio_chunk arrivals within a
// single WebSocket connection so the gateway knows when to flush a stub
// transcript_chunk. It only ever sees t_ms/count — raw PCM is read off the
// wire and discarded, never stored here or anywhere else.
type audioAccumulator struct {
	firstTMs int64
	lastTMs  int64
}

// defaultAttentionThreshold is the fixed threshold used while a
// participant's baseline is still warming_up (no Image Analysis Worker
// output yet). baselineDropMargin is how far below a ready baseline's
// attention_score_avg counts as a drop once a personal baseline is
// available. Both are stand-ins for the real signal_summary / LLM
// decision layer (Phase 12); Phase 9 only wires baseline availability
// into feedback, per plan/backend-local-docker-runbook.md.
const (
	defaultAttentionThreshold = 0.4
	baselineDropMargin        = 0.15
)

// baselineFields is the subset of session:baseline:* JSON
// (internal/imageanalysis's baselineFields) the gateway reads to correct
// feedback.
type baselineFields struct {
	AttentionScoreAvg float64 `json:"attention_score_avg"`
}

// realtimeLLMTimeout is the fixed timeout budget for the (stubbed)
// Realtime LLM call, matching REALTIME_LLM_TIMEOUT_MS in
// backend/.env.example. Phase 12 of plan/backend-local-docker-runbook.md
// doesn't call a real LLM yet — this timeout guard and the error-fallback
// path it enables are scaffolding for the real Gemini Flash call Phase 14
// plugs in later. llmStubModelVersion/llmStubConfidence are fixed stub
// values alongside it.
const (
	realtimeLLMTimeout     = 1500 * time.Millisecond
	llmStubModelVersion    = "llm-stub-v1"
	llmStubConfidence      = 0.6
	ruleDecisionConfidence = 0.5
)

type Handler struct {
	redis      *gwredis.Client
	events     *db.LocalEventStore
	llmEnabled bool
}

func NewHandler(redis *gwredis.Client, events *db.LocalEventStore, llmEnabled bool) *Handler {
	return &Handler{redis: redis, events: events, llmEnabled: llmEnabled}
}

// ServeWS handles GET /ws: it accepts the WebSocket connection, dispatches
// each incoming message by its "type" field, and keeps reading until the
// client disconnects.
func (h *Handler) ServeWS(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		log.Printf("gateway: websocket accept failed: %v", err)
		return
	}
	defer conn.CloseNow()

	ctx := r.Context()
	// One session per connection (architecture.md: "Gateway が session_id
	// ごとに self/other 各1本の Speech-to-Text streaming セッションを維持"),
	// so per-speaker audio accumulators live for the connection's lifetime.
	audioAccumulators := make(map[string]*audioAccumulator)

	for {
		var raw json.RawMessage
		if err := wsjson.Read(ctx, conn, &raw); err != nil {
			if websocket.CloseStatus(err) == -1 {
				log.Printf("gateway: read failed: %v", err)
			}
			return
		}

		var envelope struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			h.writeError(ctx, conn, "invalid json")
			continue
		}

		switch envelope.Type {
		case "realtime_feature":
			h.handleRealtimeFeature(ctx, conn, raw)
		case "audio_chunk":
			h.handleAudioChunk(ctx, raw, audioAccumulators)
		default:
			h.writeError(ctx, conn, "unsupported type: "+envelope.Type)
		}
	}
}

func (h *Handler) handleRealtimeFeature(ctx context.Context, conn *websocket.Conn, raw json.RawMessage) {
	var msg contract.RealtimeFeatureMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		h.writeError(ctx, conn, "invalid realtime_feature payload")
		return
	}

	eventID := "evt_" + uuid.NewString()
	serverReceivedAtMs := time.Now().UnixMilli()

	compactFeatures := make([]contract.CompactFeature, 0, len(msg.Features.FaceTracks))
	for _, track := range msg.Features.FaceTracks {
		feature := contract.CompactFeature{
			EventID:            eventID,
			SessionID:          msg.SessionID,
			AudienceID:         track.AudienceID,
			TMs:                msg.TMs,
			ServerReceivedAtMs: serverReceivedAtMs,
			AttentionScore:     track.AttentionScore,
		}
		compactFeatures = append(compactFeatures, feature)
		if err := h.redis.StoreRecentFeature(ctx, feature); err != nil {
			log.Printf("gateway: store recent feature failed: %v", err)
		}
	}

	// One feedback_event + decision_log per audience_id: baseline-aware
	// correction (Phase 9) and the realtime LLM stub / rule fallback
	// (Phase 12) are both per participant.
	feedbacks := make([]contract.FeedbackEvent, 0, len(compactFeatures))
	decisionLogs := make([]contract.DecisionLog, 0, len(compactFeatures))
	for _, feature := range compactFeatures {
		state, err := h.redis.GetBaselineState(ctx, feature.SessionID, feature.AudienceID)
		if err != nil {
			log.Printf("gateway: get baseline state failed: %v", err)
			state = gwredis.BaselineState{Status: gwredis.BaselineStatusWarmingUp}
		}

		evidenceQuote := ""
		if h.llmEnabled {
			if chunk, ok, err := h.redis.GetLatestTranscript(ctx, feature.SessionID, "self"); err != nil {
				log.Printf("gateway: get latest transcript failed: %v", err)
			} else if ok {
				evidenceQuote = chunk.Text
			}
		}

		feedback, decision := decideFeedback(ctx, feature, state, h.llmEnabled, evidenceQuote)
		feedbacks = append(feedbacks, feedback)
		decisionLogs = append(decisionLogs, decision)
	}

	payload := contract.FeatureEventPayload{
		EventID:            eventID,
		SessionID:          msg.SessionID,
		TMs:                msg.TMs,
		ServerReceivedAtMs: serverReceivedAtMs,
		Features:           compactFeatures,
		DecisionLogs:       decisionLogs,
	}
	if err := h.events.Enqueue(ctx, featureEventsTopic, eventID, payload); err != nil {
		log.Printf("gateway: enqueue local event failed: %v", err)
	}

	for _, feedback := range feedbacks {
		if err := wsjson.Write(ctx, conn, feedback); err != nil {
			log.Printf("gateway: write feedback_event failed: %v", err)
			return
		}
	}
}

// handleAudioChunk implements the Phase 11 STT stub: it never calls a real
// Speech-to-Text service and never persists msg.PCM anywhere. It only
// tracks how much audio_chunk t_ms range has accumulated per speaker, and
// once that reaches transcriptFlushIntervalMs it fabricates one
// deterministic "final" transcript_chunk covering that range, caches it in
// the Redis transcript window, and enqueues it to the same feature-events
// local bus realtime_feature uses so Durable Writer persists it too.
func (h *Handler) handleAudioChunk(ctx context.Context, raw json.RawMessage, accumulators map[string]*audioAccumulator) {
	var msg contract.AudioChunkMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		log.Printf("gateway: invalid audio_chunk payload: %v", err)
		return
	}
	if msg.Speaker == "" {
		log.Printf("gateway: audio_chunk missing speaker")
		return
	}

	acc, ok := accumulators[msg.Speaker]
	if !ok {
		acc = &audioAccumulator{firstTMs: msg.TMs}
		accumulators[msg.Speaker] = acc
	}
	acc.lastTMs = msg.TMs

	if acc.lastTMs-acc.firstTMs < transcriptFlushIntervalMs {
		return
	}

	chunk := buildFakeTranscriptChunk(msg.SessionID, msg.Speaker, acc.firstTMs, acc.lastTMs)
	acc.firstTMs = msg.TMs

	if err := h.redis.StoreRecentTranscript(ctx, chunk); err != nil {
		log.Printf("gateway: store recent transcript failed: %v", err)
	}

	payload := contract.FeatureEventPayload{
		EventID:            chunk.EventID,
		SessionID:          chunk.SessionID,
		TMs:                chunk.TEndMs,
		ServerReceivedAtMs: time.Now().UnixMilli(),
		TranscriptChunks:   []contract.TranscriptChunk{chunk},
	}
	if err := h.events.Enqueue(ctx, featureEventsTopic, chunk.EventID, payload); err != nil {
		log.Printf("gateway: enqueue transcript event failed: %v", err)
	}
}

// buildFakeTranscriptChunk fabricates a deterministic "final" transcript
// covering [tStartMs, tEndMs) for one speaker. Placeholder until Phase 14
// wires a real Speech-to-Text adapter behind the same shape.
func buildFakeTranscriptChunk(sessionID, speaker string, tStartMs, tEndMs int64) contract.TranscriptChunk {
	return contract.TranscriptChunk{
		EventID:       "evt_" + uuid.NewString(),
		Type:          "transcript_chunk",
		SchemaVersion: 1,
		SessionID:     sessionID,
		Speaker:       speaker,
		TStartMs:      tStartMs,
		TEndMs:        tEndMs,
		Text:          fmt.Sprintf("[stub transcript speaker=%s %d-%dms]", speaker, tStartMs, tEndMs),
		Confidence:    fakeTranscriptConfidence,
		IsFinal:       true,
	}
}

// buildFeedback decides a feedback_event for one participant's compact
// feature. If that participant's baseline is ready, attention_score is
// compared against their own attention_score_avg; otherwise (warming_up,
// missing, or unparseable baseline JSON) it falls back to
// defaultAttentionThreshold. Deterministic and LLM-free, matching Phase
// 8's fake visual_summary / baseline stub.
func buildFeedback(feature contract.CompactFeature, state gwredis.BaselineState) contract.FeedbackEvent {
	feedback := contract.FeedbackEvent{
		Type:       "feedback_event",
		SessionID:  feature.SessionID,
		AudienceID: feature.AudienceID,
		TMs:        feature.TMs,
	}

	if state.Status == gwredis.BaselineStatusReady {
		var baseline baselineFields
		if err := json.Unmarshal(state.Baseline, &baseline); err == nil {
			feedback.Source = "rule_baseline"
			if feature.AttentionScore < baseline.AttentionScoreAvg-baselineDropMargin {
				feedback.FeedbackType = "attention_drop"
				feedback.Severity = "warning"
				feedback.Message = "attention below this participant's baseline"
			} else {
				feedback.FeedbackType = "on_track"
				feedback.Severity = "info"
				feedback.Message = "attention within this participant's baseline"
			}
			return feedback
		}
	}

	feedback.Source = "rule_default"
	if feature.AttentionScore < defaultAttentionThreshold {
		feedback.FeedbackType = "attention_drop"
		feedback.Severity = "warning"
		feedback.Message = "attention below default threshold (baseline warming up)"
	} else {
		feedback.FeedbackType = "on_track"
		feedback.Severity = "info"
		feedback.Message = "attention within default threshold (baseline warming up)"
	}
	return feedback
}

// llmCandidate is what a (stubbed) Realtime LLM call returns: a feedback
// candidate plus the evidence it was grounded in, per architecture.md's
// feedback_event "source: llm" shape.
type llmCandidate struct {
	Message       string
	ReasonCodes   []string
	EvidenceQuote string
	ModelVersion  string
	Confidence    float64
}

// generateLLMStubCandidate simulates calling Gemini Flash with
// signal_summary + transcript window (architecture.md's realtime LLM
// path): deterministic, no network call, and no real signal_summary
// layer yet either — feedbackType stands in for it, since that's already
// the richest per-participant signal buildFeedback computes. It still
// runs behind ctx's timeout budget, exercising the same fallback path a
// real timeout/error would take once Phase 14 wires an actual adapter in.
func generateLLMStubCandidate(ctx context.Context, feature contract.CompactFeature, feedbackType, evidenceQuote string) (llmCandidate, error) {
	select {
	case <-ctx.Done():
		return llmCandidate{}, ctx.Err()
	default:
	}

	message := fmt.Sprintf("%s appears on track — no action needed right now.", feature.AudienceID)
	var reasonCodes []string
	if feedbackType == "attention_drop" {
		message = fmt.Sprintf("attention for %s may be dropping — consider pausing for a question.", feature.AudienceID)
		reasonCodes = []string{"attention_score_drop"}
	}

	return llmCandidate{
		Message:       message,
		ReasonCodes:   reasonCodes,
		EvidenceQuote: evidenceQuote,
		ModelVersion:  llmStubModelVersion,
		Confidence:    llmStubConfidence,
	}, nil
}

// decideFeedback builds the feedback_event (via buildFeedback) and its
// paired decision_log. When llmEnabled, it attempts the realtime LLM stub
// within realtimeLLMTimeout; on success the feedback message and
// decision_log.source ("llm_stub") reflect that candidate, evidence_quote
// included. On any error (or when llmEnabled is false) it falls back to
// the rule-based feedback already computed, decision_log.source "rule",
// evidence_quote nil — matching Phase 12's completion condition that
// feedback keeps flowing with the LLM disabled or unavailable.
func decideFeedback(ctx context.Context, feature contract.CompactFeature, state gwredis.BaselineState, llmEnabled bool, evidenceQuote string) (contract.FeedbackEvent, contract.DecisionLog) {
	feedback := buildFeedback(feature, state)

	detail := contract.DecisionDetail{
		FeedbackType: feedback.FeedbackType,
		Severity:     feedback.Severity,
		Message:      feedback.Message,
		Confidence:   ruleDecisionConfidence,
	}
	source := "rule"

	if llmEnabled {
		llmCtx, cancel := context.WithTimeout(ctx, realtimeLLMTimeout)
		candidate, err := generateLLMStubCandidate(llmCtx, feature, feedback.FeedbackType, evidenceQuote)
		cancel()
		if err != nil {
			log.Printf("gateway: realtime llm stub unavailable, falling back to rule: %v", err)
		} else {
			feedback.Message = candidate.Message
			feedback.Source = "llm_stub"
			source = "llm_stub"
			detail = contract.DecisionDetail{
				FeedbackType:  feedback.FeedbackType,
				Severity:      feedback.Severity,
				Message:       candidate.Message,
				ReasonCodes:   candidate.ReasonCodes,
				EvidenceQuote: nonEmptyPtr(candidate.EvidenceQuote),
				ModelVersion:  candidate.ModelVersion,
				Confidence:    candidate.Confidence,
			}
		}
	}

	decisionBody, err := json.Marshal(detail)
	if err != nil {
		log.Printf("gateway: marshal decision detail failed: %v", err)
		decisionBody = json.RawMessage("{}")
	}

	decision := contract.DecisionLog{
		EventID:    "evt_" + uuid.NewString(),
		SessionID:  feature.SessionID,
		AudienceID: feature.AudienceID,
		TMs:        feature.TMs,
		Source:     source,
		Decision:   decisionBody,
	}

	return feedback, decision
}

// nonEmptyPtr returns nil for an empty string, else a pointer to it —
// DecisionDetail.EvidenceQuote must be JSON null, not "", when there is no
// transcript to quote.
func nonEmptyPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func (h *Handler) writeError(ctx context.Context, conn *websocket.Conn, message string) {
	payload := map[string]string{"type": "error", "message": message}
	if err := wsjson.Write(ctx, conn, payload); err != nil {
		log.Printf("gateway: write error failed: %v", err)
	}
}
