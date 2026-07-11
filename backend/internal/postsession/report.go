package postsession

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/k-kanke/reaction-engine/backend/internal/contract"
	"github.com/k-kanke/reaction-engine/backend/internal/realtime"
)

// importantWindowRadiusMs is how far before/after each trigger_event an
// important_windows entry extends, per plan/mood-wave-contract-migration.md
// Step 10 ("important_windows の抽出は「trigger_events の前後 ±90秒」を
// 基本ロジックにし").
const importantWindowRadiusMs = 90 * 1000

// jst is a fixed +9:00 offset, not time.LoadLocation("Asia/Tokyo"): the
// backend Docker image is built FROM gcr.io/distroless/static-debian12
// (see backend/Dockerfile), which has no tzdata, so LoadLocation would
// fail at runtime. Japan has no DST, so a fixed offset is exact, not an
// approximation.
var jst = time.FixedZone("JST", 9*60*60)

// Report is the shape marshaled into reports.report (jsonb), matching
// architecture.md's post_session_report input example under
// 全体FBレポートフロー (purpose/session/wave_overview/important_windows/
// realtime_feedback_history/baseline_context). It never calls an LLM;
// Source stays "post_session_stub".
type Report struct {
	Purpose                 string                   `json:"purpose"`
	Session                 ReportSession            `json:"session"`
	GeneratedAt             string                   `json:"generated_at"`
	Source                  string                   `json:"source"`
	TranscriptChunkCount    int                      `json:"transcript_chunk_count"`
	MoodWaveSampleCount     int                      `json:"mood_wave_sample_count"`
	WaveOverview            WaveOverview             `json:"wave_overview"`
	ImportantWindows        []ImportantWindow        `json:"important_windows"`
	RealtimeFeedbackHistory []contract.FeedbackEvent `json:"realtime_feedback_history"`
	BaselineContext         BaselineContext          `json:"baseline_context"`
}

// ReportSession is architecture.md's post_session_report "session" object.
type ReportSession struct {
	SessionID   string  `json:"session_id"`
	DurationMin float64 `json:"duration_min"`
}

// WaveOverview is architecture.md's post_session_report "wave_overview"
// object: a whole-session description built from which important_windows
// turned out to be drops vs peaks. Overall is a deterministic, non-LLM
// label (e.g. "mostly_stable_with_2_drops") standing in for what a real
// system would have an LLM phrase, matching architecture.md's own example
// shape ("mostly_stable_with_two_drops").
type WaveOverview struct {
	Overall              string   `json:"overall"`
	PeakPositiveSections []string `json:"peak_positive_sections"`
	DropSections         []string `json:"drop_sections"`
}

// ImportantWindow is one entry of architecture.md's post_session_report
// "important_windows": the ±90s window around one trigger_event, its mood
// wave summary (reusing internal/realtime's window-building logic from the
// realtime path, Step 5), a deterministic transcript excerpt, and any
// evidence_frame media_refs captured for that trigger.
type ImportantWindow struct {
	Start             string                   `json:"start"`
	End               string                   `json:"end"`
	MoodWaveSummary   realtime.MoodWaveSummary `json:"mood_wave_summary"`
	TranscriptSummary string                   `json:"transcript_summary,omitempty"`
	EvidenceRefs      []string                 `json:"evidence_refs,omitempty"`
}

// BaselineContext is architecture.md's post_session_report
// "baseline_context": every participant's baseline + visual summary, the
// same per-participant context the realtime evidence pack (Step 5) draws
// on, reused here for the whole-session report.
type BaselineContext struct {
	Participants []ParticipantBaselineContext `json:"participants"`
}

// ParticipantBaselineContext summarizes one audience_id's baseline state.
type ParticipantBaselineContext struct {
	AudienceID    string          `json:"audience_id"`
	Baseline      json.RawMessage `json:"baseline,omitempty"`
	Confidence    float64         `json:"confidence"`
	VisualSummary json.RawMessage `json:"visual_summary,omitempty"`
}

// EvidenceMediaRef pairs an evidence_frame's media_ref with the trigger_id
// it was captured for (media_refs.trigger_id, Step 7), so BuildReport can
// attach it to the matching important_windows entry.
type EvidenceMediaRef struct {
	TriggerID string
	MediaRef  string
}

