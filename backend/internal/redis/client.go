package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/k-kanke/reaction-engine/backend/internal/contract"
)

const (
	// moodWaveRecentTrimWindow mirrors architecture.md's Redis pseudocode
	// ("ZREMRANGEBYSCORE mood_wave:recent:{session_id} -inf now-10min"):
	// the session-level mood wave ZSET keeps 10 minutes of history so the
	// Realtime Worker (Step 5) can slice any window up to that length, not
	// just the realtime 30s window.
	moodWaveRecentTrimWindow = 10 * time.Minute

	// transcriptRecentWindow was 10s under the old per-message feedback
	// model. The Realtime Worker (Step 5) needs a 30s transcript_window
	// alongside the 30s mood wave window, so this keeps a 10s safety
	// margin beyond that instead of trimming right at the edge.
	transcriptRecentWindow = 40 * time.Second

	keyTTL = time.Hour
)

type Client struct {
	rdb *redis.Client
}

func NewClient(addr string) *Client {
	return &Client{rdb: redis.NewClient(&redis.Options{Addr: addr})}
}

func (c *Client) Ping(ctx context.Context) error {
	return c.rdb.Ping(ctx).Err()
}

func (c *Client) Close() error {
	return c.rdb.Close()
}

func transcriptRecentKey(sessionID, speaker string) string {
	return fmt.Sprintf("transcript:recent:%s:%s", sessionID, speaker)
}

// StoreRecentTranscript appends one finalized transcript_chunk to the
// session/speaker recent window (ZSET scored by t_start_ms), trims entries
// older than the window, and refreshes the key TTL. Mirrors
// StoreRecentFeature's shape for features:recent:*, per architecture.md's
// "on audio_chunk" pseudocode and Phase 11 of
// plan/backend-local-docker-runbook.md.
func (c *Client) StoreRecentTranscript(ctx context.Context, chunk contract.TranscriptChunk) error {
	payload, err := json.Marshal(chunk)
	if err != nil {
		return err
	}

	key := transcriptRecentKey(chunk.SessionID, chunk.Speaker)
	cutoff := chunk.TEndMs - transcriptRecentWindow.Milliseconds()

	pipe := c.rdb.Pipeline()
	pipe.ZAdd(ctx, key, redis.Z{Score: float64(chunk.TStartMs), Member: payload})
	pipe.ZRemRangeByScore(ctx, key, "-inf", fmt.Sprintf("%d", cutoff))
	pipe.Expire(ctx, key, keyTTL)
	_, err = pipe.Exec(ctx)
	return err
}

// GetLatestTranscript returns the most recent (highest t_start_ms) cached
// transcript_chunk for one session_id + speaker, if any. Used by the
// realtime LLM stub (Phase 12) as the evidence_quote grounding a feedback
// candidate.
func (c *Client) GetLatestTranscript(ctx context.Context, sessionID, speaker string) (contract.TranscriptChunk, bool, error) {
	vals, err := c.rdb.ZRevRange(ctx, transcriptRecentKey(sessionID, speaker), 0, 0).Result()
	if err != nil {
		return contract.TranscriptChunk{}, false, err
	}
	if len(vals) == 0 {
		return contract.TranscriptChunk{}, false, nil
	}

	var chunk contract.TranscriptChunk
	if err := json.Unmarshal([]byte(vals[0]), &chunk); err != nil {
		return contract.TranscriptChunk{}, false, err
	}
	return chunk, true, nil
}

// GetTranscriptWindow returns every cached transcript_chunk for
// session_id + speaker with t_start_ms in [sinceTMs, +inf), oldest first.
// Used by the Realtime Worker (Step 5) to build the transcript_window half
// of a realtime evidence pack, alongside GetLatestTranscript's
// single-chunk read for the (now-dormant) old feedback path.
func (c *Client) GetTranscriptWindow(ctx context.Context, sessionID, speaker string, sinceTMs int64) ([]contract.TranscriptChunk, error) {
	vals, err := c.rdb.ZRangeByScore(ctx, transcriptRecentKey(sessionID, speaker), &redis.ZRangeBy{
		Min: fmt.Sprintf("%d", sinceTMs),
		Max: "+inf",
	}).Result()
	if err != nil {
		return nil, err
	}

	chunks := make([]contract.TranscriptChunk, 0, len(vals))
	for _, v := range vals {
		var chunk contract.TranscriptChunk
		if err := json.Unmarshal([]byte(v), &chunk); err != nil {
			return nil, err
		}
		chunks = append(chunks, chunk)
	}
	return chunks, nil
}

