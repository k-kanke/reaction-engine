package postsession

import (
	"encoding/json"
	"sort"
	"time"

	"github.com/k-kanke/reaction-engine/backend/internal/contract"
)

// attentionChangeThreshold mirrors internal/gateway's
// defaultAttentionThreshold: the same fixed cut used for realtime
// feedback, reused here so a post-session change point lines up with what
// the presenter would have seen live. Post-session Job doesn't call an
// LLM (that's Phase 14's Vertex AI / Gemini integration); this report is
// a deterministic stub, matching Phase 8/12's non-LLM stubs.
const attentionChangeThreshold = 0.4

// Report is the shape marshaled into reports.report (jsonb).
type Report struct {
	SessionID            string              `json:"session_id"`
	GeneratedAt          string              `json:"generated_at"`
	Source               string              `json:"source"`
	TranscriptChunkCount int                 `json:"transcript_chunk_count"`
	DecisionLogCount     int                 `json:"decision_log_count"`
	SignalSummaryCount   int                 `json:"signal_summary_count"`
	Participants         []ParticipantReport `json:"participants"`
}

// ParticipantReport summarizes one audience_id's session.
type ParticipantReport struct {
	AudienceID           string          `json:"audience_id"`
	SampleCount          int             `json:"sample_count"`
	AttentionAvg         float64         `json:"attention_avg"`
	AttentionMin         float64         `json:"attention_min"`
	BaselineAttentionAvg *float64        `json:"baseline_attention_avg,omitempty"`
	BaselineConfidence   *float64        `json:"baseline_confidence,omitempty"`
	VisualSummary        json.RawMessage `json:"visual_summary,omitempty"`
	ChangePoints         []ChangePoint   `json:"change_points"`
}

// ChangePoint is one attention_score crossing of attentionChangeThreshold.
type ChangePoint struct {
	TMs            int64   `json:"t_ms"`
	Type           string  `json:"type"` // "attention_drop" or "attention_recovery"
	AttentionScore float64 `json:"attention_score"`
}

type baselineFields struct {
	AttentionScoreAvg float64 `json:"attention_score_avg"`
}

// BuildReport assembles a deterministic post-session report from every
// input Phase 13 of plan/backend-local-docker-runbook.md calls for: local
// JSONL compact-raw features (for the attention_score timeline and change
// points), and Postgres transcripts/participant_baselines/visual_summaries/
// signal_summaries for per-participant context. It never calls an LLM.
func BuildReport(
	sessionID string,
	features []contract.FeatureEventPayload,
	transcripts []Transcript,
	baselines []ParticipantBaseline,
	visualSummaries []VisualSummary,
	signalSummaries []SignalSummary,
	generatedAt time.Time,
) Report {
	baselineByAudience := make(map[string]ParticipantBaseline, len(baselines))
	for _, b := range baselines {
		baselineByAudience[b.AudienceID] = b
	}

	visualSummaryByAudience := make(map[string]VisualSummary, len(visualSummaries))
	for _, v := range visualSummaries {
		visualSummaryByAudience[v.AudienceID] = v
	}

	type sample struct {
		tMs            int64
		attentionScore float64
	}
	samplesByAudience := make(map[string][]sample)
	var audienceOrder []string
	for _, payload := range features {
		for _, f := range payload.Features {
			if _, ok := samplesByAudience[f.AudienceID]; !ok {
				audienceOrder = append(audienceOrder, f.AudienceID)
			}
			samplesByAudience[f.AudienceID] = append(samplesByAudience[f.AudienceID], sample{tMs: f.TMs, attentionScore: f.AttentionScore})
		}
	}
	sort.Strings(audienceOrder)

	participants := make([]ParticipantReport, 0, len(audienceOrder))
	for _, audienceID := range audienceOrder {
		samples := samplesByAudience[audienceID]
		sort.Slice(samples, func(i, j int) bool { return samples[i].tMs < samples[j].tMs })

		sum, minAttention := 0.0, samples[0].attentionScore
		for _, s := range samples {
			sum += s.attentionScore
			if s.attentionScore < minAttention {
				minAttention = s.attentionScore
			}
		}

		changePoints := make([]ChangePoint, 0)
		wasBelow := samples[0].attentionScore < attentionChangeThreshold
		if wasBelow {
			changePoints = append(changePoints, ChangePoint{TMs: samples[0].tMs, Type: "attention_drop", AttentionScore: samples[0].attentionScore})
		}
		for _, s := range samples[1:] {
			isBelow := s.attentionScore < attentionChangeThreshold
			if isBelow == wasBelow {
				continue
			}
			changeType := "attention_recovery"
			if isBelow {
				changeType = "attention_drop"
			}
			changePoints = append(changePoints, ChangePoint{TMs: s.tMs, Type: changeType, AttentionScore: s.attentionScore})
			wasBelow = isBelow
		}

		report := ParticipantReport{
			AudienceID:   audienceID,
			SampleCount:  len(samples),
			AttentionAvg: sum / float64(len(samples)),
			AttentionMin: minAttention,
			ChangePoints: changePoints,
		}

		if baseline, ok := baselineByAudience[audienceID]; ok {
			var fields baselineFields
			if err := json.Unmarshal(baseline.Baseline, &fields); err == nil {
				avg := fields.AttentionScoreAvg
				report.BaselineAttentionAvg = &avg
			}
			confidence := baseline.Confidence
			report.BaselineConfidence = &confidence
		}

		if visualSummary, ok := visualSummaryByAudience[audienceID]; ok {
			report.VisualSummary = visualSummary.VisualSummary
		}

		participants = append(participants, report)
	}

	return Report{
		SessionID:            sessionID,
		GeneratedAt:          generatedAt.UTC().Format(time.RFC3339),
		Source:               "post_session_stub",
		TranscriptChunkCount: len(transcripts),
		DecisionLogCount:     decisionLogCountFromFeatures(features),
		SignalSummaryCount:   len(signalSummaries),
		Participants:         participants,
	}
}

func decisionLogCountFromFeatures(features []contract.FeatureEventPayload) int {
	count := 0
	for _, payload := range features {
		count += len(payload.DecisionLogs)
	}
	return count
}
