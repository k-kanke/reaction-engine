package db

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// LocalEvent is a row of the local_events table: the local (pre-GCP)
// stand-in for a Pub/Sub message, per
// plan/backend-local-docker-runbook.md Phase 5.
type LocalEvent struct {
	ID          int64
	Topic       string
	EventID     string
	Payload     json.RawMessage
	AvailableAt time.Time
	CreatedAt   time.Time
	AckedAt     *time.Time
}

type LocalEventStore struct {
	pool *pgxpool.Pool
}

func NewLocalEventStore(pool *pgxpool.Pool) *LocalEventStore {
	return &LocalEventStore{pool: pool}
}

// Enqueue writes a new unacked event for the given topic.
func (s *LocalEventStore) Enqueue(ctx context.Context, topic, eventID string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	_, err = s.pool.Exec(ctx,
		`INSERT INTO local_events (topic, event_id, payload) VALUES ($1, $2, $3)`,
		topic, eventID, body,
	)
	return err
}

// Ack marks an event as processed. It is a no-op (idempotent) if the event
// is already acked.
func (s *LocalEventStore) Ack(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE local_events SET acked_at = now() WHERE id = $1 AND acked_at IS NULL`,
		id,
	)
	return err
}

// FetchUnacked returns up to limit unacked, available events for a topic,
// oldest first.
func (s *LocalEventStore) FetchUnacked(ctx context.Context, topic string, limit int) ([]LocalEvent, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, topic, event_id, payload, available_at, created_at, acked_at
		 FROM local_events
		 WHERE topic = $1 AND acked_at IS NULL AND available_at <= now()
		 ORDER BY id
		 LIMIT $2`,
		topic, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []LocalEvent
	for rows.Next() {
		var e LocalEvent
		if err := rows.Scan(&e.ID, &e.Topic, &e.EventID, &e.Payload, &e.AvailableAt, &e.CreatedAt, &e.AckedAt); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}
