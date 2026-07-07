package media

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/k-kanke/reaction-engine/backend/internal/contract"
)

type fakeStore struct {
	ensureSessionErr  error
	insertSnapshotErr error
	insertRefErr      error
	getCaptureErr     error
	markUploadedErr   error

	sessionsEnsured []string
	snapshots       []CaptureSnapshot
	refs            []MediaRef
	captures        map[string]CaptureRecord // key: sessionID+"/"+captureID
	markUploadedFor []string
}

func (f *fakeStore) EnsureSession(ctx context.Context, sessionID string) error {
	f.sessionsEnsured = append(f.sessionsEnsured, sessionID)
	return f.ensureSessionErr
}

func (f *fakeStore) InsertCaptureSnapshot(ctx context.Context, s CaptureSnapshot) error {
	f.snapshots = append(f.snapshots, s)
	return f.insertSnapshotErr
}

func (f *fakeStore) InsertMediaRef(ctx context.Context, m MediaRef) error {
	f.refs = append(f.refs, m)
	return f.insertRefErr
}

func (f *fakeStore) GetCapture(ctx context.Context, sessionID, captureID string) (CaptureRecord, error) {
	if f.getCaptureErr != nil {
		return CaptureRecord{}, f.getCaptureErr
	}
	rec, ok := f.captures[sessionID+"/"+captureID]
	if !ok {
		return CaptureRecord{}, ErrCaptureNotFound
	}
	return rec, nil
}

func (f *fakeStore) MarkUploaded(ctx context.Context, sessionID, captureID string, uploadedAt time.Time) error {
	f.markUploadedFor = append(f.markUploadedFor, sessionID+"/"+captureID)
	return f.markUploadedErr
}

type fakePublisher struct {
	enqueueErr error
	published  []publishedEvent
}

type publishedEvent struct {
	topic   string
	eventID string
	payload any
}

func (f *fakePublisher) Enqueue(ctx context.Context, topic, eventID string, payload any) error {
	f.published = append(f.published, publishedEvent{topic: topic, eventID: eventID, payload: payload})
	return f.enqueueErr
}

func newTestHandler(store Store, publisher EventPublisher, mediaDir string) *Handler {
	mediaStore := NewLocalMediaStore(mediaDir, "http://test-base", 900*time.Second)
	fixedNow := func() time.Time { return time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC) }
	mediaStore.Now = fixedNow

	h := NewHandler(store, publisher, mediaStore, mediaDir)
	h.Now = fixedNow
	return h
}

func newTestMux(h *Handler) *http.ServeMux {
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	return mux
}

