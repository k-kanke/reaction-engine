package gateway

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"

	"github.com/k-kanke/reaction-engine/backend/internal/contract"
	"github.com/k-kanke/reaction-engine/backend/internal/postsessiontrigger"
	"github.com/k-kanke/reaction-engine/backend/internal/realtime"
	gwredis "github.com/k-kanke/reaction-engine/backend/internal/redis"
	"github.com/k-kanke/reaction-engine/backend/internal/speech"
)

const featureEventsTopic = "feature-events"

// EventPublisher is the durable event bus boundary gateway publishes
// mood_wave_sample/trigger_event/feedback_event/transcript_chunk payloads
// to -- either the real Pub/Sub "feature-events" topic
// (internal/pubsub.Publisher, EVENT_BUS_BACKEND=pubsub) or, for local dev,
// db.LocalEventStore's Postgres-table stand-in (EVENT_BUS_BACKEND=local,
// the default). Same shape as internal/media.EventPublisher, deliberately,
// so both packages' callers can swap implementations identically.
type EventPublisher interface {
	Enqueue(ctx context.Context, topic, eventID string, payload any) error
}

// transcriptFlushIntervalMs is how much audio_chunk t_ms range accumulates
// per speaker before the stub STT flushes a fake transcript_chunk.
// fakeTranscriptConfidence is a fixed stub confidence. This path only runs
// when no real Recognizer is configured (h.stt == nil).
const (
	transcriptFlushIntervalMs = 5000
	fakeTranscriptConfidence  = 0.6
)

// maxSTTStreamDuration bounds one real Speech-to-Text stream's lifetime,
// safely under Google's ~305s per-stream cap. architecture.md: "1ストリーム
// の継続時間上限があるため、Gatewayが会議中に定期的にストリームを再接続する"
// — the gateway (not internal/speech) owns this reconnect schedule because
// it's tied to per-connection state.
const maxSTTStreamDuration = 240 * time.Second

// audioAccumulator tracks one speaker's audio_chunk arrivals within a
// single WebSocket connection so the gateway knows when to flush a stub
// transcript_chunk. It only ever sees t_ms/count — raw PCM is read off the
// wire and discarded, never stored here or anywhere else. Only used when
// real STT is disabled.
type audioAccumulator struct {
	firstTMs int64
	lastTMs  int64
}

// sttSession is one speaker's live real-STT stream within a connection,
// tracked so handleAudioChunk knows when to reconnect (maxSTTStreamDuration)
// or lazily create one.
type sttSession struct {
	stream    *speech.Stream
	startedAt time.Time
}

type Handler struct {
	redis              *gwredis.Client
	events             EventPublisher
	llm                realtime.FeedbackGenerator
	stt                speech.Recognizer
	sttLanguageCode    string
	postSessionTrigger postsessiontrigger.Trigger
}

func NewHandler(redis *gwredis.Client, events EventPublisher, llmEnabled bool) *Handler {
	var generator realtime.FeedbackGenerator
	if llmEnabled {
		generator = realtime.StubFeedbackGenerator{}
	}
	return NewHandlerWithFeedbackGenerator(redis, events, generator)
}

// NewHandlerWithFeedbackGenerator builds a Handler with no real STT
// (ENABLE_REAL_STT=false path): audio_chunk falls back to the stub
// transcript builder below, unchanged from before Step 4.
func NewHandlerWithFeedbackGenerator(redis *gwredis.Client, events EventPublisher, generator realtime.FeedbackGenerator) *Handler {
	return NewHandlerWithSTT(redis, events, generator, nil, "")
}

// NewHandlerWithSTT is NewHandlerWithFeedbackGenerator with an injected
// real Speech-to-Text Recognizer. recognizer nil keeps the stub transcript
// path (used by tests and local runs without GCP credentials).
func NewHandlerWithSTT(redis *gwredis.Client, events EventPublisher, generator realtime.FeedbackGenerator, recognizer speech.Recognizer, sttLanguageCode string) *Handler {
	return NewHandlerWithPostSessionTrigger(redis, events, generator, recognizer, sttLanguageCode, nil)
}

