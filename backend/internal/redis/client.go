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
	recentWindow = 60 * time.Second
	keyTTL       = time.Hour
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
