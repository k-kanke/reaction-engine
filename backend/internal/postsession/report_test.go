package postsession

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/k-kanke/reaction-engine/backend/internal/contract"
)

func moodSample(tMs int64, y float64) contract.MoodWaveSampleMessage {
	return contract.MoodWaveSampleMessage{TMs: tMs, Mood: contract.MoodValue{Y: y}}
}

func TestBuildReport_NoData(t *testing.T) {
	report := BuildReport("sess_empty", nil, nil, nil, nil, nil, nil, nil, time.Unix(0, 0))

	if report.Session.SessionID != "sess_empty" {
		t.Errorf("Session.SessionID = %q, want sess_empty", report.Session.SessionID)
	}
	if report.Purpose != "post_session_report" {
		t.Errorf("Purpose = %q, want post_session_report", report.Purpose)
	}
	if report.Source != "post_session_stub" {
		t.Errorf("Source = %q, want post_session_stub", report.Source)
	}
	if len(report.ImportantWindows) != 0 {
		t.Errorf("ImportantWindows = %+v, want empty", report.ImportantWindows)
	}
	if report.WaveOverview.Overall != "stable" {
		t.Errorf("WaveOverview.Overall = %q, want stable", report.WaveOverview.Overall)
	}
}

func TestBuildReport_DurationMin(t *testing.T) {
	samples := []contract.MoodWaveSampleMessage{
		moodSample(0, 0),
		moodSample(5*60*1000, 0), // 5 minutes later
	}
	report := BuildReport("sess_1", samples, nil, nil, nil, nil, nil, nil, time.Unix(0, 0))

	if report.Session.DurationMin != 5 {
		t.Errorf("DurationMin = %v, want 5", report.Session.DurationMin)
	}
	if report.MoodWaveSampleCount != 2 {
		t.Errorf("MoodWaveSampleCount = %d, want 2", report.MoodWaveSampleCount)
	}
}

func TestBuildReport_ImportantWindowAroundTrigger(t *testing.T) {
	// Session starts at t=0. The trigger fires at t=120000 (2min); the
	// +-90s window around it is [30000, 210000]. Recovery happens at
	// t=250000, outside that window, so the window itself still reads as
	// declined (start_y=0.05 at t=60000 -> end_y=-0.15 at t=150000).
	samples := []contract.MoodWaveSampleMessage{
		moodSample(0, 0.1),
		moodSample(60000, 0.05),
		moodSample(90000, -0.05),
		moodSample(120000, -0.2), // trigger fires here
		moodSample(150000, -0.15),
		moodSample(250000, 0.1), // recovered, outside the +-90s window
	}
	triggers := []contract.TriggerEvent{
		{EventID: "evt_trig_1", SessionID: "sess_1", TriggerID: "trig_1", Type: "wave_drop", Source: "chrome", TMs: 120000, PeakTMs: 120000, Delta: 0.3},
	}
	evidenceRefs := []EvidenceMediaRef{
		{TriggerID: "trig_1", MediaRef: "local://sessions/sess_1/evidence/trig_1.jpg"},
	}
	transcripts := []Transcript{
		{Speaker: "self", TStartMs: 100000, TEndMs: 105000, Text: "price discussion starts"},
		{Speaker: "self", TStartMs: 500000, TEndMs: 505000, Text: "unrelated, far outside the window"},
	}

	report := BuildReport("sess_1", samples, triggers, nil, transcripts, evidenceRefs, nil, nil, time.Unix(0, 0))

	if len(report.ImportantWindows) != 1 {
		t.Fatalf("len(ImportantWindows) = %d, want 1", len(report.ImportantWindows))
	}
	iw := report.ImportantWindows[0]

	// window = [120000-90000, 120000+90000] = [30000, 210000] -> 00:00:30 to 00:03:30
	if iw.Start != "00:00:30" {
		t.Errorf("Start = %q, want 00:00:30", iw.Start)
	}
	if iw.End != "00:03:30" {
		t.Errorf("End = %q, want 00:03:30", iw.End)
	}
	if iw.TranscriptSummary != "price discussion starts" {
		t.Errorf("TranscriptSummary = %q, want only the in-window chunk", iw.TranscriptSummary)
	}
	if len(iw.EvidenceRefs) != 1 || iw.EvidenceRefs[0] != "local://sessions/sess_1/evidence/trig_1.jpg" {
		t.Errorf("EvidenceRefs = %v, want the trig_1 media_ref", iw.EvidenceRefs)
	}

	if len(report.WaveOverview.DropSections) != 1 {
		t.Fatalf("DropSections = %v, want 1 entry (the window declined overall)", report.WaveOverview.DropSections)
	}
	if report.WaveOverview.Overall != "mostly_stable_with_1_drops" {
		t.Errorf("WaveOverview.Overall = %q, want mostly_stable_with_1_drops", report.WaveOverview.Overall)
	}
}

