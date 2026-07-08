package media

import "context"

// MediaUploader lets a caller obtain an upload destination for a baseline
// frame without knowing whether bytes end up on local disk or in Cloud
// Storage. LocalMediaStore and GCSMediaStore are the two implementations
// (plan/gcp-adapter-migration-phase14.md Step 14-1).
type MediaUploader interface {
	// SignedUploadURL returns the URL the Chrome extension PUTs raw bytes
	// to, the media_ref to persist in capture_snapshots/media_refs, and the
	// URL's expiry (RFC3339, UTC) for the API response.
	SignedUploadURL(ctx context.Context, sessionID, captureID, contentType, ext string) (uploadURL, mediaRef, expiresAt string, err error)
}

// MediaReader lets a caller confirm an uploaded frame exists and fetch its
// bytes, independent of storage backend. Image Analysis Worker only needs
// this half of MediaStore.
type MediaReader interface {
	Exists(ctx context.Context, mediaRef string) (bool, error)
	Read(ctx context.Context, mediaRef string) ([]byte, error)
}

// MediaWriter lets a server-side caller that already holds real storage
// credentials (unlike a Chrome extension client, which only ever gets a
// signed URL) write bytes directly, with no signed-URL/client-PUT round
// trip. Added in Step F of plan/gcp-deployment-runbook.md for
// cmd/pdf-renderer, which previously wrote report.pdf straight to local
// disk via os.WriteFile, bypassing this package's Local/GCS split entirely
// — a bug once pdf-renderer and the service that reads report.pdf back run
// as separate Cloud Run instances with no shared filesystem.
type MediaWriter interface {
	// Write uploads data under sessions/{sessionID}/{parts...} (parts
	// joined as path segments, the last one being the filename) and
	// returns the resulting media_ref — mirroring SignedUploadURL's
	// (sessionID, captureID, contentType, ext) -> (uploadURL, mediaRef,
	// expiresAt) shape, minus the signed-URL indirection this caller
	// doesn't need.
	Write(ctx context.Context, sessionID string, parts []string, data []byte, contentType string) (mediaRef string, err error)
}

// MediaStore is the full boundary media-api's Handler depends on: issuing
// upload URLs and, at upload-complete time, confirming the object exists.
type MediaStore interface {
	MediaUploader
	MediaReader
}
