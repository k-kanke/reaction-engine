package contract

import "encoding/json"

// UploadURLRequest is the body of POST /sessions/{session_id}/media/upload-url.
// architecture.md's two Upload URL request examples (画像フロー § Media API)
// diverge by purpose: baseline_frame carries AudienceID/TileID (a
// per-participant crop) while evidence_frame carries TriggerID instead (a
// room-level tab screenshot tied to the trigger that caused it, not to any
// one participant) — see validateUploadURLRequest in internal/media/server.go.
type UploadURLRequest struct {
	Purpose         string          `json:"purpose"`
	ContentType     string          `json:"content_type"`
	CaptureID       string          `json:"capture_id"`
	TMs             int64           `json:"t_ms"`
	AudienceID      string          `json:"audience_id,omitempty"`
	TileID          string          `json:"tile_id,omitempty"`
	TriggerID       string          `json:"trigger_id,omitempty"`
	FeatureSnapshot json.RawMessage `json:"feature_snapshot,omitempty"`
}

// UploadURLResponse is returned from the upload-url endpoint. Locally
// upload_url points back at this same service's /local-upload endpoint;
// in Cloud Run it will be a real Cloud Storage signed URL.
type UploadURLResponse struct {
	UploadURL string `json:"upload_url"`
	MediaRef  string `json:"media_ref"`
	CaptureID string `json:"capture_id"`
	ExpiresAt string `json:"expires_at"`
}

// MediaUploadedEventPayload is the local_events payload media-api publishes
// to the "media-analysis-events" topic once an uploaded capture frame is
// confirmed on disk. Shape matches architecture.md's Image Analysis Worker
// input contract.
type MediaUploadedEventPayload struct {
	EventID       string `json:"event_id"`
	Type          string `json:"type"`
	SchemaVersion int    `json:"schema_version"`
	SessionID     string `json:"session_id"`
	CaptureID     string `json:"capture_id"`
	AudienceID    string `json:"audience_id"`
	TileID        string `json:"tile_id,omitempty"`
	TMs           int64  `json:"t_ms"`
	MediaRef      string `json:"media_ref"`
	Purpose       string `json:"purpose"`
}
