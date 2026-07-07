package realtime

import (
	"math"
	"sort"

	"github.com/k-kanke/reaction-engine/backend/internal/contract"
)

// WindowDurationSec is the realtime evidence window length architecture.md
// uses throughout リアルタイムFBフロー (30s mood wave + transcript window).
const WindowDurationSec = 30

// slopeDeadbandPerSec is how close to zero mood_wave.summary.slope_per_sec
// must be to count as "stable" rather than "declined"/"improved". Chosen so
// a window that barely drifts within MOOD_TRIGGER_DELTA-scale noise
// (extension/src/sidebar.js) doesn't read as a trend; refinable once real
// session data is available.
const slopeDeadbandPerSec = 0.002

// WindowMeta is architecture.md's realtime LLM input "window" object: the
// slice boundaries and (if this window was built for an accepted trigger)
// which trigger caused it.
type WindowMeta struct {
	StartTMs    int64  `json:"start_t_ms"`
	EndTMs      int64  `json:"end_t_ms"`
	DurationSec int    `json:"duration_sec"`
	TriggerTMs  int64  `json:"trigger_t_ms,omitempty"`
	TriggerType string `json:"trigger_type,omitempty"`
}

// MoodWavePoint is one point of mood_wave.points in architecture.md's
// realtime LLM input.
type MoodWavePoint struct {
	TMs        int64   `json:"t_ms"`
	Y          float64 `json:"y"`
	AttentionY float64 `json:"attention_y"`
}

// MoodWaveSummary is mood_wave.summary: architecture.md's window-level
// trend description (§リアルタイムFBフロー's realtime LLM input example).
type MoodWaveSummary struct {
	Overall     string  `json:"overall"` // "declined" | "improved" | "stable"
	StartY      float64 `json:"start_y"`
	EndY        float64 `json:"end_y"`
	MinY        float64 `json:"min_y"`
	MaxY        float64 `json:"max_y"`
	SlopePerSec float64 `json:"slope_per_sec"`
	Volatility  float64 `json:"volatility"`
}

// MoodWaveQuality is mood_wave.quality: averaged sample-quality signals
// over the window.
type MoodWaveQuality struct {
	VisibleFacesAvg float64 `json:"visible_faces_avg"`
	ConfidenceAvg   float64 `json:"confidence_avg"`
}

// MoodWave is architecture.md's realtime LLM input "mood_wave" object.
type MoodWave struct {
	Points  []MoodWavePoint `json:"points"`
	Summary MoodWaveSummary `json:"summary"`
	Quality MoodWaveQuality `json:"quality"`
}

// BuildWindow slices samples to [endTMs-durationSec*1000, endTMs], sorts
// them oldest-first, and computes WindowMeta + MoodWave (points + summary +
// quality) per architecture.md's リアルタイムFBフロー step 10 ("直近30秒の
// mood_wave_window、transcript_window...を組み立てる"). samples need not be
// pre-filtered or pre-sorted; the caller (HandleTrigger) is expected to
// have already fetched roughly this range from Redis, but BuildWindow
// re-filters defensively.
func BuildWindow(samples []contract.MoodWaveSampleMessage, endTMs int64, durationSec int) (WindowMeta, MoodWave) {
	startTMs := endTMs - int64(durationSec)*1000

	filtered := make([]contract.MoodWaveSampleMessage, 0, len(samples))
	for _, s := range samples {
		if s.TMs >= startTMs && s.TMs <= endTMs {
			filtered = append(filtered, s)
		}
	}
	sort.Slice(filtered, func(i, j int) bool { return filtered[i].TMs < filtered[j].TMs })

	meta := WindowMeta{StartTMs: startTMs, EndTMs: endTMs, DurationSec: durationSec}

	if len(filtered) == 0 {
		return meta, MoodWave{Points: []MoodWavePoint{}, Summary: MoodWaveSummary{Overall: "stable"}}
	}

	points := make([]MoodWavePoint, 0, len(filtered))
	minY, maxY := filtered[0].Mood.Y, filtered[0].Mood.Y
	sumY, sumConfidence, sumVisibleFaces := 0.0, 0.0, 0.0
	for _, s := range filtered {
		points = append(points, MoodWavePoint{TMs: s.TMs, Y: s.Mood.Y, AttentionY: s.AttentionY})
		if s.Mood.Y < minY {
			minY = s.Mood.Y
		}
		if s.Mood.Y > maxY {
			maxY = s.Mood.Y
		}
		sumY += s.Mood.Y
		sumConfidence += s.Quality.Confidence
		sumVisibleFaces += float64(s.Signals.VisibleFaces)
	}

	startY := filtered[0].Mood.Y
	endY := filtered[len(filtered)-1].Mood.Y
	meanY := sumY / float64(len(filtered))

	elapsedSec := float64(filtered[len(filtered)-1].TMs-filtered[0].TMs) / 1000.0
	slopePerSec := 0.0
	if elapsedSec > 0 {
		slopePerSec = (endY - startY) / elapsedSec
	}

	variance := 0.0
	for _, p := range points {
		variance += (p.Y - meanY) * (p.Y - meanY)
	}
	volatility := 0.0
	if len(points) > 0 {
		volatility = math.Sqrt(variance / float64(len(points)))
	}

	overall := "stable"
	switch {
	case slopePerSec < -slopeDeadbandPerSec:
		overall = "declined"
	case slopePerSec > slopeDeadbandPerSec:
		overall = "improved"
	}

	summary := MoodWaveSummary{
		Overall:     overall,
		StartY:      startY,
		EndY:        endY,
		MinY:        minY,
		MaxY:        maxY,
		SlopePerSec: slopePerSec,
		Volatility:  volatility,
	}
	quality := MoodWaveQuality{
		VisibleFacesAvg: sumVisibleFaces / float64(len(filtered)),
		ConfidenceAvg:   sumConfidence / float64(len(filtered)),
	}

	return meta, MoodWave{Points: points, Summary: summary, Quality: quality}
}
