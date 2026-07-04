package postsession

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/k-kanke/reaction-engine/backend/internal/contract"
)

func featurePayload(audienceID string, tMs int64, attentionScore float64) contract.FeatureEventPayload {
	return contract.FeatureEventPayload{
		SessionID: "sess_1",
		TMs:       tMs,
		Features: []contract.CompactFeature{
			{SessionID: "sess_1", AudienceID: audienceID, TMs: tMs, AttentionScore: attentionScore},
		},
	}
}

func TestBuildReportChangePoints(t *testing.T) {
	features := []contract.FeatureEventPayload{
		featurePayload("aud_1", 1000, 0.6), // above threshold (0.4)
		featurePayload("aud_1", 2000, 0.3), // drop
		featurePayload("aud_1", 3000, 0.2), // still below, no new change point
		featurePayload("aud_1", 4000, 0.5), // recovery
	}

	report := BuildReport("sess_1", features, nil, nil, nil, nil, time.Unix(0, 0))

	if len(report.Participants) != 1 {
		t.Fatalf("len(Participants) = %d, want 1", len(report.Participants))
	}
	p := report.Participants[0]

	if p.AudienceID != "aud_1" {
		t.Errorf("AudienceID = %q, want aud_1", p.AudienceID)
	}
	if p.SampleCount != 4 {
		t.Errorf("SampleCount = %d, want 4", p.SampleCount)
	}
	if p.AttentionMin != 0.2 {
		t.Errorf("AttentionMin = %v, want 0.2", p.AttentionMin)
	}
	wantAvg := (0.6 + 0.3 + 0.2 + 0.5) / 4
	if diff := p.AttentionAvg - wantAvg; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("AttentionAvg = %v, want %v", p.AttentionAvg, wantAvg)
	}

	if len(p.ChangePoints) != 2 {
		t.Fatalf("len(ChangePoints) = %d, want 2 (one drop, one recovery): %+v", len(p.ChangePoints), p.ChangePoints)
	}
	if p.ChangePoints[0].Type != "attention_drop" || p.ChangePoints[0].TMs != 2000 {
		t.Errorf("ChangePoints[0] = %+v, want drop at t_ms=2000", p.ChangePoints[0])
	}
	if p.ChangePoints[1].Type != "attention_recovery" || p.ChangePoints[1].TMs != 4000 {
		t.Errorf("ChangePoints[1] = %+v, want recovery at t_ms=4000", p.ChangePoints[1])
	}
}

func TestBuildReportStartsBelowThreshold(t *testing.T) {
	features := []contract.FeatureEventPayload{
		featurePayload("aud_1", 1000, 0.1),
	}

	report := BuildReport("sess_1", features, nil, nil, nil, nil, time.Unix(0, 0))

	p := report.Participants[0]
	if len(p.ChangePoints) != 1 || p.ChangePoints[0].Type != "attention_drop" {
		t.Fatalf("ChangePoints = %+v, want a single attention_drop for the first sample", p.ChangePoints)
	}
}

func TestBuildReportBaselineAndVisualSummary(t *testing.T) {
	baselineJSON, _ := json.Marshal(baselineFields{AttentionScoreAvg: 0.65})
	features := []contract.FeatureEventPayload{
		featurePayload("aud_1", 1000, 0.6),
	}
	baselines := []ParticipantBaseline{
		{AudienceID: "aud_1", Baseline: baselineJSON, SampleCount: 8, Confidence: 0.9},
	}
	visualSummaries := []VisualSummary{
		{AudienceID: "aud_1", CaptureID: "cap_1", VisualSummary: json.RawMessage(`{"face_quality":"usable"}`)},
	}

	report := BuildReport("sess_1", features, nil, baselines, visualSummaries, nil, time.Unix(0, 0))

	p := report.Participants[0]
	if p.BaselineAttentionAvg == nil || *p.BaselineAttentionAvg != 0.65 {
		t.Errorf("BaselineAttentionAvg = %v, want pointer to 0.65", p.BaselineAttentionAvg)
	}
	if p.BaselineConfidence == nil || *p.BaselineConfidence != 0.9 {
		t.Errorf("BaselineConfidence = %v, want pointer to 0.9", p.BaselineConfidence)
	}
	if string(p.VisualSummary) != `{"face_quality":"usable"}` {
		t.Errorf("VisualSummary = %s, want the raw visual_summary JSON", p.VisualSummary)
	}
}

func TestBuildReportCounts(t *testing.T) {
	features := []contract.FeatureEventPayload{
		{
			SessionID: "sess_1",
			Features: []contract.CompactFeature{
				{SessionID: "sess_1", AudienceID: "aud_1", TMs: 1000, AttentionScore: 0.5},
			},
			DecisionLogs: []contract.DecisionLog{
				{EventID: "evt_1", SessionID: "sess_1", AudienceID: "aud_1"},
				{EventID: "evt_2", SessionID: "sess_1", AudienceID: "aud_1"},
			},
		},
	}
	transcripts := []Transcript{{Speaker: "self", Text: "hello"}}
	signalSummaries := []SignalSummary{{AudienceID: "aud_1", TMs: 1000}}

	report := BuildReport("sess_1", features, transcripts, nil, nil, signalSummaries, time.Unix(0, 0))

	if report.TranscriptChunkCount != 1 {
		t.Errorf("TranscriptChunkCount = %d, want 1", report.TranscriptChunkCount)
	}
	if report.DecisionLogCount != 2 {
		t.Errorf("DecisionLogCount = %d, want 2", report.DecisionLogCount)
	}
	if report.SignalSummaryCount != 1 {
		t.Errorf("SignalSummaryCount = %d, want 1", report.SignalSummaryCount)
	}
	if report.Source != "post_session_stub" {
		t.Errorf("Source = %q, want post_session_stub", report.Source)
	}
}

func TestBuildReportNoParticipants(t *testing.T) {
	report := BuildReport("sess_empty", nil, nil, nil, nil, nil, time.Unix(0, 0))

	if len(report.Participants) != 0 {
		t.Errorf("Participants = %+v, want empty", report.Participants)
	}
	if report.SessionID != "sess_empty" {
		t.Errorf("SessionID = %q, want sess_empty", report.SessionID)
	}
}