// NewHandlerWithPostSessionTrigger is NewHandlerWithSTT with an injected
// post-session pipeline Trigger (Step 5 of
// plan/post-session-report-implementation.md). trigger nil makes
// session_end a no-op besides logging -- used by tests and local runs
// without a deployed r-post-session-job/r-pdf-renderer to call.
func NewHandlerWithPostSessionTrigger(redis *gwredis.Client, events EventPublisher, generator realtime.FeedbackGenerator, recognizer speech.Recognizer, sttLanguageCode string, trigger postsessiontrigger.Trigger) *Handler {
	return &Handler{redis: redis, events: events, llm: generator, stt: recognizer, sttLanguageCode: sttLanguageCode, postSessionTrigger: trigger}
}

// ServeWS handles GET /ws: it accepts the WebSocket connection, dispatches
// each incoming message by its "type" field, and keeps reading until the
// client disconnects.
func (h *Handler) ServeWS(w http.ResponseWriter, r *http.Request) {
	if !isAllowedWebSocketOrigin(r) {
		log.Printf("gateway: websocket origin rejected: origin=%q host=%q", r.Header.Get("Origin"), r.Host)
		http.Error(w, "websocket origin not allowed", http.StatusForbidden)
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// Browser extensions use chrome-extension://<extension-id> origins,
		// which nhooyr's same-origin default rejects. We perform the narrower
		// extension-origin check above, then skip the library's duplicate check.
		InsecureSkipVerify: true,
	})
	if err != nil {
		log.Printf("gateway: websocket accept failed: %v", err)
		return
	}
	defer conn.CloseNow()

	ctx := r.Context()
	// One session per connection (architecture.md: "Gateway が session_id
	// ごとに self/other 各1本の Speech-to-Text streaming セッションを維持"),
	// so per-speaker audio accumulators/STT sessions live for the
	// connection's lifetime.
	audioAccumulators := make(map[string]*audioAccumulator)
	sttSessions := make(map[string]*sttSession)
	defer func() {
		for _, sess := range sttSessions {
			sess.stream.Close()
		}
	}()

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
			h.handleAudioChunk(ctx, raw, audioAccumulators, sttSessions)
		case "session_end":
			h.handleSessionEnd(raw)
		default:
			h.writeError(ctx, conn, "unsupported type: "+envelope.Type)
		}
	}
}

func isAllowedWebSocketOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}

	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if u.Host == "" {
		return false
	}
	if u.Host == r.Host {
		return true
	}

	switch u.Scheme {
	case "chrome-extension", "moz-extension":
		return true
	}

	return false
}

// handleMoodWaveSample implements architecture.md's "on mood_wave_sample"
// pseudocode: validate, assign event_id/server_received_at_ms, cache it in
// Redis (mood_wave:recent + session:state), publish it for durable
// processing, and — when the sample carries a trigger — cache that trigger
// and hand it to the Realtime Worker (internal/realtime, Step 5 of
// plan/mood-wave-contract-migration.md) for cooldown/LLM-budget gating and
// a feedback decision. When a trigger is accepted, the resulting
// trigger_event/feedback_event are written back to Chrome over this
// connection and separately enqueued for Durable Writer (Step 6) to
// persist to trigger_events/feedback_events + JSONL.
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

	if msg.Trigger == nil {
		return
	}

	if err := h.redis.StoreRecentTrigger(ctx, msg.SessionID, *msg.Trigger); err != nil {
		log.Printf("gateway: store recent trigger failed: %v", err)
	}

	feedback, accepted, err := realtime.HandleTriggerWithGenerator(ctx, h.redis, msg, h.llm)
	if err != nil {
		log.Printf("gateway: realtime worker handle trigger failed: %v", err)
		return
	}
	if !accepted {
		return
	}

	if err := wsjson.Write(ctx, conn, feedback); err != nil {
		log.Printf("gateway: write feedback_event failed: %v", err)
	}

	h.enqueueTriggerAndFeedback(ctx, msg, feedback)
}

