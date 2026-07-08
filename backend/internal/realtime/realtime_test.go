package realtime

import (
	"context"
	"testing"
	"time"

	"github.com/k-kanke/reaction-engine/backend/internal/contract"
)

func TestRuleFallback(t *testing.T) {
	cases := []struct {
		name             string
		overall          string
		triggerType      string
		wantFeedbackType string
		wantReasonCode   string
	}{
		{"declined", "declined", "wave_drop", "reaction_down_candidate", "mood_wave_drop"},
		{"improved", "improved", "wave_rise", "reaction_up_candidate", "mood_wave_rise"},
		{"stable", "stable", "wave_drop", "on_track", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			moodWave := MoodWave{Summary: MoodWaveSummary{Overall: tc.overall}}
			trigger := contract.TriggerInfo{TriggerID: "trig_1", Type: tc.triggerType}

			got := ruleFallback("sess_1", 1000, trigger, moodWave)

			if got.Type != "feedback_event" {
				t.Errorf("Type = %q, want feedback_event", got.Type)
			}
			if got.SessionID != "sess_1" || got.TMs != 1000 {
				t.Errorf("SessionID/TMs = %q/%d, want sess_1/1000", got.SessionID, got.TMs)
			}
			if got.TriggerID != "trig_1" {
				t.Errorf("TriggerID = %q, want trig_1", got.TriggerID)
			}
			if got.Source != "rule" {
				t.Errorf("Source = %q, want rule", got.Source)
			}
			if got.FeedbackType != tc.wantFeedbackType {
				t.Errorf("FeedbackType = %q, want %q", got.FeedbackType, tc.wantFeedbackType)
			}
			if got.CooldownMs != feedbackCooldownMs {
				t.Errorf("CooldownMs = %d, want %d", got.CooldownMs, feedbackCooldownMs)
			}
			if tc.wantReasonCode != "" {
				found := false
				for _, code := range got.ReasonCodes {
					if code == tc.wantReasonCode {
						found = true
					}
				}
				if !found {
					t.Errorf("ReasonCodes = %v, want to contain %q", got.ReasonCodes, tc.wantReasonCode)
				}
			}
		})
	}

	t.Run("nod trigger adds nod_trigger reason code", func(t *testing.T) {
		moodWave := MoodWave{Summary: MoodWaveSummary{Overall: "stable"}}
		trigger := contract.TriggerInfo{TriggerID: "trig_nod", Type: "nod"}
		got := ruleFallback("sess_1", 1000, trigger, moodWave)
		if len(got.ReasonCodes) != 1 || got.ReasonCodes[0] != "nod_trigger" {
			t.Errorf("ReasonCodes = %v, want [nod_trigger]", got.ReasonCodes)
		}
	})
}

func TestGenerateLLMStubCandidate(t *testing.T) {
	pack := EvidencePack{
		MoodWave:         MoodWave{Summary: MoodWaveSummary{Overall: "declined"}},
		TranscriptWindow: []contract.TranscriptChunk{{TStartMs: 1000, Text: "old"}, {TStartMs: 5000, Text: "quarterly numbers look strong"}},
	}
	trigger := contract.TriggerInfo{TriggerID: "trig_1", Type: "wave_drop"}

	t.Run("quotes the latest transcript chunk", func(t *testing.T) {
		got, err := generateLLMStubCandidate(context.Background(), "sess_1", 1000, trigger, pack)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Source != "llm_stub" {
			t.Errorf("Source = %q, want llm_stub", got.Source)
		}
		if got.ModelVersion != llmStubModelVersion {
			t.Errorf("ModelVersion = %q, want %q", got.ModelVersion, llmStubModelVersion)
		}
		if got.EvidenceQuote == nil || *got.EvidenceQuote != "quarterly numbers look strong" {
			t.Errorf("EvidenceQuote = %v, want pointer to latest chunk text", got.EvidenceQuote)
		}
	})

	t.Run("no transcript leaves EvidenceQuote nil", func(t *testing.T) {
		emptyPack := EvidencePack{MoodWave: MoodWave{Summary: MoodWaveSummary{Overall: "stable"}}}
		got, err := generateLLMStubCandidate(context.Background(), "sess_1", 1000, trigger, emptyPack)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.EvidenceQuote != nil {
			t.Errorf("EvidenceQuote = %v, want nil", got.EvidenceQuote)
		}
	})

	t.Run("expired context falls back with an error", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 0)
		defer cancel()
		time.Sleep(time.Millisecond)

		_, err := generateLLMStubCandidate(ctx, "sess_1", 1000, trigger, pack)
		if err == nil {
			t.Fatalf("expected an error from an already-expired context, got nil")
		}
	})
}

