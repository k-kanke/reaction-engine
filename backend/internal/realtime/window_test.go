package realtime

import (
	"testing"

	"github.com/k-kanke/reaction-engine/backend/internal/contract"
)

func sample(tMs int64, y, attentionY float64) contract.MoodWaveSampleMessage {
	return contract.MoodWaveSampleMessage{
		TMs:        tMs,
		Mood:       contract.MoodValue{Y: y},
		AttentionY: attentionY,
		Signals:    contract.MoodWaveSignals{VisibleFaces: 5},
		Quality:    contract.MoodWaveQuality{Confidence: 0.8},
	}
}

func TestBuildWindow(t *testing.T) {
	t.Run("empty samples yields stable summary with no points", func(t *testing.T) {
		meta, wave := BuildWindow(nil, 30000, 30)
		if meta.StartTMs != 0 || meta.EndTMs != 30000 || meta.DurationSec != 30 {
			t.Errorf("meta = %+v, want start=0 end=30000 duration=30", meta)
		}
		if len(wave.Points) != 0 {
			t.Errorf("Points = %v, want empty", wave.Points)
		}
		if wave.Summary.Overall != "stable" {
			t.Errorf("Overall = %q, want stable", wave.Summary.Overall)
		}
	})

	t.Run("declining slope classified as declined", func(t *testing.T) {
		samples := []contract.MoodWaveSampleMessage{
			sample(0, 0.10, 0.02),
			sample(10000, 0.00, 0.00),
			sample(20000, -0.10, -0.02),
			sample(30000, -0.20, -0.04),
		}
		meta, wave := BuildWindow(samples, 30000, 30)
		if meta.StartTMs != 0 {
			t.Errorf("StartTMs = %d, want 0", meta.StartTMs)
		}
		if wave.Summary.Overall != "declined" {
			t.Errorf("Overall = %q, want declined (slope=%v)", wave.Summary.Overall, wave.Summary.SlopePerSec)
		}
		if wave.Summary.StartY != 0.10 || wave.Summary.EndY != -0.20 {
			t.Errorf("StartY/EndY = %v/%v, want 0.10/-0.20", wave.Summary.StartY, wave.Summary.EndY)
		}
		if wave.Summary.MinY != -0.20 || wave.Summary.MaxY != 0.10 {
			t.Errorf("MinY/MaxY = %v/%v, want -0.20/0.10", wave.Summary.MinY, wave.Summary.MaxY)
		}
		if len(wave.Points) != 4 {
			t.Errorf("len(Points) = %d, want 4", len(wave.Points))
		}
		if wave.Quality.VisibleFacesAvg != 5 {
			t.Errorf("VisibleFacesAvg = %v, want 5", wave.Quality.VisibleFacesAvg)
		}
	})

	t.Run("improving slope classified as improved", func(t *testing.T) {
		samples := []contract.MoodWaveSampleMessage{
			sample(0, -0.10, 0),
			sample(30000, 0.15, 0),
		}
		_, wave := BuildWindow(samples, 30000, 30)
		if wave.Summary.Overall != "improved" {
			t.Errorf("Overall = %q, want improved (slope=%v)", wave.Summary.Overall, wave.Summary.SlopePerSec)
		}
	})

	t.Run("flat samples within deadband classified as stable", func(t *testing.T) {
		samples := []contract.MoodWaveSampleMessage{
			sample(0, 0.01, 0),
			sample(30000, 0.01, 0),
		}
		_, wave := BuildWindow(samples, 30000, 30)
		if wave.Summary.Overall != "stable" {
			t.Errorf("Overall = %q, want stable", wave.Summary.Overall)
		}
	})

	t.Run("filters out samples outside the window", func(t *testing.T) {
		samples := []contract.MoodWaveSampleMessage{
			sample(-5000, 0.5, 0), // before window start (0)
			sample(10000, 0.0, 0),
			sample(35000, -0.5, 0), // after window end (30000)
		}
		_, wave := BuildWindow(samples, 30000, 30)
		if len(wave.Points) != 1 {
			t.Fatalf("len(Points) = %d, want 1 (only the in-window sample)", len(wave.Points))
		}
		if wave.Points[0].TMs != 10000 {
			t.Errorf("Points[0].TMs = %d, want 10000", wave.Points[0].TMs)
		}
	})
}