// BuildReport assembles architecture.md's post_session_report input from
// every Step 10 input source: mood_wave_sample JSONL (whole-session
// wave_overview and, sliced around each trigger, important_windows),
// trigger_events (which windows matter) + evidence media_refs (their
// evidence images), feedback_events (realtime_feedback_history),
// transcripts (window summaries), and
// participant_baselines/visual_summaries (baseline_context). It replaces
// the old attention_score/CompactFeature-based report — mood_wave_sample
// is a session-level aggregate, not per-participant, so there is no
// per-participant attention timeline to report anymore.
func BuildReport(
	sessionID string,
	moodWaveSamples []contract.MoodWaveSampleMessage,
	triggerEvents []contract.TriggerEvent,
	feedbackEvents []contract.FeedbackEvent,
	transcripts []Transcript,
	evidenceRefs []EvidenceMediaRef,
	baselines []ParticipantBaseline,
	visualSummaries []VisualSummary,
	generatedAt time.Time,
) Report {
	sort.Slice(moodWaveSamples, func(i, j int) bool { return moodWaveSamples[i].TMs < moodWaveSamples[j].TMs })

	var sessionStartTMs, sessionEndTMs int64
	if len(moodWaveSamples) > 0 {
		sessionStartTMs = moodWaveSamples[0].TMs
		sessionEndTMs = moodWaveSamples[len(moodWaveSamples)-1].TMs
	}
	durationMin := 0.0
	if sessionEndTMs > sessionStartTMs {
		durationMin = float64(sessionEndTMs-sessionStartTMs) / 60000.0
	}

	evidenceRefsByTrigger := make(map[string][]string, len(evidenceRefs))
	for _, ref := range evidenceRefs {
		evidenceRefsByTrigger[ref.TriggerID] = append(evidenceRefsByTrigger[ref.TriggerID], ref.MediaRef)
	}

	sort.Slice(triggerEvents, func(i, j int) bool { return triggerEvents[i].TMs < triggerEvents[j].TMs })

	importantWindows := make([]ImportantWindow, 0, len(triggerEvents))
	var dropSections, peakSections []string
	for _, trig := range triggerEvents {
		windowEndTMs := trig.TMs + importantWindowRadiusMs
		durationSec := int(2 * importantWindowRadiusMs / 1000)
		meta, moodWave := realtime.BuildWindow(moodWaveSamples, windowEndTMs, durationSec)

		startLabel := formatElapsed(meta.StartTMs, sessionStartTMs)
		endLabel := formatElapsed(meta.EndTMs, sessionStartTMs)

		importantWindows = append(importantWindows, ImportantWindow{
			Start:             startLabel,
			End:               endLabel,
			MoodWaveSummary:   moodWave.Summary,
			TranscriptSummary: summarizeTranscript(transcripts, meta.StartTMs, meta.EndTMs),
			EvidenceRefs:      evidenceRefsByTrigger[trig.TriggerID],
		})

		section := fmt.Sprintf("%s-%s", startLabel, endLabel)
		switch moodWave.Summary.Overall {
		case "declined":
			dropSections = append(dropSections, section)
		case "improved":
			peakSections = append(peakSections, section)
		}
	}

	sort.Slice(feedbackEvents, func(i, j int) bool { return feedbackEvents[i].TMs < feedbackEvents[j].TMs })

	visualByAudience := make(map[string]VisualSummary, len(visualSummaries))
	for _, v := range visualSummaries {
		visualByAudience[v.AudienceID] = v
	}
	participants := make([]ParticipantBaselineContext, 0, len(baselines))
	for _, b := range baselines {
		p := ParticipantBaselineContext{AudienceID: b.AudienceID, Baseline: b.Baseline, Confidence: b.Confidence}
		if v, ok := visualByAudience[b.AudienceID]; ok {
			p.VisualSummary = v.VisualSummary
		}
		participants = append(participants, p)
	}
	sort.Slice(participants, func(i, j int) bool { return participants[i].AudienceID < participants[j].AudienceID })

	return Report{
		Purpose:                 "post_session_report",
		Session:                 ReportSession{SessionID: sessionID, DurationMin: durationMin},
		GeneratedAt:             generatedAt.In(jst).Format(time.RFC3339),
		Source:                  "post_session_stub",
		TranscriptChunkCount:    len(transcripts),
		MoodWaveSampleCount:     len(moodWaveSamples),
		WaveOverview:            WaveOverview{Overall: describeOverall(len(dropSections), len(peakSections)), PeakPositiveSections: peakSections, DropSections: dropSections},
		ImportantWindows:        importantWindows,
		RealtimeFeedbackHistory: feedbackEvents,
		BaselineContext:         BaselineContext{Participants: participants},
	}
}

// formatElapsed renders tMs as HH:MM:SS elapsed since sessionStartTMs,
// matching architecture.md's important_windows example ("00:18:20").
// Negative elapsed (a window's start before the first mood_wave_sample)
// clamps to zero rather than going negative.
func formatElapsed(tMs, sessionStartTMs int64) string {
	elapsed := tMs - sessionStartTMs
	if elapsed < 0 {
		elapsed = 0
	}
	d := time.Duration(elapsed) * time.Millisecond
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
}

// describeOverall is a deterministic, non-LLM stand-in for the whole-session
// summary phrase architecture.md's example shows
// ("mostly_stable_with_two_drops"). A real system would have an LLM write
// this; this stub just counts.
func describeOverall(dropCount, peakCount int) string {
	switch {
	case dropCount == 0 && peakCount == 0:
		return "stable"
	case dropCount > 0 && peakCount == 0:
		return fmt.Sprintf("mostly_stable_with_%d_drops", dropCount)
	case dropCount == 0 && peakCount > 0:
		return fmt.Sprintf("mostly_stable_with_%d_peaks", peakCount)
	default:
		return fmt.Sprintf("mixed_with_%d_drops_%d_peaks", dropCount, peakCount)
	}
}

// summarizeTranscript concatenates transcript text starting within
// [startTMs, endTMs] as a deterministic, non-LLM proxy for
// architecture.md's transcript_summary ("価格説明に入った区間" in its
// example) — a real system would summarize this with an LLM; this stub
// just quotes what was said.
func summarizeTranscript(transcripts []Transcript, startTMs, endTMs int64) string {
	var parts []string
	for _, t := range transcripts {
		if t.TStartMs >= startTMs && t.TStartMs <= endTMs {
			parts = append(parts, t.Text)
		}
	}
	return strings.Join(parts, " ")
}
