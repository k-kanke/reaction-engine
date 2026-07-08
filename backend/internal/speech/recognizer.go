// Package speech wraps Google Cloud Speech-to-Text streaming recognition
// for architecture.md's Speech-to-Text / Transcript section: the gateway
// keeps one streaming session per (session_id, speaker) and forwards only
// final results as transcript_chunk. This package owns exactly one bounded
// StreamingRecognize call per Stream; internal/gateway is responsible for
// closing a Stream before Google's ~305s per-stream duration limit and
// opening a fresh one (architecture.md: "Gateway が会議中に定期的にストリー
// ムを再接続する"), since that lifecycle is tied to gateway's per-connection
// state, not to this package.
package speech

import (
	"context"
	"fmt"

	speechapi "cloud.google.com/go/speech/apiv1"
	"cloud.google.com/go/speech/apiv1/speechpb"
	"google.golang.org/api/option"
)

// Recognizer opens new streaming recognition sessions. GoogleRecognizer is
// the production implementation; tests can substitute a fake.
type Recognizer interface {
	NewStream(ctx context.Context, sampleRateHertz int32, languageCode string) (*Stream, error)
}

// Result is one final transcription result from a Stream.
type Result struct {
	Text       string
	Confidence float64
}

// GoogleRecognizer is the Recognizer backed by the real Cloud Speech-to-Text
// API. It follows the same auth pattern as internal/media's GCSMediaStore:
// an explicit credentials file for local dev, ADC (the Cloud Run attached
// service account) when credentialsFile is empty.
type GoogleRecognizer struct {
	client *speechapi.Client
}

func NewGoogleRecognizer(ctx context.Context, credentialsFile string) (*GoogleRecognizer, error) {
	var opts []option.ClientOption
	if credentialsFile != "" {
		opts = append(opts, option.WithCredentialsFile(credentialsFile))
	}

	client, err := speechapi.NewClient(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("speech: create client: %w", err)
	}

	return &GoogleRecognizer{client: client}, nil
}

// NewStream opens one StreamingRecognize call and sends its required first
// message (the StreamingRecognitionConfig). InterimResults is always false
// and SingleUtterance always false: architecture.md requires only final
// results ("中間(interim)結果は永続化しない") and continuous multi-utterance
// recognition for the life of the stream, not single-utterance auto-stop.
func (r *GoogleRecognizer) NewStream(ctx context.Context, sampleRateHertz int32, languageCode string) (*Stream, error) {
	streamCtx, cancel := context.WithCancel(ctx)

	raw, err := r.client.StreamingRecognize(streamCtx)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("speech: start streaming recognize: %w", err)
	}

	config := &speechpb.StreamingRecognizeRequest{
		StreamingRequest: &speechpb.StreamingRecognizeRequest_StreamingConfig{
			StreamingConfig: &speechpb.StreamingRecognitionConfig{
				Config: &speechpb.RecognitionConfig{
					Encoding:        speechpb.RecognitionConfig_LINEAR16,
					SampleRateHertz: sampleRateHertz,
					LanguageCode:    languageCode,
				},
				InterimResults:  false,
				SingleUtterance: false,
			},
		},
	}
	if err := raw.Send(config); err != nil {
		cancel()
		return nil, fmt.Errorf("speech: send streaming config: %w", err)
	}

	return newStream(streamCtx, raw, cancel), nil
}
