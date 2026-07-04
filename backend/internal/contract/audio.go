package contract

// AudioChunkMessage is the payload the Chrome extension sends over the
// gateway WebSocket for type "audio_chunk", riding the same connection as
// realtime_feature (architecture.md's "Audio Chunk（クライアント→Gateway）").
// PCM is never persisted; the gateway only tracks arrival timestamps to
// decide when to flush a transcript_chunk.
type AudioChunkMessage struct {
	Type       string `json:"type"`
	SessionID  string `json:"session_id"`
	Speaker    string `json:"speaker"`
	TMs        int64  `json:"t_ms"`
	SampleRate int    `json:"sample_rate"`
	PCM        string `json:"pcm"`
}

// TranscriptChunk is a finalized Speech-to-Text result
// (architecture.md's "Transcript Chunk（Gateway→永続化）"). Phase 11 builds
// these deterministically (no real STT call yet); Speaker is "self"
// (presenter mic) or "other" (tab audio).
type TranscriptChunk struct {
	EventID       string  `json:"event_id"`
	Type          string  `json:"type"`
	SchemaVersion int     `json:"schema_version"`
	SessionID     string  `json:"session_id"`
	Speaker       string  `json:"speaker"`
	TStartMs      int64   `json:"t_start_ms"`
	TEndMs        int64   `json:"t_end_ms"`
	Text          string  `json:"text"`
	Confidence    float64 `json:"confidence"`
	IsFinal       bool    `json:"is_final"`
}
