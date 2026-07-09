package pubsub

import (
	"encoding/json"
	"fmt"
	"io"
)

// PushMessage is the body Pub/Sub POSTs to a push subscription's endpoint.
// json.Unmarshal base64-decodes Data automatically since it's a []byte
// field -- see https://cloud.google.com/pubsub/docs/push#receive_push.
type PushMessage struct {
	Message struct {
		Data       []byte            `json:"data"`
		Attributes map[string]string `json:"attributes"`
		MessageID  string            `json:"messageId"`
	} `json:"message"`
	Subscription string `json:"subscription"`
}

// DecodePush reads and decodes one Pub/Sub push request body. Authenticity
// of the request itself isn't checked here -- Cloud Run's own IAM already
// rejects any caller besides the push subscription's dedicated service
// account before the request reaches the handler (see
// modules/pubsub's push_service_account_email wiring), the same pattern
// media-api relies on for its IAM-protected endpoints.
func DecodePush(body io.Reader) (PushMessage, error) {
	var msg PushMessage
	if err := json.NewDecoder(body).Decode(&msg); err != nil {
		return PushMessage{}, fmt.Errorf("pubsub: decode push body: %w", err)
	}
	return msg, nil
}
