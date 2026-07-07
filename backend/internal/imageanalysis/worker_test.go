package imageanalysis

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/k-kanke/reaction-engine/backend/internal/contract"
)

type fakeStore struct {
	captures           map[string]CaptureData // key: sessionID+"/"+captureID
	getCaptureCalls    int
	upsertBaselineCall bool
	insertSummaryCall  bool
}

func (f *fakeStore) GetCaptureSnapshot(ctx context.Context, sessionID, captureID string) (CaptureData, error) {
	f.getCaptureCalls++
	data, ok := f.captures[sessionID+"/"+captureID]
	if !ok {
		return CaptureData{}, ErrCaptureNotFound
	}
	return data, nil
}

func (f *fakeStore) GetParticipantBaseline(ctx context.Context, sessionID, audienceID string) (ParticipantBaseline, bool, error) {
	return ParticipantBaseline{}, false, nil
}

func (f *fakeStore) UpsertParticipantBaseline(ctx context.Context, sessionID, audienceID string, baseline json.RawMessage, sampleCount int, confidence float64) error {
	f.upsertBaselineCall = true
	return nil
}

func (f *fakeStore) InsertVisualSummary(ctx context.Context, sessionID, audienceID, captureID, mediaRef string, visualSummary json.RawMessage, confidence float64) error {
	f.insertSummaryCall = true
	return nil
}

type fakeCache struct {
	setBaselineReadyCall bool
}

func (f *fakeCache) SetBaselineReady(ctx context.Context, sessionID, audienceID string, baseline, visualSummary []byte, mediaRef string) error {
	f.setBaselineReadyCall = true
	return nil
}

type fakeMediaReader struct {
	exists bool
}

func (f *fakeMediaReader) Exists(ctx context.Context, mediaRef string) (bool, error) {
	return f.exists, nil
}

func (f *fakeMediaReader) Read(ctx context.Context, mediaRef string) ([]byte, error) {
	return nil, nil
}

// TestProcessMediaUploaded_IgnoresNonBaselinePurpose covers Step 8 of
// plan/mood-wave-contract-migration.md: an evidence_frame media_uploaded
// event (possible since Step 7 wired Media API to accept them) must not be
// processed as a baseline — it should not even look up the capture
// snapshot, let alone overwrite a participant's baseline with data from an
// unrelated trigger-driven capture.
func TestProcessMediaUploaded_IgnoresNonBaselinePurpose(t *testing.T) {
	store := &fakeStore{captures: map[string]CaptureData{
		"sess_1/cap_ev_1": {SessionID: "sess_1", CaptureID: "cap_ev_1", AudienceID: "", MediaRef: "local://sessions/sess_1/evidence/cap_ev_1.jpg"},
	}}
	cache := &fakeCache{}
	mediaReader := &fakeMediaReader{exists: true}
	w := NewWorker(store, cache, mediaReader)

	err := w.ProcessMediaUploaded(context.Background(), contract.MediaUploadedEventPayload{
		SessionID: "sess_1",
		CaptureID: "cap_ev_1",
		Purpose:   "evidence_frame",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if store.getCaptureCalls != 0 {
		t.Errorf("GetCaptureSnapshot called %d times, want 0", store.getCaptureCalls)
	}
	if store.upsertBaselineCall {
		t.Errorf("UpsertParticipantBaseline was called, want not called")
	}
	if store.insertSummaryCall {
		t.Errorf("InsertVisualSummary was called, want not called")
	}
	if cache.setBaselineReadyCall {
		t.Errorf("SetBaselineReady was called, want not called")
	}
}

// TestProcessMediaUploaded_BaselineFramePurposeStillProcessed is a
// regression check that the Step 8 guard doesn't also block the intended
// baseline_frame path.
func TestProcessMediaUploaded_BaselineFramePurposeStillProcessed(t *testing.T) {
	store := &fakeStore{captures: map[string]CaptureData{
		"sess_1/cap_bl_1": {
			SessionID:       "sess_1",
			CaptureID:       "cap_bl_1",
			AudienceID:      "aud_1",
			MediaRef:        "local://sessions/sess_1/baseline/frames/cap_bl_1.webp",
			FeatureSnapshot: json.RawMessage(`{"attention_score":0.6}`),
		},
	}}
	cache := &fakeCache{}
	mediaReader := &fakeMediaReader{exists: true}
	w := NewWorker(store, cache, mediaReader)

	err := w.ProcessMediaUploaded(context.Background(), contract.MediaUploadedEventPayload{
		SessionID:  "sess_1",
		CaptureID:  "cap_bl_1",
		AudienceID: "aud_1",
		Purpose:    "baseline_frame",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if store.getCaptureCalls != 1 {
		t.Errorf("GetCaptureSnapshot called %d times, want 1", store.getCaptureCalls)
	}
	if !store.upsertBaselineCall {
		t.Errorf("UpsertParticipantBaseline was not called, want called")
	}
	if !store.insertSummaryCall {
		t.Errorf("InsertVisualSummary was not called, want called")
	}
	if !cache.setBaselineReadyCall {
		t.Errorf("SetBaselineReady was not called, want called")
	}
}
