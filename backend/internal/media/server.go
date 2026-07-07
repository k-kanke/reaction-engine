package media

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"

	"github.com/k-kanke/reaction-engine/backend/internal/contract"
)

const (
	maxLocalUploadBytes      = 10 << 20 // 10MB, generous for a single baseline frame
	mediaAnalysisEventsTopic = "media-analysis-events"
)

type Handler struct {
	Store         Store
	Events        EventPublisher
	MediaStore    MediaStore
	LocalMediaDir string
	Now           func() time.Time
}

func NewHandler(store Store, events EventPublisher, mediaStore MediaStore, localMediaDir string) *Handler {
	return &Handler{
		Store:         store,
		Events:        events,
		MediaStore:    mediaStore,
		LocalMediaDir: localMediaDir,
		Now:           time.Now,
	}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /sessions/{session_id}/media/upload-url", h.handleUploadURL)
	mux.HandleFunc("PUT /local-upload/{session_id}/{filename}", h.handleLocalUpload)
	mux.HandleFunc("POST /sessions/{session_id}/media/{capture_id}/complete", h.handleUploadComplete)
}

// handleUploadURL implements Phase 7.1: it validates the request, records a
// pending capture_snapshot + media_ref, and returns a (locally: fake) signed
// upload URL. It does not publish any event; that is Phase 7.3 / Phase 5.
func (h *Handler) handleUploadURL(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("session_id")

	var req contract.UploadURLRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}

	if err := validateUploadURLRequest(sessionID, req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	ext, ok := extensionForContentType(req.ContentType)
	if !ok {
		writeError(w, http.StatusBadRequest, "unsupported content_type: "+req.ContentType)
		return
	}

	ctx := r.Context()
	if err := h.Store.EnsureSession(ctx, sessionID); err != nil {
		log.Printf("media-api: ensure session failed: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to prepare session")
		return
	}

	uploadURL, ref, expiresAtStr, err := h.MediaStore.SignedUploadURL(ctx, sessionID, req.CaptureID, req.ContentType, ext)
	if err != nil {
		log.Printf("media-api: signed upload url failed: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to create upload url")
		return
	}

	if err := h.Store.InsertCaptureSnapshot(ctx, CaptureSnapshot{
		CaptureID:       req.CaptureID,
		SessionID:       sessionID,
		AudienceID:      req.AudienceID,
		TileID:          req.TileID,
		TMs:             req.TMs,
		MediaRef:        ref,
		UploadStatus:    "pending",
		FeatureSnapshot: req.FeatureSnapshot,
		TriggerID:       req.TriggerID,
	}); err != nil {
		log.Printf("media-api: insert capture_snapshot failed: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to store capture snapshot")
		return
	}

	if err := h.Store.InsertMediaRef(ctx, MediaRef{
		SessionID:    sessionID,
		CaptureID:    req.CaptureID,
		MediaRef:     ref,
		Purpose:      req.Purpose,
		ContentType:  req.ContentType,
		UploadStatus: "pending",
		TriggerID:    req.TriggerID,
	}); err != nil {
		log.Printf("media-api: insert media_ref failed: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to store media ref")
		return
	}

	resp := contract.UploadURLResponse{
		UploadURL: uploadURL,
		MediaRef:  ref,
		CaptureID: req.CaptureID,
		ExpiresAt: expiresAtStr,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// validateUploadURLRequest enforces the two shapes architecture.md's Upload
// URL request examples show (画像フロー § Media API): baseline_frame is a
// per-participant crop and requires audience_id; evidence_frame is a
// room-level tab screenshot tied to the trigger that caused it and
// requires trigger_id instead — it has no audience_id on the wire at all.
func validateUploadURLRequest(sessionID string, req contract.UploadURLRequest) error {
	if sessionID == "" {
		return errors.New("session_id is required")
	}
	if req.CaptureID == "" {
		return errors.New("capture_id is required")
	}
	if req.ContentType == "" {
		return errors.New("content_type is required")
	}

	switch req.Purpose {
	case "baseline_frame":
		if req.AudienceID == "" {
			return errors.New("audience_id is required for purpose=baseline_frame")
		}
	case "evidence_frame":
		if req.TriggerID == "" {
			return errors.New("trigger_id is required for purpose=evidence_frame")
		}
	case "":
		return errors.New("purpose is required")
	default:
		return fmt.Errorf("unsupported purpose: %s", req.Purpose)
	}

	return nil
}

// handleLocalUpload implements Phase 7.2: a local-only endpoint (not used in
// Cloud Run, where the extension PUTs directly to a Cloud Storage signed
// URL) that stores the uploaded bytes under LocalMediaDir, at the same
// sessions/{session_id}/baseline/frames/ layout mediaRef encodes.
func (h *Handler) handleLocalUpload(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("session_id")
	filename := r.PathValue("filename")

	if sessionID == "" || filename == "" {
		writeError(w, http.StatusBadRequest, "session_id and filename are required")
		return
	}
	if r.Header.Get("Content-Type") == "" {
		writeError(w, http.StatusBadRequest, "Content-Type header is required")
		return
	}

	dir := filepath.Join(h.LocalMediaDir, "sessions", sessionID, "baseline", "frames")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("media-api: mkdir failed: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to prepare storage")
		return
	}

	dest := filepath.Join(dir, filename)
	out, err := os.Create(dest)
	if err != nil {
		log.Printf("media-api: create file failed: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to store file")
		return
	}
	defer out.Close()

	limited := http.MaxBytesReader(w, r.Body, maxLocalUploadBytes)
	written, err := io.Copy(out, limited)
	if err != nil {
		os.Remove(dest)
		writeError(w, http.StatusRequestEntityTooLarge, "upload too large or read failed")
		return
	}
	if written == 0 {
		os.Remove(dest)
		writeError(w, http.StatusBadRequest, "empty upload body")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"stored":   true,
		"bytes":    written,
		"path":     dest,
		"filename": filename,
	})
}

