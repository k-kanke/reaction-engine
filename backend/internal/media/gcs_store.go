package media

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	"google.golang.org/api/option"
)

// GCSMediaStore is the MediaStore backend for MEDIA_STORE_BACKEND=gcs
// (plan/gcp-adapter-migration-phase14.md Step 14-1). media_ref uses the
// gs:// scheme (architecture.md's Media API contract) instead of
// LocalMediaStore's local://. Signed upload URLs point directly at Cloud
// Storage; media-api never sees the uploaded bytes, so the /local-upload
// route is unused with this backend.
type GCSMediaStore struct {
	client     *storage.Client
	bucket     string
	ttl        time.Duration
	now        func() time.Time
	accessID   string
	privateKey []byte
}

type gcsCredentialsFile struct {
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
}

// NewGCSMediaStore builds a GCSMediaStore. credentialsFile is a downloaded
// service account JSON key, used both to authenticate the Cloud Storage
// client and to sign V4 upload URLs (GoogleAccessID/PrivateKey). This is
// intended for local verification against a real bucket per the Step 14-1
// runbook; on Cloud Run, signing should instead go through the attached
// service account's IAM SignBlob permission (no key file), which this
// constructor does not implement yet.
func NewGCSMediaStore(ctx context.Context, bucket, credentialsFile string, ttl time.Duration) (*GCSMediaStore, error) {
	var opts []option.ClientOption
	if credentialsFile != "" {
		opts = append(opts, option.WithCredentialsFile(credentialsFile))
	}

	client, err := storage.NewClient(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("media: create gcs client: %w", err)
	}

	store := &GCSMediaStore{client: client, bucket: bucket, ttl: ttl, now: time.Now}

	if credentialsFile != "" {
		keyData, err := os.ReadFile(credentialsFile)
		if err != nil {
			return nil, fmt.Errorf("media: read gcs credentials file: %w", err)
		}
		var sa gcsCredentialsFile
		if err := json.Unmarshal(keyData, &sa); err != nil {
			return nil, fmt.Errorf("media: parse gcs credentials file: %w", err)
		}
		store.accessID = sa.ClientEmail
		store.privateKey = []byte(sa.PrivateKey)
	}

	return store, nil
}

func (s *GCSMediaStore) object(sessionID, captureID, ext string) string {
	return fmt.Sprintf("sessions/%s/baseline/frames/%s.%s", sessionID, captureID, ext)
}

func (s *GCSMediaStore) SignedUploadURL(ctx context.Context, sessionID, captureID, contentType, ext string) (string, string, string, error) {
	object := s.object(sessionID, captureID, ext)
	expires := s.now().Add(s.ttl)

	url, err := storage.SignedURL(s.bucket, object, &storage.SignedURLOptions{
		GoogleAccessID: s.accessID,
		PrivateKey:     s.privateKey,
		Method:         http.MethodPut,
		Expires:        expires,
		ContentType:    contentType,
		Scheme:         storage.SigningSchemeV4,
	})
	if err != nil {
		return "", "", "", fmt.Errorf("media: sign upload url: %w", err)
	}

	ref := fmt.Sprintf("gs://%s/%s", s.bucket, object)
	return url, ref, expires.UTC().Format(time.RFC3339), nil
}

func (s *GCSMediaStore) Exists(ctx context.Context, mediaRef string) (bool, error) {
	object, err := s.objectFromRef(mediaRef)
	if err != nil {
		return false, err
	}

	_, err = s.client.Bucket(s.bucket).Object(object).Attrs(ctx)
	if errors.Is(err, storage.ErrObjectNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("media: stat gcs object %q: %w", object, err)
	}
	return true, nil
}

func (s *GCSMediaStore) Read(ctx context.Context, mediaRef string) ([]byte, error) {
	object, err := s.objectFromRef(mediaRef)
	if err != nil {
		return nil, err
	}

	r, err := s.client.Bucket(s.bucket).Object(object).NewReader(ctx)
	if err != nil {
		return nil, fmt.Errorf("media: open gcs object %q: %w", object, err)
	}
	defer r.Close()

	return io.ReadAll(r)
}

// objectFromRef strips this store's gs://{bucket}/ prefix off a media_ref to
// recover the object key. It errors instead of guessing if the ref belongs
// to a different bucket, since that would only happen from a config change
// or a bug upstream.
func (s *GCSMediaStore) objectFromRef(mediaRef string) (string, error) {
	prefix := fmt.Sprintf("gs://%s/", s.bucket)
	if !strings.HasPrefix(mediaRef, prefix) {
		return "", fmt.Errorf("media: media_ref %q does not belong to bucket %q", mediaRef, s.bucket)
	}
	return strings.TrimPrefix(mediaRef, prefix), nil
}
