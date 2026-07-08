package contract

// SessionEndMessage is sent once over the WebSocket connection when the
// extension detects a session has ended (screen share stopped, tab closed,
// or the user clicked Stop -- see extension/src/sidebar.js's stopCapture).
// It's the trigger architecture.md describes as "session end で
// Post-session Job を起動する" (plan/post-session-report-implementation.md
// Step 5).
type SessionEndMessage struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id"`
}
