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

// MediaStore is the full boundary media-api's Handler depends on: issuing
// upload URLs and, at upload-complete time, confirming the object exists.
type MediaStore interface {
	MediaUploader
	MediaReader
}
