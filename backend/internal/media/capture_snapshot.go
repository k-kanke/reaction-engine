package media

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrCaptureNotFound is returned by Store.GetCapture when no
// capture_snapshots/media_refs row exists for the given session_id +
// capture_id.
var ErrCaptureNotFound = errors.New("media: capture not found")

// CaptureSnapshot mirrors a row of the capture_snapshots table.
type CaptureSnapshot struct {
	CaptureID       string
	SessionID       string
	AudienceID      string
	TileID          string // empty means NULL
	TMs             int64
	MediaRef        string
	UploadStatus    string
	FeatureSnapshot json.RawMessage
	// TriggerID links an evidence_frame capture to the trigger that caused
	// it (architecture.md's mood_wave_sample.trigger.trigger_id). Empty
	// (NULL) for baseline_frame captures, which are not trigger-driven.
	TriggerID string
}

// MediaRef mirrors a row of the media_refs table.
type MediaRef struct {
	SessionID    string
	CaptureID    string
	MediaRef     string
	Purpose      string
	ContentType  string
	UploadStatus string
	// TriggerID mirrors CaptureSnapshot.TriggerID; empty (NULL) for
	// baseline_frame media_refs.
	TriggerID string
}

// CaptureRecord is the joined capture_snapshots + media_refs view returned
// by Store.GetCapture, used by the upload-complete handler (Phase 7.3) to
// locate the uploaded file and build the media_uploaded event.
type CaptureRecord struct {
	SessionID    string
	CaptureID    string
	AudienceID   string
	TileID       string // empty means NULL
	TMs          int64
	MediaRef     string
	ContentType  string
	Purpose      string
	UploadStatus string
	TriggerID    string // empty means NULL
}

// Store is the persistence boundary the media-api handlers depend on. It is
// an interface so handler logic can be unit tested without a real Postgres.
type Store interface {
	// EnsureSession upserts a minimal sessions row so that capture_snapshots
	// and media_refs (both FK-constrained on session_id) can be inserted
	// before a dedicated Session API exists.
	EnsureSession(ctx context.Context, sessionID string) error
	InsertCaptureSnapshot(ctx context.Context, snapshot CaptureSnapshot) error
	InsertMediaRef(ctx context.Context, ref MediaRef) error
	// GetCapture returns ErrCaptureNotFound if no matching row exists.
	GetCapture(ctx context.Context, sessionID, captureID string) (CaptureRecord, error)
	// MarkUploaded sets upload_status = 'uploaded' (+ uploaded_at) on both
	// capture_snapshots and media_refs for the given capture.
	MarkUploaded(ctx context.Context, sessionID, captureID string, uploadedAt time.Time) error
}

// EventPublisher is the local event bus boundary (Phase 5's
// db.LocalEventStore in production) media-api publishes media_uploaded
// events to.
type EventPublisher interface {
	Enqueue(ctx context.Context, topic, eventID string, payload any) error
}

// PGStore is the pgx-backed Store implementation used outside tests.
type PGStore struct {
	pool *pgxpool.Pool
}

func NewPGStore(pool *pgxpool.Pool) *PGStore {
	return &PGStore{pool: pool}
}

func (s *PGStore) EnsureSession(ctx context.Context, sessionID string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO sessions (session_id, meeting_provider)
		VALUES ($1, 'unknown')
		ON CONFLICT (session_id) DO NOTHING
	`, sessionID)
	return err
}

func (s *PGStore) InsertCaptureSnapshot(ctx context.Context, snapshot CaptureSnapshot) error {
	var tileID *string
	if snapshot.TileID != "" {
		tileID = &snapshot.TileID
	}
	var triggerID *string
	if snapshot.TriggerID != "" {
		triggerID = &snapshot.TriggerID
	}

	featureSnapshot := snapshot.FeatureSnapshot
	if len(featureSnapshot) == 0 {
		featureSnapshot = json.RawMessage("{}")
	}

	_, err := s.pool.Exec(ctx, `
		INSERT INTO capture_snapshots
			(capture_id, session_id, audience_id, tile_id, t_ms, media_ref, upload_status, feature_snapshot, trigger_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, $9)
	`,
		snapshot.CaptureID,
		snapshot.SessionID,
		snapshot.AudienceID,
		tileID,
		snapshot.TMs,
		snapshot.MediaRef,
		snapshot.UploadStatus,
		[]byte(featureSnapshot),
		triggerID,
	)
	return err
}

func (s *PGStore) InsertMediaRef(ctx context.Context, ref MediaRef) error {
	var triggerID *string
	if ref.TriggerID != "" {
		triggerID = &ref.TriggerID
	}

	_, err := s.pool.Exec(ctx, `
		INSERT INTO media_refs
			(session_id, capture_id, media_ref, purpose, content_type, upload_status, trigger_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`,
		ref.SessionID,
		ref.CaptureID,
		ref.MediaRef,
		ref.Purpose,
		ref.ContentType,
		ref.UploadStatus,
		triggerID,
	)
	return err
}

func (s *PGStore) GetCapture(ctx context.Context, sessionID, captureID string) (CaptureRecord, error) {
	var rec CaptureRecord
	var tileID *string
	var triggerID *string

	err := s.pool.QueryRow(ctx, `
		SELECT cs.session_id, cs.capture_id, cs.audience_id, cs.tile_id, cs.t_ms,
		       cs.media_ref, mr.content_type, mr.purpose, cs.upload_status, cs.trigger_id
		FROM capture_snapshots cs
		JOIN media_refs mr ON mr.capture_id = cs.capture_id AND mr.session_id = cs.session_id
		WHERE cs.session_id = $1 AND cs.capture_id = $2
	`, sessionID, captureID).Scan(
		&rec.SessionID,
		&rec.CaptureID,
		&rec.AudienceID,
		&tileID,
		&rec.TMs,
		&rec.MediaRef,
		&rec.ContentType,
		&rec.Purpose,
		&rec.UploadStatus,
		&triggerID,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return CaptureRecord{}, ErrCaptureNotFound
	}
	if err != nil {
		return CaptureRecord{}, err
	}
	if tileID != nil {
		rec.TileID = *tileID
	}
	if triggerID != nil {
		rec.TriggerID = *triggerID
	}
	return rec, nil
}

func (s *PGStore) MarkUploaded(ctx context.Context, sessionID, captureID string, uploadedAt time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		UPDATE capture_snapshots SET upload_status = 'uploaded', uploaded_at = $3
		WHERE session_id = $1 AND capture_id = $2
	`, sessionID, captureID, uploadedAt); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `
		UPDATE media_refs SET upload_status = 'uploaded', uploaded_at = $3
		WHERE session_id = $1 AND capture_id = $2
	`, sessionID, captureID, uploadedAt); err != nil {
		return err
	}

	return tx.Commit(ctx)
}
