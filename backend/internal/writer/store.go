package writer

import "context"

// JSONLStore is the JSONL persistence boundary Durable Writer / Post-session
// Job depend on, so the local (Docker Compose) and GCS backends can be
// swapped via JSONL_STORE_BACKEND, mirroring internal/media.MediaStore's
// Local/GCS split (plan/gcp-adapter-migration-phase14.md Step 14-1). Added
// in Step F of plan/gcp-deployment-runbook.md: Cloud Run instances don't
// share a filesystem, so Durable Writer writing to local disk was invisible
// to Post-session Job/pdf-renderer running as separate instances.
type JSONLStore interface {
	// Append adds one JSON-encoded line to
	// sessions/{sessionID}/{parts...}/part-0001.jsonl (architecture.md's
	// exact Cloud Storage layout). eventID must be unique per call —
	// GCSJSONLStore uses it to name a short-lived staging object so
	// concurrent appends from different event_ids never collide on the
	// same object name.
	Append(ctx context.Context, sessionID, eventID string, payload any, parts ...string) error
	// ReadAll returns every line previously Appended under
	// sessions/{sessionID}/{parts...}/, oldest first (both backends only
	// ever append, never reorder).
	ReadAll(ctx context.Context, sessionID string, parts ...string) ([][]byte, error)
}