func baselineKey(sessionID, audienceID string) string {
	return fmt.Sprintf("session:baseline:%s:%s", sessionID, audienceID)
}

func visualSummaryKey(sessionID, audienceID string) string {
	return fmt.Sprintf("session:visual_summary:%s:%s", sessionID, audienceID)
}

func baselineStatusKey(sessionID, audienceID string) string {
	return fmt.Sprintf("session:baseline_status:%s:%s", sessionID, audienceID)
}

func baselineMediaRefKey(sessionID, audienceID string) string {
	return fmt.Sprintf("session:baseline_media_ref:%s:%s", sessionID, audienceID)
}

// baselineParticipantsKey is a SET of every audience_id with a ready
// baseline for session_id, so ListReadyBaselineMediaRefs (the Realtime
// Worker's evidence pack, Step 5 of plan/mood-wave-contract-migration.md)
// can enumerate baseline_frames without an audience_id index elsewhere —
// mood_wave_sample is session-level, so nothing else names participants.
func baselineParticipantsKey(sessionID string) string {
	return fmt.Sprintf("session:baseline_participants:%s", sessionID)
}

// SetBaselineReady caches a participant's baseline, visual summary, and the
// media_ref of the baseline frame it was derived from, and marks
// baseline_status "ready", per plan/backend-local-docker-runbook.md Phase
// 8. Gateway feedback logic (Phase 9+) reads baseline/visual_summary/status
// to apply baseline-aware corrections once ready; the Realtime Worker
// (Step 5) reads mediaRef (via ListReadyBaselineMediaRefs) to include
// baseline_frames in its LLM evidence pack.
func (c *Client) SetBaselineReady(ctx context.Context, sessionID, audienceID string, baseline, visualSummary []byte, mediaRef string) error {
	pipe := c.rdb.Pipeline()
	pipe.Set(ctx, baselineKey(sessionID, audienceID), baseline, keyTTL)
	pipe.Set(ctx, visualSummaryKey(sessionID, audienceID), visualSummary, keyTTL)
	pipe.Set(ctx, baselineStatusKey(sessionID, audienceID), "ready", keyTTL)
	pipe.Set(ctx, baselineMediaRefKey(sessionID, audienceID), mediaRef, keyTTL)
	pipe.SAdd(ctx, baselineParticipantsKey(sessionID), audienceID)
	pipe.Expire(ctx, baselineParticipantsKey(sessionID), keyTTL)
	_, err := pipe.Exec(ctx)
	return err
}

// ListReadyBaselineMediaRefs returns the baseline frame media_ref of every
// participant with a ready baseline in session_id, for the Realtime
// Worker's evidence pack (Step 5). Order is unspecified (SMEMBERS order).
func (c *Client) ListReadyBaselineMediaRefs(ctx context.Context, sessionID string) ([]string, error) {
	audienceIDs, err := c.rdb.SMembers(ctx, baselineParticipantsKey(sessionID)).Result()
	if err != nil {
		return nil, err
	}
	if len(audienceIDs) == 0 {
		return nil, nil
	}

	keys := make([]string, len(audienceIDs))
	for i, audienceID := range audienceIDs {
		keys[i] = baselineMediaRefKey(sessionID, audienceID)
	}

	vals, err := c.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}

	refs := make([]string, 0, len(vals))
	for _, v := range vals {
		if s, ok := v.(string); ok && s != "" {
			refs = append(refs, s)
		}
	}
	return refs, nil
}

// BaselineStatusReady and BaselineStatusWarmingUp mirror the
// session:baseline_status:* values from architecture.md: "ready" once the
// Image Analysis Worker has produced a baseline for this participant,
// "warming_up" before that (no row yet, or the status key missing/expired).
const (
	BaselineStatusReady     = "ready"
	BaselineStatusWarmingUp = "warming_up"
)

// BaselineState is the Gateway-side read of a participant's cached
// baseline/visual_summary/baseline_status, per Phase 9 of
// plan/backend-local-docker-runbook.md.
type BaselineState struct {
	Status        string
	Baseline      json.RawMessage
	VisualSummary json.RawMessage
}

// GetBaselineState reads session:baseline:*, session:visual_summary:*, and
// session:baseline_status:* for one session_id + audience_id. A missing
// baseline_status key (nothing written yet by the Image Analysis Worker)
// reports BaselineStatusWarmingUp rather than an error.
func (c *Client) GetBaselineState(ctx context.Context, sessionID, audienceID string) (BaselineState, error) {
	vals, err := c.rdb.MGet(ctx,
		baselineKey(sessionID, audienceID),
		visualSummaryKey(sessionID, audienceID),
		baselineStatusKey(sessionID, audienceID),
	).Result()
	if err != nil {
		return BaselineState{}, err
	}

	state := BaselineState{Status: BaselineStatusWarmingUp}
	if s, ok := vals[0].(string); ok {
		state.Baseline = json.RawMessage(s)
	}
	if s, ok := vals[1].(string); ok {
		state.VisualSummary = json.RawMessage(s)
	}
	if s, ok := vals[2].(string); ok && s != "" {
		state.Status = s
	}
	return state, nil
}

