package writer

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/k-kanke/reaction-engine/backend/internal/contract"
)

// Store persists transcript_chunk, trigger_event, and feedback_event rows
// to their local/Cloud SQL tables (migrations 000001_initial_schema and
// 000003_add_trigger_events).
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// EnsureSession upserts a minimal sessions row so transcripts/trigger_events/
// feedback_events (all FK on session_id) can be inserted before a dedicated
// Session API exists. Mirrors internal/media.PGStore.EnsureSession.
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

// InsertTriggerEvent inserts one trigger_events row (migration
// 000003_add_trigger_events, Step 1 of
// plan/mood-wave-contract-migration.md), deduped by event_id (same
// at-least-once concern as InsertTranscriptChunk).
func (s *Store) InsertTriggerEvent(ctx context.Context, t contract.TriggerEvent) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO trigger_events (event_id, session_id, trigger_id, type, source, t_ms, peak_t_ms, delta)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (event_id) DO NOTHING
	`,
		t.EventID,
		t.SessionID,
		t.TriggerID,
		t.Type,
		t.Source,
		t.TMs,
		t.PeakTMs,
		t.Delta,
	)
	return err
}

// InsertFeedbackEvent inserts one feedback_events row (migration
// 000001_initial_schema), deduped by event_id (same at-least-once concern
// as InsertTranscriptChunk). AudienceID/ModelVersion are nullable text
// columns; empty string on FeedbackEvent maps to NULL via NULLIF so a
// FeedbackEvent that never set them (the common mood_wave_sample-driven
// path) doesn't store empty strings where the schema means "absent".
func (s *Store) InsertFeedbackEvent(ctx context.Context, f contract.FeedbackEvent) error {
	var reasonCodes []byte
	if len(f.ReasonCodes) > 0 {
		var err error
		reasonCodes, err = json.Marshal(f.ReasonCodes)
		if err != nil {
			return err
		}
	}

	_, err := s.pool.Exec(ctx, `
		INSERT INTO feedback_events
			(event_id, session_id, audience_id, t_ms, feedback_type, severity, message,
			 reason_codes, evidence_quote, source, model_version, confidence, cooldown_ms)
		VALUES ($1, $2, NULLIF($3, ''), $4, $5, $6, $7, $8::jsonb, $9, $10, NULLIF($11, ''), $12, $13)
		ON CONFLICT (event_id) DO NOTHING
	`,
		f.EventID,
		f.SessionID,
		f.AudienceID,
		f.TMs,
		f.FeedbackType,
		f.Severity,
		f.Message,
		reasonCodes,
		f.EvidenceQuote,
		f.Source,
		f.ModelVersion,
		f.Confidence,
		f.CooldownMs,
	)
	return err
}