func TestHandleUploadURL_Success(t *testing.T) {
	store := &fakeStore{}
	mux := newTestMux(newTestHandler(store, &fakePublisher{}, t.TempDir()))

	body := `{
		"purpose": "baseline_frame",
		"content_type": "image/webp",
		"capture_id": "cap_1",
		"t_ms": 12345,
		"audience_id": "aud_1",
		"tile_id": "tile_1",
		"feature_snapshot": {"attention_score": 0.55}
	}`
	req := httptest.NewRequest(http.MethodPost, "/sessions/sess_1/media/upload-url", strings.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var resp contract.UploadURLResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	wantMediaRef := "local://sessions/sess_1/baseline/frames/cap_1.webp"
	wantUploadURL := "http://test-base/local-upload/sess_1/cap_1.webp"
	if resp.MediaRef != wantMediaRef {
		t.Errorf("media_ref = %q, want %q", resp.MediaRef, wantMediaRef)
	}
	if resp.UploadURL != wantUploadURL {
		t.Errorf("upload_url = %q, want %q", resp.UploadURL, wantUploadURL)
	}
	if resp.CaptureID != "cap_1" {
		t.Errorf("capture_id = %q, want cap_1", resp.CaptureID)
	}
	if resp.ExpiresAt != "2026-07-04T12:15:00Z" {
		t.Errorf("expires_at = %q, want 2026-07-04T12:15:00Z", resp.ExpiresAt)
	}

	if len(store.sessionsEnsured) != 1 || store.sessionsEnsured[0] != "sess_1" {
		t.Errorf("sessionsEnsured = %v, want [sess_1]", store.sessionsEnsured)
	}
	if len(store.snapshots) != 1 {
		t.Fatalf("snapshots recorded = %d, want 1", len(store.snapshots))
	}
	snap := store.snapshots[0]
	if snap.UploadStatus != "pending" || snap.MediaRef != wantMediaRef || snap.AudienceID != "aud_1" {
		t.Errorf("unexpected snapshot: %+v", snap)
	}
	if string(snap.FeatureSnapshot) != `{"attention_score": 0.55}` {
		t.Errorf("feature_snapshot = %s", snap.FeatureSnapshot)
	}

	if len(store.refs) != 1 {
		t.Fatalf("refs recorded = %d, want 1", len(store.refs))
	}
	ref := store.refs[0]
	if ref.UploadStatus != "pending" || ref.MediaRef != wantMediaRef || ref.Purpose != "baseline_frame" {
		t.Errorf("unexpected media_ref row: %+v", ref)
	}
}

func TestHandleUploadURL_MissingCaptureID(t *testing.T) {
	store := &fakeStore{}
	mux := newTestMux(newTestHandler(store, &fakePublisher{}, t.TempDir()))

	body := `{"purpose":"baseline_frame","content_type":"image/webp","audience_id":"aud_1"}`
	req := httptest.NewRequest(http.MethodPost, "/sessions/sess_1/media/upload-url", strings.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
	}
	if len(store.snapshots) != 0 {
		t.Errorf("expected no snapshot to be recorded, got %d", len(store.snapshots))
	}
}

func TestHandleUploadURL_UnsupportedContentType(t *testing.T) {
	store := &fakeStore{}
	mux := newTestMux(newTestHandler(store, &fakePublisher{}, t.TempDir()))

	body := `{"purpose":"baseline_frame","content_type":"image/gif","capture_id":"cap_1","audience_id":"aud_1"}`
	req := httptest.NewRequest(http.MethodPost, "/sessions/sess_1/media/upload-url", strings.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
	}
}

// TestHandleUploadURL_EvidenceFrame covers Step 7 of
// plan/mood-wave-contract-migration.md: evidence_frame requests have no
// audience_id (a room-level tab screenshot, not a per-participant crop)
// and require trigger_id instead.
func TestHandleUploadURL_EvidenceFrame(t *testing.T) {
	store := &fakeStore{}
	mux := newTestMux(newTestHandler(store, &fakePublisher{}, t.TempDir()))

	body := `{
		"purpose": "evidence_frame",
		"content_type": "image/jpeg",
		"capture_id": "cap_ev_1",
		"t_ms": 1783067120000,
		"trigger_id": "trig_123"
	}`
	req := httptest.NewRequest(http.MethodPost, "/sessions/sess_1/media/upload-url", strings.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}

	if len(store.snapshots) != 1 {
		t.Fatalf("snapshots recorded = %d, want 1", len(store.snapshots))
	}
	snap := store.snapshots[0]
	if snap.AudienceID != "" {
		t.Errorf("AudienceID = %q, want empty for an evidence_frame capture", snap.AudienceID)
	}
	if snap.TriggerID != "trig_123" {
		t.Errorf("TriggerID = %q, want trig_123", snap.TriggerID)
	}

	if len(store.refs) != 1 {
		t.Fatalf("refs recorded = %d, want 1", len(store.refs))
	}
	if store.refs[0].TriggerID != "trig_123" {
		t.Errorf("media_refs TriggerID = %q, want trig_123", store.refs[0].TriggerID)
	}
}

func TestHandleUploadURL_EvidenceFrameMissingTriggerID(t *testing.T) {
	store := &fakeStore{}
	mux := newTestMux(newTestHandler(store, &fakePublisher{}, t.TempDir()))

	body := `{"purpose":"evidence_frame","content_type":"image/jpeg","capture_id":"cap_ev_1","t_ms":1783067120000}`
	req := httptest.NewRequest(http.MethodPost, "/sessions/sess_1/media/upload-url", strings.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
	}
	if len(store.snapshots) != 0 {
		t.Errorf("expected no snapshot to be recorded, got %d", len(store.snapshots))
	}
}

func TestHandleUploadURL_UnsupportedPurpose(t *testing.T) {
	store := &fakeStore{}
	mux := newTestMux(newTestHandler(store, &fakePublisher{}, t.TempDir()))

	body := `{"purpose":"something_else","content_type":"image/jpeg","capture_id":"cap_1","t_ms":1}`
	req := httptest.NewRequest(http.MethodPost, "/sessions/sess_1/media/upload-url", strings.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandleLocalUpload_Success(t *testing.T) {
	dir := t.TempDir()
	mux := newTestMux(newTestHandler(&fakeStore{}, &fakePublisher{}, dir))

	req := httptest.NewRequest(http.MethodPut, "/local-upload/sess_1/cap_1.webp", strings.NewReader("fake-webp-bytes"))
	req.Header.Set("Content-Type", "image/webp")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	stored := filepath.Join(dir, "sessions", "sess_1", "baseline", "frames", "cap_1.webp")
	data, err := os.ReadFile(stored)
	if err != nil {
		t.Fatalf("expected file at %s: %v", stored, err)
	}
	if string(data) != "fake-webp-bytes" {
		t.Errorf("stored content = %q", data)
	}
}

func TestHandleLocalUpload_MissingContentType(t *testing.T) {
	dir := t.TempDir()
	mux := newTestMux(newTestHandler(&fakeStore{}, &fakePublisher{}, dir))

	req := httptest.NewRequest(http.MethodPut, "/local-upload/sess_1/cap_1.webp", strings.NewReader("bytes"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandleLocalUpload_EmptyBody(t *testing.T) {
	dir := t.TempDir()
	mux := newTestMux(newTestHandler(&fakeStore{}, &fakePublisher{}, dir))

	req := httptest.NewRequest(http.MethodPut, "/local-upload/sess_1/cap_1.webp", strings.NewReader(""))
	req.Header.Set("Content-Type", "image/webp")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandleUploadComplete_Success(t *testing.T) {
	dir := t.TempDir()
	store := &fakeStore{
		captures: map[string]CaptureRecord{
			"sess_1/cap_1": {
				SessionID:   "sess_1",
				CaptureID:   "cap_1",
				AudienceID:  "aud_1",
				TileID:      "tile_1",
				TMs:         12345,
				MediaRef:    "local://sessions/sess_1/baseline/frames/cap_1.webp",
				ContentType: "image/webp",
				Purpose:     "baseline_frame",
			},
		},
	}
	publisher := &fakePublisher{}
	mux := newTestMux(newTestHandler(store, publisher, dir))

	// The file must already exist on disk, as if handleLocalUpload had run.
	uploadedDir := filepath.Join(dir, "sessions", "sess_1", "baseline", "frames")
	if err := os.MkdirAll(uploadedDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(uploadedDir, "cap_1.webp"), []byte("bytes"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/sessions/sess_1/media/cap_1/complete", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	if len(store.markUploadedFor) != 1 || store.markUploadedFor[0] != "sess_1/cap_1" {
		t.Errorf("markUploadedFor = %v, want [sess_1/cap_1]", store.markUploadedFor)
	}

	if len(publisher.published) != 1 {
		t.Fatalf("published events = %d, want 1", len(publisher.published))
	}
	got := publisher.published[0]
	if got.topic != "media-analysis-events" {
		t.Errorf("topic = %q, want media-analysis-events", got.topic)
	}
	payload, ok := got.payload.(contract.MediaUploadedEventPayload)
	if !ok {
		t.Fatalf("payload type = %T, want contract.MediaUploadedEventPayload", got.payload)
	}
	if payload.Type != "media_uploaded" || payload.SessionID != "sess_1" || payload.CaptureID != "cap_1" ||
		payload.AudienceID != "aud_1" || payload.TileID != "tile_1" || payload.TMs != 12345 ||
		payload.MediaRef != "local://sessions/sess_1/baseline/frames/cap_1.webp" || payload.Purpose != "baseline_frame" {
		t.Errorf("unexpected payload: %+v", payload)
	}
}

func TestHandleUploadComplete_CaptureNotFound(t *testing.T) {
	dir := t.TempDir()
	store := &fakeStore{captures: map[string]CaptureRecord{}}
	publisher := &fakePublisher{}
	mux := newTestMux(newTestHandler(store, publisher, dir))

	req := httptest.NewRequest(http.MethodPost, "/sessions/sess_1/media/cap_missing/complete", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body = %s", rec.Code, rec.Body.String())
	}
	if len(publisher.published) != 0 {
		t.Errorf("expected no event published, got %d", len(publisher.published))
	}
}

func TestHandleUploadComplete_FileNotUploaded(t *testing.T) {
	dir := t.TempDir()
	store := &fakeStore{
		captures: map[string]CaptureRecord{
			"sess_1/cap_1": {
				SessionID:   "sess_1",
				CaptureID:   "cap_1",
				AudienceID:  "aud_1",
				MediaRef:    "local://sessions/sess_1/baseline/frames/cap_1.webp",
				ContentType: "image/webp",
				Purpose:     "baseline_frame",
			},
		},
	}
	publisher := &fakePublisher{}
	mux := newTestMux(newTestHandler(store, publisher, dir))

	// No file written to disk this time.
	req := httptest.NewRequest(http.MethodPost, "/sessions/sess_1/media/cap_1/complete", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
	}
	if len(store.markUploadedFor) != 0 {
		t.Errorf("expected MarkUploaded not to be called, got %v", store.markUploadedFor)
	}
	if len(publisher.published) != 0 {
		t.Errorf("expected no event published, got %d", len(publisher.published))
	}
}
