// Package pubsub is the real Google Cloud Pub/Sub boundary between
// gateway/media-api (publishers) and writer/image-analysis-worker (push
// subscribers). It replaces internal/db.LocalEventStore -- the
// Postgres-table stand-in for a message bus -- in production
// (EVENT_BUS_BACKEND=pubsub); local dev keeps using LocalEventStore
// (EVENT_BUS_BACKEND=local, the default) so docker-compose workflows are
// unaffected.
//
// The switch away from LocalEventStore in production was driven by two
// problems only visible once writer/image-analysis-worker were actually
// deployed (plan/post-session-report-implementation.md Steps 1-2): polling
// Postgres every 2s forced both services to stay pinned at
// min_instance_count=1 (always billed, even with zero live sessions), and
// under a real session's write volume the JSONL append path repeatedly hit
// GCS's per-object mutation rate limit (see internal/writer/gcs_store.go).
// Pub/Sub push lets both services scale to zero when idle, and its
// subscription-level retry/backoff absorbs transient failures like that
// GCS 429 instead of the poll loop's fixed 2s retry cadence.
package pubsub

import (
	"context"
	"encoding/json"
	"fmt"

	"cloud.google.com/go/pubsub/v2"
)

// Publisher publishes to a real Pub/Sub topic. Its Enqueue method matches
// db.LocalEventStore's shape exactly (see internal/media.EventPublisher and
// internal/gateway.EventPublisher), so gateway/media-api can swap between
// the two with no other code change.
type Publisher struct {
	client *pubsub.Client
}

// NewPublisher dials Pub/Sub using Application Default Credentials --
// gateway/media-api's own Cloud Run runtime identity, which Terraform
// grants roles/pubsub.publisher scoped to just the topics it needs (see
// infra/modules/pubsub).
func NewPublisher(ctx context.Context, projectID string) (*Publisher, error) {
	client, err := pubsub.NewClient(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("pubsub: new client: %w", err)
	}
	return &Publisher{client: client}, nil
}

// Enqueue publishes payload as one message to topic, waiting for Pub/Sub to
// confirm the publish before returning (matching LocalEventStore.Enqueue's
// synchronous-write semantics, so callers don't need to know which
// implementation they're using).
func (p *Publisher) Enqueue(ctx context.Context, topic, eventID string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("pubsub: marshal payload: %w", err)
	}

	result := p.client.Publisher(topic).Publish(ctx, &pubsub.Message{
		Data:       body,
		Attributes: map[string]string{"event_id": eventID},
	})
	if _, err := result.Get(ctx); err != nil {
		return fmt.Errorf("pubsub: publish to %s: %w", topic, err)
	}
	return nil
}
