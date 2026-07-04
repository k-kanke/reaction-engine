package gateway

import (
	"context"
	"encoding/json"
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

type Handler struct {
	redis  *gwredis.Client
	events *db.LocalEventStore
}

func NewHandler(redis *gwredis.Client, events *db.LocalEventStore) *Handler {
	return &Handler{redis: redis, events: events}
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

	payload := contract.FeatureEventPayload{
		EventID:            eventID,
		SessionID:          msg.SessionID,
		TMs:                msg.TMs,
		ServerReceivedAtMs: serverReceivedAtMs,
		Features:           compactFeatures,
	}
	if err := h.events.Enqueue(ctx, featureEventsTopic, eventID, payload); err != nil {
		log.Printf("gateway: enqueue local event failed: %v", err)
	}

	// One feedback_event per audience_id: baseline-aware correction
	// (Phase 9) is per participant, and a real signal_summary/decision_log
	// driven decision still lands in a later phase (system computation
	// layer, cooldown, LLM).
	for _, feature := range compactFeatures {
		state, err := h.redis.GetBaselineState(ctx, feature.SessionID, feature.AudienceID)
		if err != nil {
			log.Printf("gateway: get baseline state failed: %v", err)
			state = gwredis.BaselineState{Status: gwredis.BaselineStatusWarmingUp}
		}

		feedback := buildFeedback(feature, state)
		if err := wsjson.Write(ctx, conn, feedback); err != nil {
			log.Printf("gateway: write feedback_event failed: %v", err)
			return
		}
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

func (h *Handler) writeError(ctx context.Context, conn *websocket.Conn, message string) {
	payload := map[string]string{"type": "error", "message": message}
	if err := wsjson.Write(ctx, conn, payload); err != nil {
		log.Printf("gateway: write error failed: %v", err)
	}
}
