package postsession

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ParticipantBaseline mirrors the columns of one participant_baselines row
// Post-session Job needs.
type ParticipantBaseline struct {
	AudienceID  string
	Baseline    json.RawMessage
	SampleCount int
	Confidence  float64
}

// VisualSummary mirrors the columns of one visual_summaries row.
type VisualSummary struct {
	AudienceID    string
	CaptureID     string
	VisualSummary json.RawMessage
	Confidence    *float64
}

// Transcript mirrors the columns of one transcripts row.
type Transcript struct {
	Speaker    string
	TStartMs   int64
	TEndMs     int64
	Text       string
	Confidence *float64
	IsFinal    bool
}

// SignalSummary mirrors the columns of one signal_summaries row. Nothing
// writes to this table yet as of Phase 13 (the real system-computation
// signal_summary layer is still aspirational per architecture.md), so this
// will read back empty for every session today; Post-session Job still
// reads it so it picks summaries up automatically once a later phase
// starts writing them.
type SignalSummary struct {
	AudienceID string
	TMs        int64
	Summary    json.RawMessage
}

// PGStore is the pgx-backed persistence boundary Post-session Job (Phase
// 13 of plan/backend-local-docker-runbook.md) reads its Postgres inputs
// from and writes the generated report to.
type PGStore struct {
	pool *pgxpool.Pool
}

func NewPGStore(pool *pgxpool.Pool) *PGStore {
	return &PGStore{pool: pool}
}

// EnsureSession upserts a minimal sessions row so a report (FK on
// session_id) can be inserted even for a session that never went through
// media-api/writer's own EnsureSession calls. Mirrors
// internal/media.PGStore.EnsureSession.
func (s *PGStore) EnsureSession(ctx context.Context, sessionID string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO sessions (session_id, meeting_provider)
		VALUES ($1, 'unknown')
		ON CONFLICT (session_id) DO NOTHING
	`, sessionID)
	return err
}

func (s *PGStore) ListParticipantBaselines(ctx context.Context, sessionID string) ([]ParticipantBaseline, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT audience_id, baseline, sample_count, confidence
		FROM participant_baselines
		WHERE session_id = $1
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ParticipantBaseline
	for rows.Next() {
		var b ParticipantBaseline
		if err := rows.Scan(&b.AudienceID, &b.Baseline, &b.SampleCount, &b.Confidence); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *PGStore) ListVisualSummaries(ctx context.Context, sessionID string) ([]VisualSummary, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT audience_id, capture_id, visual_summary, confidence
		FROM visual_summaries
		WHERE session_id = $1
		ORDER BY created_at
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []VisualSummary
	for rows.Next() {
		var v VisualSummary
		var captureID *string
		if err := rows.Scan(&v.AudienceID, &captureID, &v.VisualSummary, &v.Confidence); err != nil {
			return nil, err
		}
		if captureID != nil {
			v.CaptureID = *captureID
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *PGStore) ListTranscripts(ctx context.Context, sessionID string) ([]Transcript, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT speaker, t_start_ms, t_end_ms, text, confidence, is_final
		FROM transcripts
		WHERE session_id = $1
		ORDER BY t_start_ms
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Transcript
	for rows.Next() {
		var t Transcript
		if err := rows.Scan(&t.Speaker, &t.TStartMs, &t.TEndMs, &t.Text, &t.Confidence, &t.IsFinal); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *PGStore) ListSignalSummaries(ctx context.Context, sessionID string) ([]SignalSummary, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT audience_id, t_ms, summary
		FROM signal_summaries
		WHERE session_id = $1
		ORDER BY t_ms
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SignalSummary
	for rows.Next() {
		var summary SignalSummary
		if err := rows.Scan(&summary.AudienceID, &summary.TMs, &summary.Summary); err != nil {
			return nil, err
		}
		out = append(out, summary)
	}
	return out, rows.Err()
}

// InsertReport inserts one reports row and returns its generated id.
func (s *PGStore) InsertReport(ctx context.Context, sessionID string, report json.RawMessage, generatedAt time.Time) (string, error) {
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO reports (session_id, report, generated_at)
		VALUES ($1, $2::jsonb, $3)
		RETURNING id
	`, sessionID, []byte(report), generatedAt).Scan(&id)
	return id, err
}
