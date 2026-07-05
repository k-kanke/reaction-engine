package media

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// LocalMediaStore is the MediaStore backend used when
// MEDIA_STORE_BACKEND=local (the default, and the only backend before
// plan/gcp-adapter-migration-phase14.md Step 14-1). It stands in for Cloud
// Storage: "signed" upload URLs point back at this same media-api
// instance's /local-upload endpoint (Handler.handleLocalUpload), and
// media_ref uses a local:// scheme instead of gs://.
type LocalMediaStore struct {
	Dir           string
	PublicBaseURL string
	TTL           time.Duration
	Now           func() time.Time
}

func NewLocalMediaStore(dir, publicBaseURL string, ttl time.Duration) *LocalMediaStore {
	return &LocalMediaStore{Dir: dir, PublicBaseURL: publicBaseURL, TTL: ttl, Now: time.Now}
}

func (s *LocalMediaStore) SignedUploadURL(ctx context.Context, sessionID, captureID, contentType, ext string) (string, string, string, error) {
	ref := mediaRef(sessionID, captureID, ext)
	uploadURL := localUploadURL(s.PublicBaseURL, sessionID, captureID, ext)
	return uploadURL, ref, expiresAt(s.TTL, s.Now()), nil
}

func (s *LocalMediaStore) Exists(ctx context.Context, mediaRef string) (bool, error) {
	_, err := os.Stat(s.path(mediaRef))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (s *LocalMediaStore) Read(ctx context.Context, mediaRef string) ([]byte, error) {
	return os.ReadFile(s.path(mediaRef))
}

// path resolves a local:// media_ref (e.g.
// local://sessions/{session_id}/baseline/frames/{capture_id}.webp) to the
// on-disk path under Dir. handleLocalUpload writes to exactly this layout,
// so trimming the scheme is enough; there's no separate path to rebuild.
func (s *LocalMediaStore) path(mediaRef string) string {
	return filepath.Join(s.Dir, strings.TrimPrefix(mediaRef, "local://"))
}