func TestBuildReport_RealtimeFeedbackHistorySortedByTMs(t *testing.T) {
	feedback := []contract.FeedbackEvent{
		{Type: "feedback_event", SessionID: "sess_1", TMs: 5000, FeedbackType: "on_track"},
		{Type: "feedback_event", SessionID: "sess_1", TMs: 1000, FeedbackType: "reaction_down_candidate"},
	}
	report := BuildReport("sess_1", nil, nil, feedback, nil, nil, nil, nil, time.Unix(0, 0))

	if len(report.RealtimeFeedbackHistory) != 2 {
		t.Fatalf("len(RealtimeFeedbackHistory) = %d, want 2", len(report.RealtimeFeedbackHistory))
	}
	if report.RealtimeFeedbackHistory[0].TMs != 1000 || report.RealtimeFeedbackHistory[1].TMs != 5000 {
		t.Errorf("RealtimeFeedbackHistory not sorted by t_ms: %+v", report.RealtimeFeedbackHistory)
	}
}

func TestBuildReport_BaselineContext(t *testing.T) {
	baselineJSON, _ := json.Marshal(map[string]any{"attention_score_avg": 0.65})
	baselines := []ParticipantBaseline{
		{AudienceID: "aud_2", Baseline: baselineJSON, SampleCount: 8, Confidence: 0.9},
		{AudienceID: "aud_1", Baseline: baselineJSON, SampleCount: 4, Confidence: 0.5},
	}
	visualSummaries := []VisualSummary{
		{AudienceID: "aud_1", CaptureID: "cap_1", VisualSummary: json.RawMessage(`{"face_quality":"usable"}`)},
	}

	report := BuildReport("sess_1", nil, nil, nil, nil, nil, baselines, visualSummaries, time.Unix(0, 0))

	if len(report.BaselineContext.Participants) != 2 {
		t.Fatalf("len(Participants) = %d, want 2", len(report.BaselineContext.Participants))
	}
	// sorted by audience_id
	if report.BaselineContext.Participants[0].AudienceID != "aud_1" {
		t.Errorf("Participants[0].AudienceID = %q, want aud_1", report.BaselineContext.Participants[0].AudienceID)
	}
	if string(report.BaselineContext.Participants[0].VisualSummary) != `{"face_quality":"usable"}` {
		t.Errorf("Participants[0].VisualSummary = %s, want the raw visual_summary JSON", report.BaselineContext.Participants[0].VisualSummary)
	}
	if report.BaselineContext.Participants[1].AudienceID != "aud_2" {
		t.Errorf("Participants[1].AudienceID = %q, want aud_2", report.BaselineContext.Participants[1].AudienceID)
	}
	if len(report.BaselineContext.Participants[1].VisualSummary) != 0 {
		t.Errorf("Participants[1].VisualSummary = %s, want empty (no visual summary for aud_2)", report.BaselineContext.Participants[1].VisualSummary)
	}
}

func TestDescribeOverall(t *testing.T) {
	cases := []struct {
		drops, peaks int
		want         string
	}{
		{0, 0, "stable"},
		{2, 0, "mostly_stable_with_2_drops"},
		{0, 3, "mostly_stable_with_3_peaks"},
		{1, 1, "mixed_with_1_drops_1_peaks"},
	}
	for _, tc := range cases {
		if got := describeOverall(tc.drops, tc.peaks); got != tc.want {
			t.Errorf("describeOverall(%d, %d) = %q, want %q", tc.drops, tc.peaks, got, tc.want)
		}
	}
}
