package contract

// MoodWaveSampleMessage is the payload the Chrome extension sends over the
// gateway WebSocket for type "mood_wave_sample" (architecture.md's "Mood
// Wave Sample（Chrome -> Gateway）"). It replaces RealtimeFeatureMessage:
// Chrome compresses raw face/gaze/motion/nod/VAD features into this single
// lightweight time-series point before sending, so the server never sees
// raw feature data. Trigger and EvidenceFrame are nil on the common,
// untriggered sample and set together when Chrome's local moment trigger
// fires.
type MoodWaveSampleMessage struct {
	Type               string            `json:"type"`
	SchemaVersion      int               `json:"schema_version"`
	SessionID          string            `json:"session_id"`
	TMs                int64             `json:"t_ms"`
	MeetingProvider    string            `json:"meeting_provider"`
	Source             string            `json:"source"`
	Mood               MoodValue         `json:"mood"`
	AttentionY         float64           `json:"attention_y"`
	Signals            MoodWaveSignals   `json:"signals"`
	Quality            MoodWaveQuality   `json:"quality"`
	ClientModelVersion map[string]string `json:"client_model_version,omitempty"`
	Trigger            *TriggerInfo      `json:"trigger,omitempty"`
	EvidenceFrame      *EvidenceFrameRef `json:"evidence_frame,omitempty"`
}

// MoodValue is mood_wave_sample.mood: the room-level mood EMA, its rolling
// baseline, and their deviation (the wave's y axis).
type MoodValue struct {
	Value    float64 `json:"value"`
	Baseline float64 `json:"baseline"`
	Y        float64 `json:"y"`
}

// MoodWaveSignals is mood_wave_sample.signals: the compact, non-identifying
// room signals the mood composition drew from. No per-face bbox/landmark
// data crosses the wire, matching architecture.md's "raw feature を常時
// サーバーへ送らない" design principle.
type MoodWaveSignals struct {
	VisibleFaces int     `json:"visible_faces"`
	NodRatio     float64 `json:"nod_ratio"`
	SpeechRatio  float64 `json:"speech_ratio"`
	BrowFlag     bool    `json:"brow_flag"`
}

// MoodWaveQuality is mood_wave_sample.quality: whether the client is still
// warming up its baseline and how confident it is in this sample.
type MoodWaveQuality struct {
	Calibrating bool    `json:"calibrating"`
	Confidence  float64 `json:"confidence"`
}

// TriggerInfo is mood_wave_sample.trigger: set when Chrome's local moment
// trigger (wave_drop/wave_rise/nod) fires on this sample.
type TriggerInfo struct {
	TriggerID string  `json:"trigger_id"`
	Type      string  `json:"type"`
	Source    string  `json:"source"`
	PeakTMs   int64   `json:"peak_t_ms"`
	Delta     float64 `json:"delta"`
}

// EvidenceFrameRef is mood_wave_sample.evidence_frame: the media_ref Chrome
// reserved via the Media API upload-url call for this trigger, sent
// alongside the trigger without waiting for the PUT to Cloud Storage (local:
// filesystem) to finish. UploadStatus is "uploading" here; it becomes
// "uploaded" once EvidenceUploadCompleteMessage (or the Media API's
// /complete endpoint) confirms the bytes landed.
type EvidenceFrameRef struct {
	CaptureID     string `json:"capture_id"`
	MediaRef      string `json:"media_ref"`
	UploadStatus  string `json:"upload_status"`
	SnapshotTMs   int64  `json:"snapshot_t_ms"`
	SnapshotLagMs int64  `json:"snapshot_lag_ms"`
	ContentType   string `json:"content_type"`
}

// EvidenceUploadCompleteMessage is the payload Chrome (or the Media API) can
// send over the same WebSocket once an evidence_frame's upload finishes,
// per architecture.md's データ契約 section. It lets the gateway update the
// trigger's evidence frame status without waiting on a Media API poll.
type EvidenceUploadCompleteMessage struct {
	Type         string `json:"type"`
	SessionID    string `json:"session_id"`
	TriggerID    string `json:"trigger_id"`
	CaptureID    string `json:"capture_id"`
	MediaRef     string `json:"media_ref"`
	UploadStatus string `json:"upload_status"`
}
