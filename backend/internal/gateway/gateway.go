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

	// Phase 4 stub: a real signal_summary/decision_log driven feedback_event
	// lands in later phases (system computation layer, cooldown, LLM).
	feedback := contract.FeedbackEvent{
		Type:         "feedback_event",
		SessionID:    msg.SessionID,
		TMs:          msg.TMs,
		FeedbackType: "stub",
		Severity:     "info",
		Message:      "placeholder feedback_event (Phase 4)",
		Source:       "stub",
	}
	if err := wsjson.Write(ctx, conn, feedback); err != nil {
		log.Printf("gateway: write feedback_event failed: %v", err)
	}
}

func (h *Handler) writeError(ctx context.Context, conn *websocket.Conn, message string) {
	payload := map[string]string{"type": "error", "message": message}
	if err := wsjson.Write(ctx, conn, payload); err != nil {
		log.Printf("gateway: write error failed: %v", err)
	}
}
