package contract

import "encoding/json"

// DecisionLog mirrors one row of the `decision_logs` table (migration
// 000001_initial_schema). Source is exactly "rule" or "llm_stub" — Phase
// 12 of plan/backend-local-docker-runbook.md doesn't call a real LLM yet.
// Decision carries the full candidate/rule output that led to the paired
// feedback_event.
type DecisionLog struct {
	EventID    string          `json:"event_id"`
	SessionID  string          `json:"session_id"`
	AudienceID string          `json:"audience_id"`
	TMs        int64           `json:"t_ms"`
	Source     string          `json:"source"`
	Decision   json.RawMessage `json:"decision"`
}

// DecisionDetail is the shape marshaled into DecisionLog.Decision: the
// feedback candidate plus the reasoning behind it, whether it came from
// the rule fallback or the (stubbed) realtime LLM path. EvidenceQuote
// mirrors architecture.md's feedback_event convention: a transcript
// excerpt for the LLM path, nil for rule fallback.
type DecisionDetail struct {
	FeedbackType  string   `json:"feedback_type"`
	Severity      string   `json:"severity"`
	Message       string   `json:"message"`
	ReasonCodes   []string `json:"reason_codes,omitempty"`
	EvidenceQuote *string  `json:"evidence_quote"`
	ModelVersion  string   `json:"model_version,omitempty"`
	Confidence    float64  `json:"confidence"`
}