// moodWaveRecentKey is architecture.md's "mood_wave:recent:{session_id}":
// one session-level ZSET (scored by t_ms) replacing the old
// features:recent:{session_id}:{audience_id} per-participant buffers.
func moodWaveRecentKey(sessionID string) string {
	return fmt.Sprintf("mood_wave:recent:%s", sessionID)
}

// sessionStateKey is architecture.md's "session:state:{session_id}" hash,
// holding the latest mood_wave_sample under field "latest_mood_sample" per
// the "on mood_wave_sample" pseudocode.
func sessionStateKey(sessionID string) string {
	return fmt.Sprintf("session:state:%s", sessionID)
}

const latestMoodSampleField = "latest_mood_sample"

// StoreRecentMoodWaveSample implements architecture.md's "on
// mood_wave_sample" pseudocode: ZADD into the session's recent window,
// trim anything older than moodWaveRecentTrimWindow, refresh the key TTL,
// and HSET the sample as session:state's latest_mood_sample. It replaces
// StoreRecentFeature once Step 4 of plan/mood-wave-contract-migration.md
// retires realtime_feature ingestion.
func (c *Client) StoreRecentMoodWaveSample(ctx context.Context, sample contract.MoodWaveSampleMessage) error {
	payload, err := json.Marshal(sample)
	if err != nil {
		return err
	}

	key := moodWaveRecentKey(sample.SessionID)
	cutoff := sample.TMs - moodWaveRecentTrimWindow.Milliseconds()

	pipe := c.rdb.Pipeline()
	pipe.ZAdd(ctx, key, redis.Z{Score: float64(sample.TMs), Member: payload})
	pipe.ZRemRangeByScore(ctx, key, "-inf", fmt.Sprintf("%d", cutoff))
	pipe.Expire(ctx, key, keyTTL)
	pipe.HSet(ctx, sessionStateKey(sample.SessionID), latestMoodSampleField, payload)
	pipe.Expire(ctx, sessionStateKey(sample.SessionID), keyTTL)
	_, err = pipe.Exec(ctx)
	return err
}

// GetRecentMoodWaveSamples returns every mood_wave_sample cached for
// session_id with t_ms in [sinceTMs, +inf), oldest first. The Realtime
// Worker (Step 5 of plan/mood-wave-contract-migration.md) slices this into
// the 30s window and computes its summary (slope/volatility/min/max); this
// method only reads the raw points back out of Redis.
func (c *Client) GetRecentMoodWaveSamples(ctx context.Context, sessionID string, sinceTMs int64) ([]contract.MoodWaveSampleMessage, error) {
	vals, err := c.rdb.ZRangeByScore(ctx, moodWaveRecentKey(sessionID), &redis.ZRangeBy{
		Min: fmt.Sprintf("%d", sinceTMs),
		Max: "+inf",
	}).Result()
	if err != nil {
		return nil, err
	}

	samples := make([]contract.MoodWaveSampleMessage, 0, len(vals))
	for _, v := range vals {
		var sample contract.MoodWaveSampleMessage
		if err := json.Unmarshal([]byte(v), &sample); err != nil {
			return nil, err
		}
		samples = append(samples, sample)
	}
	return samples, nil
}

// GetLatestMoodWaveSample returns session:state:{session_id}'s
// latest_mood_sample field, if any.
func (c *Client) GetLatestMoodWaveSample(ctx context.Context, sessionID string) (contract.MoodWaveSampleMessage, bool, error) {
	val, err := c.rdb.HGet(ctx, sessionStateKey(sessionID), latestMoodSampleField).Result()
	if errors.Is(err, redis.Nil) {
		return contract.MoodWaveSampleMessage{}, false, nil
	}
	if err != nil {
		return contract.MoodWaveSampleMessage{}, false, err
	}

	var sample contract.MoodWaveSampleMessage
	if err := json.Unmarshal([]byte(val), &sample); err != nil {
		return contract.MoodWaveSampleMessage{}, false, err
	}
	return sample, true, nil
}

// triggerRecentKey is architecture.md's "trigger:recent:{session_id}": the
// most recently accepted trigger, so the Realtime Worker (Step 5) can rate
// limit how often a new trigger is accepted regardless of the feedback
// cooldown below.
func triggerRecentKey(sessionID string) string {
	return fmt.Sprintf("trigger:recent:%s", sessionID)
}

