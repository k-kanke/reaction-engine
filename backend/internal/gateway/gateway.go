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

type Handler struct {
	redis  *gwredis.Client
	events *db.LocalEventStore
	// llmEnabled is read by the Realtime Worker's trigger-acceptance path
	// (plan/mood-wave-contract-migration.md Step 5), not by this file
	// directly — handleMoodWaveSample only ingests and stores.
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
		case "mood_wave_sample":
			h.handleMoodWaveSample(ctx, conn, raw)
		case "audio_chunk":
			h.handleAudioChunk(ctx, raw, audioAccumulators)
		default:
			h.writeError(ctx, conn, "unsupported type: "+envelope.Type)
		}
	}
}

// handleMoodWaveSample implements architecture.md's "on mood_wave_sample"
// pseudocode: validate, assign event_id/server_received_at_ms, cache it in
// Redis (mood_wave:recent + session:state), publish it for durable
// processing, and — when the sample carries a trigger — cache that trigger
// too. It intentionally does not decide or emit any feedback_event itself:
// window summary, cooldown/LLM budget, evidence pack assembly, and the
// LLM/rule decision all belong to the Realtime Worker
// (plan/mood-wave-contract-migration.md Step 5), which reads the state this
// handler writes.
func (h *Handler) handleMoodWaveSample(ctx context.Context, conn *websocket.Conn, raw json.RawMessage) {
	var msg contract.MoodWaveSampleMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		h.writeError(ctx, conn, "invalid mood_wave_sample payload")
		return
	}

	eventID := "evt_" + uuid.NewString()
	serverReceivedAtMs := time.Now().UnixMilli()

	if err := h.redis.StoreRecentMoodWaveSample(ctx, msg); err != nil {
		log.Printf("gateway: store recent mood wave sample failed: %v", err)
	}

	payload := contract.FeatureEventPayload{
		EventID:            eventID,
		SessionID:          msg.SessionID,
		TMs:                msg.TMs,
		ServerReceivedAtMs: serverReceivedAtMs,
		MoodWaveSample:     &msg,
	}
	if err := h.events.Enqueue(ctx, featureEventsTopic, eventID, payload); err != nil {
		log.Printf("gateway: enqueue local event failed: %v", err)
	}

	if msg.Trigger != nil {
		if err := h.redis.StoreRecentTrigger(ctx, msg.SessionID, *msg.Trigger); err != nil {
			log.Printf("gateway: store recent trigger failed: %v", err)
		}
	}
}

// handleAudioChunk implements the Phase 11 STT stub: it never calls a real
// Speech-to-Text service and never persists msg.PCM anywhere. It only
// tracks how much audio_chunk t_ms range has accumulated per speaker, and
// once that reaches transcriptFlushIntervalMs it fabricates one
// deterministic "final" transcript_chunk covering that range, caches it in
// the Redis transcript window, and enqueues it to the same feature-events
// local bus mood_wave_sample uses so Durable Writer persists it too.
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

func (h *Handler) writeError(ctx context.Context, conn *websocket.Conn, message string) {
	payload := map[string]string{"type": "error", "message": message}
	if err := wsjson.Write(ctx, conn, payload); err != nil {
		log.Printf("gateway: write error failed: %v", err)
	}
}
