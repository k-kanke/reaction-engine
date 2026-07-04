package imageanalysis

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrCaptureNotFound is returned by Store.GetCaptureSnapshot when no
// capture_snapshots row exists for the given session_id + capture_id.
var ErrCaptureNotFound = errors.New("imageanalysis: capture not found")

// CaptureData is the subset of a capture_snapshots row the worker needs.
type CaptureData struct {
	SessionID       string
	CaptureID       string
	AudienceID      string
	MediaRef        string
	FeatureSnapshot json.RawMessage
}

// ParticipantBaseline mirrors a row of participant_baselines.
type ParticipantBaseline struct {
	Baseline    json.RawMessage
	SampleCount int
	Confidence  float64
}

// Store is the persistence boundary the worker depends on. It is an
// interface so orchestration logic can be unit tested without a real
// Postgres.
type Store interface {
	GetCaptureSnapshot(ctx context.Context, sessionID, captureID string) (CaptureData, error)
	// GetParticipantBaseline reports found=false (no error) if no row
	// exists yet for the session_id + audience_id.
	GetParticipantBaseline(ctx context.Context, sessionID, audienceID string) (baseline ParticipantBaseline, found bool, err error)
	UpsertParticipantBaseline(ctx context.Context, sessionID, audienceID string, baseline json.RawMessage, sampleCount int, confidence float64) error
	InsertVisualSummary(ctx context.Context, sessionID, audienceID, captureID, mediaRef string, visualSummary json.RawMessage, confidence float64) error
}

// PGStore is the pgx-backed Store implementation used outside tests.
type PGStore struct {
	pool *pgxpool.Pool
}

func NewPGStore(pool *pgxpool.Pool) *PGStore {
	return &PGStore{pool: pool}
}

func (s *PGStore) GetCaptureSnapshot(ctx context.Context, sessionID, captureID string) (CaptureData, error) {
	var data CaptureData
	err := s.pool.QueryRow(ctx, `
		SELECT session_id, capture_id, audience_id, media_ref, feature_snapshot
		FROM capture_snapshots
		WHERE session_id = $1 AND capture_id = $2
	`, sessionID, captureID).Scan(
		&data.SessionID,
		&data.CaptureID,
		&data.AudienceID,
		&data.MediaRef,
		&data.FeatureSnapshot,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return CaptureData{}, ErrCaptureNotFound
	}
	if err != nil {
		return CaptureData{}, err
	}
	return data, nil
}

func (s *PGStore) GetParticipantBaseline(ctx context.Context, sessionID, audienceID string) (ParticipantBaseline, bool, error) {
	var b ParticipantBaseline
	err := s.pool.QueryRow(ctx, `
		SELECT baseline, sample_count, confidence
		FROM participant_baselines
		WHERE session_id = $1 AND audience_id = $2
	`, sessionID, audienceID).Scan(&b.Baseline, &b.SampleCount, &b.Confidence)
	if errors.Is(err, pgx.ErrNoRows) {
		return ParticipantBaseline{}, false, nil
	}
	if err != nil {
		return ParticipantBaseline{}, false, err
	}
	return b, true, nil
}

func (s *PGStore) UpsertParticipantBaseline(ctx context.Context, sessionID, audienceID string, baseline json.RawMessage, sampleCount int, confidence float64) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO participant_baselines (session_id, audience_id, baseline, sample_count, confidence, updated_at)
		VALUES ($1, $2, $3::jsonb, $4, $5, now())
		ON CONFLICT (session_id, audience_id) DO UPDATE SET
			baseline = EXCLUDED.baseline,
			sample_count = EXCLUDED.sample_count,
			confidence = EXCLUDED.confidence,
			updated_at = now()
	`, sessionID, audienceID, []byte(baseline), sampleCount, confidence)
	return err
}

func (s *PGStore) InsertVisualSummary(ctx context.Context, sessionID, audienceID, captureID, mediaRef string, visualSummary json.RawMessage, confidence float64) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO visual_summaries (session_id, audience_id, capture_id, media_ref, visual_summary, confidence, created_at)
		VALUES ($1, $2, $3, $4, $5::jsonb, $6, now())
	`, sessionID, audienceID, captureID, mediaRef, []byte(visualSummary), confidence)
	return err
}
