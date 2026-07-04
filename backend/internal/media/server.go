package media

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/k-kanke/reaction-engine/backend/internal/contract"
)

const maxLocalUploadBytes = 10 << 20 // 10MB, generous for a single baseline frame

type Handler struct {
	Store         Store
	LocalMediaDir string
	PublicBaseURL string
	SignedURLTTL  time.Duration
	Now           func() time.Time
}

func NewHandler(store Store, localMediaDir, publicBaseURL string, signedURLTTL time.Duration) *Handler {
	return &Handler{
		Store:         store,
		LocalMediaDir: localMediaDir,
		PublicBaseURL: publicBaseURL,
		SignedURLTTL:  signedURLTTL,
		Now:           time.Now,
	}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /sessions/{session_id}/media/upload-url", h.handleUploadURL)
	mux.HandleFunc("PUT /local-upload/{session_id}/{filename}", h.handleLocalUpload)
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

	ref := mediaRef(sessionID, req.CaptureID, ext)

	if err := h.Store.InsertCaptureSnapshot(ctx, CaptureSnapshot{
		CaptureID:       req.CaptureID,
		SessionID:       sessionID,
		AudienceID:      req.AudienceID,
		TileID:          req.TileID,
		TMs:             req.TMs,
		MediaRef:        ref,
		UploadStatus:    "pending",
		FeatureSnapshot: req.FeatureSnapshot,
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
	}); err != nil {
		log.Printf("media-api: insert media_ref failed: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to store media ref")
		return
	}

	resp := contract.UploadURLResponse{
		UploadURL: localUploadURL(h.PublicBaseURL, sessionID, req.CaptureID, ext),
		MediaRef:  ref,
		CaptureID: req.CaptureID,
		ExpiresAt: expiresAt(h.SignedURLTTL, h.Now()),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func validateUploadURLRequest(sessionID string, req contract.UploadURLRequest) error {
	if sessionID == "" {
		return errors.New("session_id is required")
	}
	if req.CaptureID == "" {
		return errors.New("capture_id is required")
	}
	if req.AudienceID == "" {
		return errors.New("audience_id is required")
	}
	if req.Purpose == "" {
		return errors.New("purpose is required")
	}
	if req.ContentType == "" {
		return errors.New("content_type is required")
	}
	return nil
}

// handleLocalUpload implements Phase 7.2: a local-only endpoint (not used in
// Cloud Run, where the extension PUTs directly to a Cloud Storage signed
// URL) that stores the uploaded bytes under LocalMediaDir.
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

	dir := filepath.Join(h.LocalMediaDir, "sessions", sessionID)
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

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}