// fakeWaveStore is an in-memory WaveStore for testing HandleTrigger without
// a real Redis.
type fakeWaveStore struct {
	samples        []contract.MoodWaveSampleMessage
	selfChunks     []contract.TranscriptChunk
	otherChunks    []contract.TranscriptChunk
	baselineRefs   []string
	inCooldown     bool
	cooldownSetMs  int
	cooldownWasSet bool
	failOn         string // name of the method to make return an error, or ""
}

var errFakeStoreFailure = context.DeadlineExceeded

func (f *fakeWaveStore) GetRecentMoodWaveSamples(ctx context.Context, sessionID string, sinceTMs int64) ([]contract.MoodWaveSampleMessage, error) {
	if f.failOn == "GetRecentMoodWaveSamples" {
		return nil, errFakeStoreFailure
	}
	return f.samples, nil
}

func (f *fakeWaveStore) GetTranscriptWindow(ctx context.Context, sessionID, speaker string, sinceTMs int64) ([]contract.TranscriptChunk, error) {
	if f.failOn == "GetTranscriptWindow" {
		return nil, errFakeStoreFailure
	}
	if speaker == "self" {
		return f.selfChunks, nil
	}
	return f.otherChunks, nil
}

func (f *fakeWaveStore) ListReadyBaselineMediaRefs(ctx context.Context, sessionID string) ([]string, error) {
	if f.failOn == "ListReadyBaselineMediaRefs" {
		return nil, errFakeStoreFailure
	}
	return f.baselineRefs, nil
}

func (f *fakeWaveStore) InFeedbackCooldown(ctx context.Context, sessionID string) (bool, error) {
	if f.failOn == "InFeedbackCooldown" {
		return false, errFakeStoreFailure
	}
	return f.inCooldown, nil
}

func (f *fakeWaveStore) SetFeedbackCooldown(ctx context.Context, sessionID string, cooldownMs int) error {
	if f.failOn == "SetFeedbackCooldown" {
		return errFakeStoreFailure
	}
	f.cooldownWasSet = true
	f.cooldownSetMs = cooldownMs
	return nil
}

func triggerMsg(trigger *contract.TriggerInfo) contract.MoodWaveSampleMessage {
	return contract.MoodWaveSampleMessage{
		SessionID: "sess_1",
		TMs:       30000,
		Mood:      contract.MoodValue{Y: -0.2},
		Trigger:   trigger,
	}
}

