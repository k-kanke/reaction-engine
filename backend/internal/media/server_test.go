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

	sessionsEnsured []string
	snapshots       []CaptureSnapshot
	refs            []MediaRef
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

func newTestHandler(store Store, mediaDir string) *Handler {
	h := NewHandler(store, mediaDir, "http://test-base", 900*time.Second)
	h.Now = func() time.Time { return time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC) }
	return h
}

func newTestMux(h *Handler) *http.ServeMux {
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	return mux
}

func TestHandleUploadURL_Success(t *testing.T) {
	store := &fakeStore{}
	mux := newTestMux(newTestHandler(store, t.TempDir()))

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
	mux := newTestMux(newTestHandler(store, t.TempDir()))

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
	mux := newTestMux(newTestHandler(store, t.TempDir()))

	body := `{"purpose":"baseline_frame","content_type":"image/gif","capture_id":"cap_1","audience_id":"aud_1"}`
	req := httptest.NewRequest(http.MethodPost, "/sessions/sess_1/media/upload-url", strings.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandleLocalUpload_Success(t *testing.T) {
	dir := t.TempDir()
	mux := newTestMux(newTestHandler(&fakeStore{}, dir))

	req := httptest.NewRequest(http.MethodPut, "/local-upload/sess_1/cap_1.webp", strings.NewReader("fake-webp-bytes"))
	req.Header.Set("Content-Type", "image/webp")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	stored := filepath.Join(dir, "sessions", "sess_1", "cap_1.webp")
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
	mux := newTestMux(newTestHandler(&fakeStore{}, dir))

	req := httptest.NewRequest(http.MethodPut, "/local-upload/sess_1/cap_1.webp", strings.NewReader("bytes"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandleLocalUpload_EmptyBody(t *testing.T) {
	dir := t.TempDir()
	mux := newTestMux(newTestHandler(&fakeStore{}, dir))

	req := httptest.NewRequest(http.MethodPut, "/local-upload/sess_1/cap_1.webp", strings.NewReader(""))
	req.Header.Set("Content-Type", "image/webp")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
	}
}
