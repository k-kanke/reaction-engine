package gateway

import (
	"encoding/json"
	"testing"

	"github.com/k-kanke/reaction-engine/backend/internal/contract"
	gwredis "github.com/k-kanke/reaction-engine/backend/internal/redis"
)

func TestBuildFeedback(t *testing.T) {
	readyBaseline := func(avg float64) json.RawMessage {
		b, _ := json.Marshal(baselineFields{AttentionScoreAvg: avg})
		return b
	}

	cases := []struct {
		name         string
		feature      contract.CompactFeature
		state        gwredis.BaselineState
		wantSource   string
		wantFeedback string
		wantSeverity string
	}{
		{
			name:         "warming_up falls back to default threshold, above",
			feature:      contract.CompactFeature{SessionID: "sess_1", AudienceID: "aud_1", TMs: 1000, AttentionScore: 0.6},
			state:        gwredis.BaselineState{Status: gwredis.BaselineStatusWarmingUp},
			wantSource:   "rule_default",
			wantFeedback: "on_track",
			wantSeverity: "info",
		},
		{
			name:         "warming_up falls back to default threshold, below",
			feature:      contract.CompactFeature{SessionID: "sess_1", AudienceID: "aud_1", TMs: 1000, AttentionScore: 0.2},
			state:        gwredis.BaselineState{Status: gwredis.BaselineStatusWarmingUp},
			wantSource:   "rule_default",
			wantFeedback: "attention_drop",
			wantSeverity: "warning",
		},
		{
			name:         "ready baseline, within margin",
			feature:      contract.CompactFeature{SessionID: "sess_1", AudienceID: "aud_1", TMs: 1000, AttentionScore: 0.55},
			state:        gwredis.BaselineState{Status: gwredis.BaselineStatusReady, Baseline: readyBaseline(0.6)},
			wantSource:   "rule_baseline",
			wantFeedback: "on_track",
			wantSeverity: "info",
		},
		{
			name:         "ready baseline, below margin",
			feature:      contract.CompactFeature{SessionID: "sess_1", AudienceID: "aud_1", TMs: 1000, AttentionScore: 0.3},
			state:        gwredis.BaselineState{Status: gwredis.BaselineStatusReady, Baseline: readyBaseline(0.6)},
			wantSource:   "rule_baseline",
			wantFeedback: "attention_drop",
			wantSeverity: "warning",
		},
		{
			name:         "ready status but unparseable baseline falls back to default",
			feature:      contract.CompactFeature{SessionID: "sess_1", AudienceID: "aud_1", TMs: 1000, AttentionScore: 0.6},
			state:        gwredis.BaselineState{Status: gwredis.BaselineStatusReady, Baseline: nil},
			wantSource:   "rule_default",
			wantFeedback: "on_track",
			wantSeverity: "info",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildFeedback(tc.feature, tc.state)

			if got.Type != "feedback_event" {
				t.Errorf("Type = %q, want feedback_event", got.Type)
			}
			if got.SessionID != tc.feature.SessionID {
				t.Errorf("SessionID = %q, want %q", got.SessionID, tc.feature.SessionID)
			}
			if got.AudienceID != tc.feature.AudienceID {
				t.Errorf("AudienceID = %q, want %q", got.AudienceID, tc.feature.AudienceID)
			}
			if got.TMs != tc.feature.TMs {
				t.Errorf("TMs = %d, want %d", got.TMs, tc.feature.TMs)
			}
			if got.Source != tc.wantSource {
				t.Errorf("Source = %q, want %q", got.Source, tc.wantSource)
			}
			if got.FeedbackType != tc.wantFeedback {
				t.Errorf("FeedbackType = %q, want %q", got.FeedbackType, tc.wantFeedback)
			}
			if got.Severity != tc.wantSeverity {
				t.Errorf("Severity = %q, want %q", got.Severity, tc.wantSeverity)
			}
		})
	}
}

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