// handleSessionEnd starts the post-session report pipeline (Step 5 of
// plan/post-session-report-implementation.md) for the session that just
// ended. It runs in its own goroutine with a fresh background context --
// not r.Context() from ServeWS, which cancels the moment this WebSocket
// connection closes, which typically happens right after the extension
// sends session_end. The pipeline (post-session-job then pdf-renderer) can
// take longer than that and must survive the disconnect.
func (h *Handler) handleSessionEnd(raw json.RawMessage) {
	if h.postSessionTrigger == nil {
		return
	}

	var msg contract.SessionEndMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		log.Printf("gateway: invalid session_end payload: %v", err)
		return
	}
	if msg.SessionID == "" {
		log.Printf("gateway: session_end missing session_id")
		return
	}

	log.Printf("gateway: session_end received session_id=%s, starting post-session pipeline", msg.SessionID)
	go h.postSessionTrigger.TriggerSessionEnd(context.Background(), msg.SessionID)
}

// enqueueTriggerAndFeedback publishes the trigger_event/feedback_event for
// one accepted trigger to the durable local event bus, for Durable Writer
// (Step 6) to persist. It sets a fresh EventID on its own copy of feedback
// so that ID never reaches Chrome (the WS write in handleMoodWaveSample
// already happened, using the zero-EventID value architecture.md's
// Chrome-facing example has).
func (h *Handler) enqueueTriggerAndFeedback(ctx context.Context, msg contract.MoodWaveSampleMessage, feedback contract.FeedbackEvent) {
	trigger := *msg.Trigger

	triggerEvent := contract.TriggerEvent{
		EventID:   "evt_trig_" + uuid.NewString(),
		SessionID: msg.SessionID,
		TriggerID: trigger.TriggerID,
		Type:      trigger.Type,
		Source:    trigger.Source,
		TMs:       msg.TMs,
		PeakTMs:   trigger.PeakTMs,
		Delta:     trigger.Delta,
	}

	feedback.EventID = "evt_fb_" + uuid.NewString()

	payload := contract.FeatureEventPayload{
		EventID:            triggerEvent.EventID,
		SessionID:          msg.SessionID,
		TMs:                msg.TMs,
		ServerReceivedAtMs: time.Now().UnixMilli(),
		TriggerEvents:      []contract.TriggerEvent{triggerEvent},
		FeedbackEvents:     []contract.FeedbackEvent{feedback},
	}
	if err := h.events.Enqueue(ctx, featureEventsTopic, triggerEvent.EventID, payload); err != nil {
		log.Printf("gateway: enqueue trigger/feedback event failed: %v", err)
	}
}

// handleAudioChunk routes each audio_chunk to real Speech-to-Text
// (h.stt != nil, Step 4) or, when no Recognizer is configured, the Phase 11
// stub: it fabricates one deterministic "final" transcript_chunk once
// transcriptFlushIntervalMs of audio_chunk t_ms range has accumulated per
// speaker. Either way the resulting transcript_chunk goes through
// persistTranscriptChunk (Redis + durable enqueue) the same way.
func (h *Handler) handleAudioChunk(ctx context.Context, raw json.RawMessage, accumulators map[string]*audioAccumulator, sttSessions map[string]*sttSession) {
	var msg contract.AudioChunkMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		log.Printf("gateway: invalid audio_chunk payload: %v", err)
		return
	}
	if msg.Speaker == "" {
		log.Printf("gateway: audio_chunk missing speaker")
		return
	}

	if h.stt != nil {
		h.forwardToSTT(ctx, msg, sttSessions)
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

	h.persistTranscriptChunk(ctx, chunk)
}

