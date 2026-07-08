package writer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"

	"cloud.google.com/go/storage"
	"google.golang.org/api/option"
)

// GCSJSONLStore is the JSONLStore backend used when JSONL_STORE_BACKEND=gcs
// (Step F of plan/gcp-deployment-runbook.md). Cloud Storage objects are
// immutable — there is no native append — so each Append stages the new
// line as a short-lived object, then uses the Compose API to fold it into
// the session's single growing part-0001.jsonl object (architecture.md's
// exact Cloud Storage layout) without re-uploading everything written so
// far. Compose always has exactly two sources (the current part-0001.jsonl
// + the newly staged line), so this stays cheap no matter how many lines
// have already accumulated — composing replaces the target in place, it
// doesn't grow the source count for the next call.
//
// Concurrency: this does not retry on a conflicting concurrent compose (no
// generation-precondition check). Durable Writer runs with
// max_instance_count=1 specifically so this is safe in practice (see
// plan/gcp-deployment-runbook.md Step H); Post-session Job/pdf-renderer
// only ever append within one --session-id's own job execution, so a
// concurrent writer to the same object would only happen if two operators
// manually re-ran the same job for the same session at the same moment.
type GCSJSONLStore struct {
	client *storage.Client
	bucket string
}

// NewGCSJSONLStore builds a GCSJSONLStore. credentialsFile is a downloaded
// service account JSON key for local verification against a real bucket;
// leave it empty on Cloud Run, where Application Default Credentials
// resolve to the attached runtime service account automatically — this
// store never signs URLs, so (unlike GCSMediaStore) it needs no IAM
// SignBlob fallback for the no-key-file case.
func NewGCSJSONLStore(ctx context.Context, bucket, credentialsFile string) (*GCSJSONLStore, error) {
	var opts []option.ClientOption
	if credentialsFile != "" {
		opts = append(opts, option.WithCredentialsFile(credentialsFile))
	}

	client, err := storage.NewClient(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("writer: create gcs client: %w", err)
	}

	return &GCSJSONLStore{client: client, bucket: bucket}, nil
}

func (s *GCSJSONLStore) objectPath(sessionID string, parts []string) string {
	segments := append([]string{"sessions", sessionID}, parts...)
	segments = append(segments, "part-0001.jsonl")
	return path.Join(segments...)
}

func (s *GCSJSONLStore) Append(ctx context.Context, sessionID, eventID string, payload any, parts ...string) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	body = append(body, '\n')

	bucket := s.client.Bucket(s.bucket)
	objectPath := s.objectPath(sessionID, parts)
	stagingPath := objectPath + ".append-" + eventID

	staging := bucket.Object(stagingPath)
	w := staging.NewWriter(ctx)
	if _, err := w.Write(body); err != nil {
		w.Close()
		return fmt.Errorf("writer: write staging object %q: %w", stagingPath, err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("writer: close staging object %q: %w", stagingPath, err)
	}
	defer bucket.Object(stagingPath).Delete(ctx) // best-effort cleanup; compose already copied the bytes

	target := bucket.Object(objectPath)
	if _, err := target.Attrs(ctx); errors.Is(err, storage.ErrObjectNotExist) {
		// First line for this session/parts: promote the staging object
		// directly. Compose requires the destination to already exist as
		// one of its own sources, so there's nothing to compose yet.
		if _, err := target.CopierFrom(staging).Run(ctx); err != nil {
			return fmt.Errorf("writer: copy first line to %q: %w", objectPath, err)
		}
		return nil
	} else if err != nil {
		return fmt.Errorf("writer: check target object %q: %w", objectPath, err)
	}

	composer := target.ComposerFrom(target, staging)
	if _, err := composer.Run(ctx); err != nil {
		return fmt.Errorf("writer: compose append to %q: %w", objectPath, err)
	}
	return nil
}

func (s *GCSJSONLStore) ReadAll(ctx context.Context, sessionID string, parts ...string) ([][]byte, error) {
	bucket := s.client.Bucket(s.bucket)
	objectPath := s.objectPath(sessionID, parts)

	r, err := bucket.Object(objectPath).NewReader(ctx)
	if errors.Is(err, storage.ErrObjectNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("writer: open object %q: %w", objectPath, err)
	}
	defer r.Close()

	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("writer: read object %q: %w", objectPath, err)
	}

	var lines [][]byte
	start := 0
	for i, b := range data {
		if b != '\n' {
			continue
		}
		if i > start {
			line := make([]byte, i-start)
			copy(line, data[start:i])
			lines = append(lines, line)
		}
		start = i + 1
	}
	return lines, nil
}