// StoreRecentTrigger caches the given trigger as session_id's most recent
// one, with keyTTL expiry.
func (c *Client) StoreRecentTrigger(ctx context.Context, sessionID string, trigger contract.TriggerInfo) error {
	payload, err := json.Marshal(trigger)
	if err != nil {
		return err
	}
	return c.rdb.Set(ctx, triggerRecentKey(sessionID), payload, keyTTL).Err()
}

// GetRecentTrigger returns session_id's most recently cached trigger, if
// any.
func (c *Client) GetRecentTrigger(ctx context.Context, sessionID string) (contract.TriggerInfo, bool, error) {
	val, err := c.rdb.Get(ctx, triggerRecentKey(sessionID)).Result()
	if errors.Is(err, redis.Nil) {
		return contract.TriggerInfo{}, false, nil
	}
	if err != nil {
		return contract.TriggerInfo{}, false, err
	}

	var trigger contract.TriggerInfo
	if err := json.Unmarshal([]byte(val), &trigger); err != nil {
		return contract.TriggerInfo{}, false, err
	}
	return trigger, true, nil
}

// feedbackRecentKey stores the last N feedback_events for a session so the
// Realtime Worker can include recent feedback history in the LLM evidence
// pack, letting the model avoid repeating the same advice.
func feedbackRecentKey(sessionID string) string {
	return fmt.Sprintf("feedback:recent:%s", sessionID)
}

// feedbackRecentMaxCount is how many feedback events to keep in the recent
// ZSET. We keep more than the 3 the LLM reads so a trim race never drops
// entries the next trigger needs.
const feedbackRecentMaxCount = 10

// StoreRecentFeedback appends one feedback_event to the session's recent
// feedback ZSET (scored by t_ms), trims to feedbackRecentMaxCount, and
// refreshes the key TTL.
func (c *Client) StoreRecentFeedback(ctx context.Context, feedback contract.FeedbackEvent) error {
	payload, err := json.Marshal(feedback)
	if err != nil {
		return err
	}

	key := feedbackRecentKey(feedback.SessionID)

	pipe := c.rdb.Pipeline()
	pipe.ZAdd(ctx, key, redis.Z{Score: float64(feedback.TMs), Member: payload})
	// Keep only the most recent feedbackRecentMaxCount entries.
	pipe.ZRemRangeByRank(ctx, key, 0, int64(-feedbackRecentMaxCount-1))
	pipe.Expire(ctx, key, keyTTL)
	_, err = pipe.Exec(ctx)
	return err
}

// GetRecentFeedbackEvents returns the last `count` feedback_events for
// sessionID, newest first. Used by the Realtime Worker to include recent
// feedback history in the LLM evidence pack.
func (c *Client) GetRecentFeedbackEvents(ctx context.Context, sessionID string, count int) ([]contract.FeedbackEvent, error) {
	vals, err := c.rdb.ZRevRange(ctx, feedbackRecentKey(sessionID), 0, int64(count-1)).Result()
	if err != nil {
		return nil, err
	}

	events := make([]contract.FeedbackEvent, 0, len(vals))
	for _, v := range vals {
		var fb contract.FeedbackEvent
		if err := json.Unmarshal([]byte(v), &fb); err != nil {
			return nil, err
		}
		events = append(events, fb)
	}
	return events, nil
}

// feedbackCooldownKey is architecture.md's "feedback:cooldown:{session_id}":
// its mere presence (not its value) means the session is still within the
// cooldown window from the last feedback_event, per the "9. ... cooldown /
// LLM budget を確認する" step of リアルタイムFBフロー.
func feedbackCooldownKey(sessionID string) string {
	return fmt.Sprintf("feedback:cooldown:%s", sessionID)
}

// SetFeedbackCooldown starts (or restarts) session_id's feedback cooldown,
// expiring automatically after cooldownMs — the same duration reported to
// Chrome as feedback_event.cooldown_ms.
func (c *Client) SetFeedbackCooldown(ctx context.Context, sessionID string, cooldownMs int) error {
	return c.rdb.Set(ctx, feedbackCooldownKey(sessionID), "1", time.Duration(cooldownMs)*time.Millisecond).Err()
}

// InFeedbackCooldown reports whether session_id is still within its
// feedback cooldown window.
func (c *Client) InFeedbackCooldown(ctx context.Context, sessionID string) (bool, error) {
	n, err := c.rdb.Exists(ctx, feedbackCooldownKey(sessionID)).Result()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}
