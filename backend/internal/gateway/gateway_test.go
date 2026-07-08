package gateway

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/k-kanke/reaction-engine/backend/internal/contract"
)

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

// fakeTrigger records TriggerSessionEnd calls for assertions instead of
// hitting the real Cloud Run Admin API.
type fakeTrigger struct {
	mu         sync.Mutex
	sessionIDs []string
	called     chan struct{}
}

func newFakeTrigger() *fakeTrigger {
	return &fakeTrigger{called: make(chan struct{}, 1)}
}

func (f *fakeTrigger) TriggerSessionEnd(ctx context.Context, sessionID string) {
	f.mu.Lock()
	f.sessionIDs = append(f.sessionIDs, sessionID)
	f.mu.Unlock()
	f.called <- struct{}{}
}

func TestHandleSessionEndStartsTrigger(t *testing.T) {
	trigger := newFakeTrigger()
	h := NewHandlerWithPostSessionTrigger(nil, nil, nil, nil, "", trigger)

	raw, err := json.Marshal(contract.SessionEndMessage{Type: "session_end", SessionID: "sess_end_1"})
	if err != nil {
		t.Fatalf("marshal session_end message: %v", err)
	}

	h.handleSessionEnd(raw)

	select {
	case <-trigger.called:
	case <-time.After(time.Second):
		t.Fatal("TriggerSessionEnd was not called within 1s")
	}

	trigger.mu.Lock()
	defer trigger.mu.Unlock()
	if len(trigger.sessionIDs) != 1 || trigger.sessionIDs[0] != "sess_end_1" {
		t.Errorf("sessionIDs = %v, want [sess_end_1]", trigger.sessionIDs)
	}
}

func TestHandleSessionEndNilTriggerIsNoop(t *testing.T) {
	h := NewHandlerWithPostSessionTrigger(nil, nil, nil, nil, "", nil)

	raw, err := json.Marshal(contract.SessionEndMessage{Type: "session_end", SessionID: "sess_end_2"})
	if err != nil {
		t.Fatalf("marshal session_end message: %v", err)
	}

	// Should not panic even though redis/events are nil -- a nil trigger
	// returns before either is touched.
	h.handleSessionEnd(raw)
}

func TestHandleSessionEndMissingSessionID(t *testing.T) {
	trigger := newFakeTrigger()
	h := NewHandlerWithPostSessionTrigger(nil, nil, nil, nil, "", trigger)

	raw, err := json.Marshal(contract.SessionEndMessage{Type: "session_end", SessionID: ""})
	if err != nil {
		t.Fatalf("marshal session_end message: %v", err)
	}

	h.handleSessionEnd(raw)

	select {
	case <-trigger.called:
		t.Fatal("TriggerSessionEnd was called with an empty session_id")
	case <-time.After(100 * time.Millisecond):
	}
}
