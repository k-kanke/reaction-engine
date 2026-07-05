package writer

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/k-kanke/reaction-engine/backend/internal/contract"
)

// Store persists transcript_chunk and decision_log rows to the
// local/Cloud SQL `transcripts` and `decision_logs` tables (migration
// 000001_initial_schema), per Phase 11/12 of
// plan/backend-local-docker-runbook.md.
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// EnsureSession upserts a minimal sessions row so transcripts/decision_logs
// (both FK on session_id) can be inserted before a dedicated Session API
// exists. Mirrors internal/media.PGStore.EnsureSession.
func (s *Store) EnsureSession(ctx context.Context, sessionID string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO sessions (session_id, meeting_provider)
		VALUES ($1, 'unknown')
		ON CONFLICT (session_id) DO NOTHING
	`, sessionID)
	return err
}

// InsertTranscriptChunk inserts one transcripts row, deduped by event_id
// so an unacked local_event reprocessed after a restart doesn't create a
// duplicate row (same at-least-once concern as compact raw features).
func (s *Store) InsertTranscriptChunk(ctx context.Context, chunk contract.TranscriptChunk) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO transcripts (event_id, session_id, speaker, t_start_ms, t_end_ms, text, confidence, is_final)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (event_id) DO NOTHING
	`,
		chunk.EventID,
		chunk.SessionID,
		chunk.Speaker,
		chunk.TStartMs,
		chunk.TEndMs,
		chunk.Text,
		chunk.Confidence,
		chunk.IsFinal,
	)
	return err
}

// InsertDecisionLog inserts one decision_logs row, deduped by event_id
// (same at-least-once concern as InsertTranscriptChunk).
func (s *Store) InsertDecisionLog(ctx context.Context, d contract.DecisionLog) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO decision_logs (event_id, session_id, audience_id, t_ms, source, decision)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb)
		ON CONFLICT (event_id) DO NOTHING
	`,
		d.EventID,
		d.SessionID,
		d.AudienceID,
		d.TMs,
		d.Source,
		[]byte(d.Decision),
	)
	return err
}
