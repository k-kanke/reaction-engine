package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/k-kanke/reaction-engine/backend/internal/contract"
)

const (
	recentWindow           = 60 * time.Second
	transcriptRecentWindow = 10 * time.Second
	keyTTL                 = time.Hour
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

func featuresRecentKey(sessionID, audienceID string) string {
	return fmt.Sprintf("features:recent:%s:%s", sessionID, audienceID)
}

// StoreRecentFeature appends a compact feature to the session/audience
// recent window (ZSET scored by t_ms), trims entries older than the
// window, and refreshes the key TTL.
func (c *Client) StoreRecentFeature(ctx context.Context, feature contract.CompactFeature) error {
	payload, err := json.Marshal(feature)
	if err != nil {
		return err
	}

	key := featuresRecentKey(feature.SessionID, feature.AudienceID)
	cutoff := feature.ServerReceivedAtMs - recentWindow.Milliseconds()

	pipe := c.rdb.Pipeline()
	pipe.ZAdd(ctx, key, redis.Z{Score: float64(feature.TMs), Member: payload})
	pipe.ZRemRangeByScore(ctx, key, "-inf", fmt.Sprintf("%d", cutoff))
	pipe.Expire(ctx, key, keyTTL)
	_, err = pipe.Exec(ctx)
	return err
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

func baselineKey(sessionID, audienceID string) string {
	return fmt.Sprintf("session:baseline:%s:%s", sessionID, audienceID)
}

func visualSummaryKey(sessionID, audienceID string) string {
	return fmt.Sprintf("session:visual_summary:%s:%s", sessionID, audienceID)
}

func baselineStatusKey(sessionID, audienceID string) string {
	return fmt.Sprintf("session:baseline_status:%s:%s", sessionID, audienceID)
}

// SetBaselineReady caches a participant's baseline and visual summary and
// marks baseline_status "ready", per
// plan/backend-local-docker-runbook.md Phase 8. Gateway feedback logic
// (Phase 9+) reads these to apply baseline-aware corrections once ready;
// until then it should treat the participant as warming_up.
func (c *Client) SetBaselineReady(ctx context.Context, sessionID, audienceID string, baseline, visualSummary []byte) error {
	pipe := c.rdb.Pipeline()
	pipe.Set(ctx, baselineKey(sessionID, audienceID), baseline, keyTTL)
	pipe.Set(ctx, visualSummaryKey(sessionID, audienceID), visualSummary, keyTTL)
	pipe.Set(ctx, baselineStatusKey(sessionID, audienceID), "ready", keyTTL)
	_, err := pipe.Exec(ctx)
	return err
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
