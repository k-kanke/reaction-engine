package media

import (
	"context"
	"encoding/json"

	"github.com/k-kanke/reaction-engine/backend/internal/db"
)

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
}

// MediaRef mirrors a row of the media_refs table.
type MediaRef struct {
	SessionID    string
	CaptureID    string
	MediaRef     string
	Purpose      string
	ContentType  string
	UploadStatus string
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
}

// PGStore is the pgx-backed Store implementation used outside tests.
type PGStore struct {
	client *db.Client
}

func NewPGStore(client *db.Client) *PGStore {
	return &PGStore{client: client}
}

func (s *PGStore) EnsureSession(ctx context.Context, sessionID string) error {
	_, err := s.client.Pool.Exec(ctx, `
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

	featureSnapshot := snapshot.FeatureSnapshot
	if len(featureSnapshot) == 0 {
		featureSnapshot = json.RawMessage("{}")
	}

	_, err := s.client.Pool.Exec(ctx, `
		INSERT INTO capture_snapshots
			(capture_id, session_id, audience_id, tile_id, t_ms, media_ref, upload_status, feature_snapshot)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb)
	`,
		snapshot.CaptureID,
		snapshot.SessionID,
		snapshot.AudienceID,
		tileID,
		snapshot.TMs,
		snapshot.MediaRef,
		snapshot.UploadStatus,
		[]byte(featureSnapshot),
	)
	return err
}

func (s *PGStore) InsertMediaRef(ctx context.Context, ref MediaRef) error {
	_, err := s.client.Pool.Exec(ctx, `
		INSERT INTO media_refs
			(session_id, capture_id, media_ref, purpose, content_type, upload_status)
		VALUES ($1, $2, $3, $4, $5, $6)
	`,
		ref.SessionID,
		ref.CaptureID,
		ref.MediaRef,
		ref.Purpose,
		ref.ContentType,
		ref.UploadStatus,
	)
	return err
}