// handleUploadComplete implements Phase 7.3: it confirms the uploaded file
// exists on local disk, marks the capture_snapshots/media_refs rows
// uploaded, and publishes a media_uploaded event to the local event bus
// (media-analysis-events topic) for the Image Analysis Worker to consume.
func (h *Handler) handleUploadComplete(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("session_id")
	captureID := r.PathValue("capture_id")
	if sessionID == "" || captureID == "" {
		writeError(w, http.StatusBadRequest, "session_id and capture_id are required")
		return
	}

	ctx := r.Context()
	capture, err := h.Store.GetCapture(ctx, sessionID, captureID)
	if errors.Is(err, ErrCaptureNotFound) {
		writeError(w, http.StatusNotFound, "capture not found; call upload-url first")
		return
	}
	if err != nil {
		log.Printf("media-api: get capture failed: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to load capture")
		return
	}

	exists, err := h.MediaStore.Exists(ctx, capture.MediaRef)
	if err != nil {
		log.Printf("media-api: check media exists failed for ref %s: %v", capture.MediaRef, err)
		writeError(w, http.StatusInternalServerError, "failed to check uploaded file")
		return
	}
	if !exists {
		writeError(w, http.StatusBadRequest, "uploaded file not found; PUT to the upload URL first")
		return
	}

	if err := h.Store.MarkUploaded(ctx, sessionID, captureID, h.Now()); err != nil {
		log.Printf("media-api: mark uploaded failed: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to update upload status")
		return
	}

	eventID := "evt_" + uuid.NewString()
	payload := contract.MediaUploadedEventPayload{
		EventID:       eventID,
		Type:          "media_uploaded",
		SchemaVersion: 1,
		SessionID:     sessionID,
		CaptureID:     captureID,
		AudienceID:    capture.AudienceID,
		TileID:        capture.TileID,
		TMs:           capture.TMs,
		MediaRef:      capture.MediaRef,
		Purpose:       capture.Purpose,
	}
	if err := h.Events.Enqueue(ctx, mediaAnalysisEventsTopic, eventID, payload); err != nil {
		log.Printf("media-api: enqueue media_uploaded failed: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to publish media_uploaded event")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":     "uploaded",
		"event_id":   eventID,
		"capture_id": captureID,
	})
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}
