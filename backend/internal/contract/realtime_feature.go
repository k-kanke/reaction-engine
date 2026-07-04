package contract

// RealtimeFeatureMessage is the payload the Chrome extension sends over the
// gateway WebSocket for type "realtime_feature".
type RealtimeFeatureMessage struct {
	Type      string           `json:"type"`
	SessionID string           `json:"session_id"`
	TMs       int64            `json:"t_ms"`
	Features  RealtimeFeatures `json:"features"`
}

type RealtimeFeatures struct {
	FaceTracks []FaceTrack `json:"face_tracks"`
}

type FaceTrack struct {
	AudienceID     string  `json:"audience_id"`
	AttentionScore float64 `json:"attention_score"`
}

// FeedbackEvent is returned to the Chrome extension over the same
// WebSocket connection.
type FeedbackEvent struct {
	Type         string `json:"type"`
	SessionID    string `json:"session_id"`
	TMs          int64  `json:"t_ms"`
	FeedbackType string `json:"feedback_type"`
	Severity     string `json:"severity"`
	Message      string `json:"message"`
	Source       string `json:"source"`
}

// CompactFeature is what the gateway stores in the Redis recent window per
// session_id + audience_id.
type CompactFeature struct {
	EventID            string  `json:"event_id"`
	SessionID          string  `json:"session_id"`
	AudienceID         string  `json:"audience_id"`
	TMs                int64   `json:"t_ms"`
	ServerReceivedAtMs int64   `json:"server_received_at_ms"`
	AttentionScore     float64 `json:"attention_score"`
}