// forwardToSTT is the Step 4 real-STT path: lazily creates (or reconnects,
// past maxSTTStreamDuration) a Speech-to-Text stream for msg.Speaker and
// forwards its decoded PCM. Any failure here is logged and the session is
// dropped so the next audio_chunk retries fresh — it never blocks or breaks
// the mood_wave/evidence-frame feedback flow, which doesn't depend on
// transcripts existing (architecture.md rule fallback already covers an
// empty transcript_window).
func (h *Handler) forwardToSTT(ctx context.Context, msg contract.AudioChunkMessage, sttSessions map[string]*sttSession) {
	sess, err := h.getOrCreateSTTStream(ctx, sttSessions, msg)
	if err != nil {
		log.Printf("gateway: create stt stream failed for speaker=%s: %v", msg.Speaker, err)
		return
	}

	pcm, err := base64.StdEncoding.DecodeString(msg.PCM)
	if err != nil {
		log.Printf("gateway: decode audio_chunk pcm failed for speaker=%s: %v", msg.Speaker, err)
		return
	}

	if err := sess.stream.Send(pcm); err != nil {
		log.Printf("gateway: stt send failed for speaker=%s: %v", msg.Speaker, err)
		sess.stream.Close()
		delete(sttSessions, msg.Speaker)
	}
}

// getOrCreateSTTStream returns msg.Speaker's live stream, transparently
// reconnecting once it's older than maxSTTStreamDuration (architecture.md's
// periodic-reconnect requirement — see the const's doc comment) or creating
// one on first use. A fresh stream gets its own result-consuming goroutine
// (consumeSTTResults) for its whole lifetime.
func (h *Handler) getOrCreateSTTStream(ctx context.Context, sttSessions map[string]*sttSession, msg contract.AudioChunkMessage) (*sttSession, error) {
	if sess, ok := sttSessions[msg.Speaker]; ok {
		if time.Since(sess.startedAt) < maxSTTStreamDuration {
			return sess, nil
		}
		sess.stream.Close()
		delete(sttSessions, msg.Speaker)
	}

	stream, err := h.stt.NewStream(ctx, int32(msg.SampleRate), h.sttLanguageCode)
	if err != nil {
		return nil, fmt.Errorf("new stt stream: %w", err)
	}

	sess := &sttSession{stream: stream, startedAt: time.Now()}
	sttSessions[msg.Speaker] = sess
	go h.consumeSTTResults(ctx, msg.SessionID, msg.Speaker, sess)
	return sess, nil
}

// consumeSTTResults reads final results off one stream for its whole
// lifetime, turning each into a transcript_chunk. TStartMs/TEndMs are wall
// clock (time since the previous result, or stream start) rather than
// Google's audio-relative offsets, matching the stub path's granularity —
// downstream only needs these for the recent-window sort/filter, not exact
// audio timing.
func (h *Handler) consumeSTTResults(ctx context.Context, sessionID, speaker string, sess *sttSession) {
	sinceMs := sess.startedAt.UnixMilli()
	for result := range sess.stream.Results() {
		nowMs := time.Now().UnixMilli()
		chunk := contract.TranscriptChunk{
			EventID:       "evt_" + uuid.NewString(),
			Type:          "transcript_chunk",
			SchemaVersion: 1,
			SessionID:     sessionID,
			Speaker:       speaker,
			TStartMs:      sinceMs,
			TEndMs:        nowMs,
			Text:          result.Text,
			Confidence:    result.Confidence,
			IsFinal:       true,
		}
		h.persistTranscriptChunk(ctx, chunk)
		sinceMs = nowMs
	}

	// Results() closing doesn't necessarily mean anything went wrong (it
	// also closes on a deliberate Close() from reconnect/teardown), so only
	// log when the stream actually ended abnormally -- e.g. missing
	// roles/speech.client, quota, or a network error. Either way this
	// speaker's transcripts simply stop; mood_wave/evidence-frame feedback
	// keeps flowing independently.
	if err := sess.stream.Err(); err != nil {
		log.Printf("gateway: stt stream ended for speaker=%s: %v", speaker, err)
	}
}

// persistTranscriptChunk is the shared sink for every transcript_chunk,
// stub or real: cache in the Redis recent window and enqueue for Durable
// Writer (Cloud SQL transcripts + JSONL).
func (h *Handler) persistTranscriptChunk(ctx context.Context, chunk contract.TranscriptChunk) {
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