func TestHandleTrigger(t *testing.T) {
	t.Run("rejects when no trigger present", func(t *testing.T) {
		store := &fakeWaveStore{}
		_, accepted, err := HandleTrigger(context.Background(), store, triggerMsg(nil), false)
		if err == nil {
			t.Fatalf("expected an error for a message without a trigger")
		}
		if accepted {
			t.Errorf("accepted = true, want false")
		}
	})

	t.Run("in cooldown: not accepted, no error", func(t *testing.T) {
		store := &fakeWaveStore{inCooldown: true}
		trigger := &contract.TriggerInfo{TriggerID: "trig_1", Type: "wave_drop"}
		feedback, accepted, err := HandleTrigger(context.Background(), store, triggerMsg(trigger), false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if accepted {
			t.Errorf("accepted = true, want false")
		}
		if feedback.Type != "" {
			t.Errorf("feedback = %+v, want zero value", feedback)
		}
		if store.cooldownWasSet {
			t.Errorf("cooldown should not be reset when already in cooldown")
		}
	})

	t.Run("accepted, rule fallback (llm disabled)", func(t *testing.T) {
		store := &fakeWaveStore{
			samples: []contract.MoodWaveSampleMessage{
				sample(0, 0.1, 0),
				sample(30000, -0.2, 0),
			},
			baselineRefs: []string{"gs://bucket/baseline/aud_1.webp"},
		}
		trigger := &contract.TriggerInfo{TriggerID: "trig_1", Type: "wave_drop", PeakTMs: 30000, Delta: 0.3}
		feedback, accepted, err := HandleTrigger(context.Background(), store, triggerMsg(trigger), false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !accepted {
			t.Fatalf("accepted = false, want true")
		}
		if feedback.Source != "rule" {
			t.Errorf("Source = %q, want rule", feedback.Source)
		}
		if feedback.FeedbackType != "reaction_down_candidate" {
			t.Errorf("FeedbackType = %q, want reaction_down_candidate", feedback.FeedbackType)
		}
		if feedback.TriggerID != "trig_1" {
			t.Errorf("TriggerID = %q, want trig_1", feedback.TriggerID)
		}
		if !store.cooldownWasSet || store.cooldownSetMs != feedbackCooldownMs {
			t.Errorf("cooldown not set correctly: wasSet=%v ms=%d", store.cooldownWasSet, store.cooldownSetMs)
		}
	})

	t.Run("accepted, llm stub (llm enabled)", func(t *testing.T) {
		store := &fakeWaveStore{
			samples: []contract.MoodWaveSampleMessage{
				sample(0, 0.1, 0),
				sample(30000, -0.2, 0),
			},
			selfChunks: []contract.TranscriptChunk{{TStartMs: 10000, Text: "let's talk pricing"}},
		}
		trigger := &contract.TriggerInfo{TriggerID: "trig_1", Type: "wave_drop"}
		feedback, accepted, err := HandleTrigger(context.Background(), store, triggerMsg(trigger), true)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !accepted {
			t.Fatalf("accepted = false, want true")
		}
		if feedback.Source != "llm_stub" {
			t.Errorf("Source = %q, want llm_stub", feedback.Source)
		}
		if feedback.EvidenceQuote == nil || *feedback.EvidenceQuote != "let's talk pricing" {
			t.Errorf("EvidenceQuote = %v, want pointer to the self transcript chunk", feedback.EvidenceQuote)
		}
	})

	t.Run("mood wave read failure propagates as an error, not accepted", func(t *testing.T) {
		store := &fakeWaveStore{failOn: "GetRecentMoodWaveSamples"}
		trigger := &contract.TriggerInfo{TriggerID: "trig_1", Type: "wave_drop"}
		_, accepted, err := HandleTrigger(context.Background(), store, triggerMsg(trigger), false)
		if err == nil {
			t.Fatalf("expected an error")
		}
		if accepted {
			t.Errorf("accepted = true, want false")
		}
	})
}

func TestBuildEvidencePackIncludesUploadingEvidenceFrameRef(t *testing.T) {
	store := &fakeWaveStore{
		samples: []contract.MoodWaveSampleMessage{
			sample(10000, 0.1, 0),
			sample(30000, -0.2, 0),
		},
	}
	trigger := contract.TriggerInfo{TriggerID: "trig_1", Type: "wave_drop", PeakTMs: 30000}
	msg := triggerMsg(&trigger)
	msg.EvidenceFrame = &contract.EvidenceFrameRef{
		MediaRef:     "gs://bucket/evidence/trig_1.jpg",
		UploadStatus: "uploading",
		SnapshotTMs:  29900,
	}

	pack, err := buildEvidencePack(context.Background(), store, "sess_1", msg, trigger)
	if err != nil {
		t.Fatalf("buildEvidencePack returned error: %v", err)
	}
	if len(pack.EvidenceFrames) != 1 {
		t.Fatalf("EvidenceFrames len = %d, want 1", len(pack.EvidenceFrames))
	}
	if pack.EvidenceFrames[0].MediaRef != "gs://bucket/evidence/trig_1.jpg" {
		t.Errorf("EvidenceFrames[0].MediaRef = %q", pack.EvidenceFrames[0].MediaRef)
	}
}
