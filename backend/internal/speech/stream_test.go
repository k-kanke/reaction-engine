package speech

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"cloud.google.com/go/speech/apiv1/speechpb"
)

// fakeGrpcStream is a minimal grpcStream double: Send records what was
// sent, Recv replays a scripted list of responses (or an error) without any
// real network/gRPC involved.
type fakeGrpcStream struct {
	mu        sync.Mutex
	sent      []*speechpb.StreamingRecognizeRequest
	responses []*speechpb.StreamingRecognizeResponse
	recvErr   error
}

func (f *fakeGrpcStream) Send(req *speechpb.StreamingRecognizeRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, req)
	return nil
}

func (f *fakeGrpcStream) Recv() (*speechpb.StreamingRecognizeResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.responses) == 0 {
		if f.recvErr != nil {
			return nil, f.recvErr
		}
		return nil, io.EOF
	}
	resp := f.responses[0]
	f.responses = f.responses[1:]
	return resp, nil
}

func finalResponse(text string, confidence float32) *speechpb.StreamingRecognizeResponse {
	return &speechpb.StreamingRecognizeResponse{
		Results: []*speechpb.StreamingRecognitionResult{
			{
				IsFinal: true,
				Alternatives: []*speechpb.SpeechRecognitionAlternative{
					{Transcript: text, Confidence: confidence},
				},
			},
		},
	}
}

func interimResponse(text string) *speechpb.StreamingRecognizeResponse {
	return &speechpb.StreamingRecognizeResponse{
		Results: []*speechpb.StreamingRecognitionResult{
			{
				IsFinal: false,
				Alternatives: []*speechpb.SpeechRecognitionAlternative{
					{Transcript: text, Confidence: 0.1},
				},
			},
		},
	}
}

func TestStreamForwardsOnlyFinalResults(t *testing.T) {
	fake := &fakeGrpcStream{
		responses: []*speechpb.StreamingRecognizeResponse{
			interimResponse("konni"),
			finalResponse("konnichiwa", 0.92),
			finalResponse("sayonara", 0.81),
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := newStream(ctx, fake, cancel)

	var got []Result
	timeout := time.After(2 * time.Second)
	for len(got) < 2 {
		select {
		case r, ok := <-s.Results():
			if !ok {
				t.Fatalf("results channel closed early, got %d results", len(got))
			}
			got = append(got, r)
		case <-timeout:
			t.Fatalf("timed out waiting for results, got %d so far", len(got))
		}
	}

	if got[0].Text != "konnichiwa" || got[0].Confidence != float64(float32(0.92)) {
		t.Errorf("got[0] = %+v, want text=konnichiwa confidence=0.92", got[0])
	}
	if got[1].Text != "sayonara" {
		t.Errorf("got[1].Text = %q, want sayonara", got[1].Text)
	}
}

func TestStreamSendWiresAudioContent(t *testing.T) {
	fake := &fakeGrpcStream{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := newStream(ctx, fake, cancel)
	if err := s.Send([]byte{1, 2, 3}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.sent) != 1 {
		t.Fatalf("sent %d requests, want 1", len(fake.sent))
	}
	audio := fake.sent[0].GetAudioContent()
	if string(audio) != string([]byte{1, 2, 3}) {
		t.Errorf("sent AudioContent = %v, want [1 2 3]", audio)
	}
}

func TestStreamCloseEndsResults(t *testing.T) {
	fake := &fakeGrpcStream{recvErr: errors.New("blocked forever until ctx cancel")}
	ctx, cancel := context.WithCancel(context.Background())

	s := newStream(ctx, fake, cancel)
	s.Close()

	select {
	case _, ok := <-s.Results():
		if ok {
			t.Fatalf("expected results channel to be closed with no values")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for results channel to close after Close()")
	}
}
