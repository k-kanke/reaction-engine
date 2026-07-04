package media

import (
	"fmt"
	"path/filepath"
	"time"
)

var contentTypeExtensions = map[string]string{
	"image/webp": "webp",
	"image/jpeg": "jpg",
	"image/png":  "png",
}

// extensionForContentType returns the file extension used for local storage
// and reports whether the content type is supported.
func extensionForContentType(contentType string) (string, bool) {
	ext, ok := contentTypeExtensions[contentType]
	return ext, ok
}

// mediaRef builds the local:// reference stored in capture_snapshots /
// media_refs. In Cloud Run this becomes a gs:// object path instead.
func mediaRef(sessionID, captureID, ext string) string {
	return fmt.Sprintf("local://sessions/%s/baseline/frames/%s.%s", sessionID, captureID, ext)
}

// localUploadURL builds the URL the Chrome extension PUTs the file to. It
// points back at this same media-api instance's /local-upload endpoint.
func localUploadURL(publicBaseURL, sessionID, captureID, ext string) string {
	return fmt.Sprintf("%s/local-upload/%s/%s.%s", publicBaseURL, sessionID, captureID, ext)
}

func expiresAt(ttl time.Duration, now time.Time) string {
	return now.Add(ttl).UTC().Format(time.RFC3339)
}

// localFilePath returns the on-disk path handleLocalUpload stores (and
// handleUploadComplete later verifies) a capture frame at:
// {mediaDir}/sessions/{sessionID}/baseline/frames/{captureID}.{ext}. This
// matches the local:// path mediaRef encodes (local://sessions/{sessionID}/baseline/frames/{captureID}.{ext})
// so media_ref actually points at where the file lives on disk instead of a
// different, flatter layout.
func localFilePath(mediaDir, sessionID, captureID, ext string) string {
	return filepath.Join(mediaDir, "sessions", sessionID, "baseline", "frames", fmt.Sprintf("%s.%s", captureID, ext))
}
