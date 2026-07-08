package gateway

import "testing"

func TestBuildFakeTranscriptChunk(t *testing.T) {
	got := buildFakeTranscriptChunk("sess_1", "self", 1000, 6000)

	if got.Type != "transcript_chunk" {
		t.Errorf("Type = %q, want transcript_chunk", got.Type)
	}
	if got.SchemaVersion != 1 {
		t.Errorf("SchemaVersion = %d, want 1", got.SchemaVersion)
	}
	if got.SessionID != "sess_1" {
		t.Errorf("SessionID = %q, want sess_1", got.SessionID)
	}
	if got.Speaker != "self" {
		t.Errorf("Speaker = %q, want self", got.Speaker)
	}
	if got.TStartMs != 1000 || got.TEndMs != 6000 {
		t.Errorf("TStartMs/TEndMs = %d/%d, want 1000/6000", got.TStartMs, got.TEndMs)
	}
	if !got.IsFinal {
		t.Errorf("IsFinal = false, want true")
	}
	if got.Confidence != fakeTranscriptConfidence {
		t.Errorf("Confidence = %v, want %v", got.Confidence, fakeTranscriptConfidence)
	}
	if got.EventID == "" {
		t.Errorf("EventID is empty, want a generated evt_ id")
	}
	if got.Text == "" {
		t.Errorf("Text is empty, want a stub transcript string")
	}
}
