package gateway

import (
	"context"
	"encoding/json"
	"testing"
	"time"

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

func TestGenerateLLMStubCandidate(t *testing.T) {
	feature := contract.CompactFeature{SessionID: "sess_1", AudienceID: "aud_1", TMs: 1000}

	t.Run("attention_drop includes reason codes and evidence", func(t *testing.T) {
		got, err := generateLLMStubCandidate(context.Background(), feature, "attention_drop", "we should talk about pricing")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got.ReasonCodes) == 0 {
			t.Errorf("ReasonCodes is empty, want at least one code for attention_drop")
		}
		if got.EvidenceQuote != "we should talk about pricing" {
			t.Errorf("EvidenceQuote = %q, want the passed-in quote", got.EvidenceQuote)
		}
		if got.ModelVersion != llmStubModelVersion {
			t.Errorf("ModelVersion = %q, want %q", got.ModelVersion, llmStubModelVersion)
		}
	})

	t.Run("on_track has no reason codes", func(t *testing.T) {
		got, err := generateLLMStubCandidate(context.Background(), feature, "on_track", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got.ReasonCodes) != 0 {
			t.Errorf("ReasonCodes = %v, want empty for on_track", got.ReasonCodes)
		}
	})

	t.Run("expired context falls back with an error", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 0)
		defer cancel()
		time.Sleep(time.Millisecond)

		_, err := generateLLMStubCandidate(ctx, feature, "attention_drop", "")
		if err == nil {
			t.Fatalf("expected an error from an already-expired context, got nil")
		}
	})
}

func TestDecideFeedback(t *testing.T) {
	feature := contract.CompactFeature{SessionID: "sess_1", AudienceID: "aud_1", TMs: 1000, AttentionScore: 0.2}
	state := gwredis.BaselineState{Status: gwredis.BaselineStatusWarmingUp}

	t.Run("llm disabled uses rule decision log", func(t *testing.T) {
		feedback, decision := decideFeedback(context.Background(), feature, state, false, "")

		if feedback.Source != "rule_default" {
			t.Errorf("feedback.Source = %q, want rule_default", feedback.Source)
		}
		if decision.Source != "rule" {
			t.Errorf("decision.Source = %q, want rule", decision.Source)
		}

		var detail contract.DecisionDetail
		if err := json.Unmarshal(decision.Decision, &detail); err != nil {
			t.Fatalf("unmarshal decision detail: %v", err)
		}
		if detail.EvidenceQuote != nil {
			t.Errorf("detail.EvidenceQuote = %v, want nil for rule fallback", detail.EvidenceQuote)
		}
	})

	t.Run("llm enabled uses llm_stub decision log with evidence", func(t *testing.T) {
		feedback, decision := decideFeedback(context.Background(), feature, state, true, "quarterly numbers look strong")

		if feedback.Source != "llm_stub" {
			t.Errorf("feedback.Source = %q, want llm_stub", feedback.Source)
		}
		if decision.Source != "llm_stub" {
			t.Errorf("decision.Source = %q, want llm_stub", decision.Source)
		}

		var detail contract.DecisionDetail
		if err := json.Unmarshal(decision.Decision, &detail); err != nil {
			t.Fatalf("unmarshal decision detail: %v", err)
		}
		if detail.EvidenceQuote == nil || *detail.EvidenceQuote != "quarterly numbers look strong" {
			t.Errorf("detail.EvidenceQuote = %v, want a pointer to the passed-in quote", detail.EvidenceQuote)
		}
		if detail.ModelVersion != llmStubModelVersion {
			t.Errorf("detail.ModelVersion = %q, want %q", detail.ModelVersion, llmStubModelVersion)
		}
	})
}

func TestNonEmptyPtr(t *testing.T) {
	if got := nonEmptyPtr(""); got != nil {
		t.Errorf("nonEmptyPtr(\"\") = %v, want nil", got)
	}
	if got := nonEmptyPtr("hello"); got == nil || *got != "hello" {
		t.Errorf("nonEmptyPtr(\"hello\") = %v, want pointer to \"hello\"", got)
	}
}
