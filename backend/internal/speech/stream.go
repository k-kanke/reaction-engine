package speech

import (
	"context"
	"fmt"

	"cloud.google.com/go/speech/apiv1/speechpb"
)

// grpcStream is the narrow slice of speechpb.Speech_StreamingRecognizeClient
// that Stream depends on, so tests can substitute a fake instead of a real
// gRPC connection.
type grpcStream interface {
	Send(*speechpb.StreamingRecognizeRequest) error
	Recv() (*speechpb.StreamingRecognizeResponse, error)
}

// Stream is one bounded StreamingRecognize session. Callers feed PCM via
// Send and read final-only results off Results(); Close tears the
// underlying gRPC call down (whether because the caller is done with it or
// because it's approaching Google's per-stream duration limit).
type Stream struct {
	raw     grpcStream
	cancel  context.CancelFunc
	results chan Result
	// err is set by recvLoop before it closes results, if the stream ended
	// because of an error rather than a deliberate Close(). Only meaningful
	// to read after Results() has been observed closed (recvLoop's own
	// goroutine is the sole writer, and the channel close is itself the
	// synchronization point).
	err error
}

func newStream(ctx context.Context, raw grpcStream, cancel context.CancelFunc) *Stream {
	s := &Stream{
		raw:     raw,
		cancel:  cancel,
		results: make(chan Result, 8),
	}
	go s.recvLoop(ctx)
	return s
}

// Send forwards one PCM chunk (LINEAR16, matching the config NewStream
// sent) to the recognizer.
func (s *Stream) Send(pcm []byte) error {
	return s.raw.Send(&speechpb.StreamingRecognizeRequest{
		StreamingRequest: &speechpb.StreamingRecognizeRequest_AudioContent{
			AudioContent: pcm,
		},
	})
}

// Results is closed once the underlying stream ends (error, EOF, or Close).
func (s *Stream) Results() <-chan Result {
	return s.results
}

// Err returns why the stream ended, or nil if it hasn't ended, ended
// cleanly (EOF), or ended because the caller called Close(). Only
// meaningful to call after Results() has been drained/closed.
func (s *Stream) Err() error {
	return s.err
}

// Close ends the stream immediately; recvLoop observes ctx.Done() and
// returns rather than waiting on a graceful server-side close, since a
// still-blocked Recv() wouldn't otherwise unblock in time.
func (s *Stream) Close() {
	s.cancel()
}

func (s *Stream) recvLoop(ctx context.Context) {
	defer close(s.results)

	for {
		resp, err := s.raw.Recv()
		if err != nil {
			// ctx.Err() != nil means this is the expected shutdown path
			// (the caller called Close()), not a real failure worth
			// surfacing.
			if ctx.Err() == nil {
				s.err = fmt.Errorf("speech: recv: %w", err)
			}
			return
		}
		if resp.Error != nil {
			s.err = fmt.Errorf("speech: streaming error (code=%d): %s", resp.Error.GetCode(), resp.Error.GetMessage())
			return
		}

		for _, result := range resp.Results {
			// architecture.md: only final results become transcript_chunk;
			// interim hypotheses are never persisted.
			if !result.IsFinal || len(result.Alternatives) == 0 {
				continue
			}
			alt := result.Alternatives[0]

			select {
			case s.results <- Result{Text: alt.Transcript, Confidence: float64(alt.Confidence)}:
			case <-ctx.Done():
				return
			}
		}
	}
}
