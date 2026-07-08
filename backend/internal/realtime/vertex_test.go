package realtime

import (
	"strings"
	"testing"
)

func TestVertexEndpoint(t *testing.T) {
	g := &VertexFeedbackGenerator{ProjectID: "proj_1", Location: "asia-northeast1", Model: "gemini-2.5-flash"}
	got := g.endpoint()
	want := "https://asia-northeast1-aiplatform.googleapis.com/v1/projects/proj_1/locations/asia-northeast1/publishers/google/models/gemini-2.5-flash:generateContent"
	if got != want {
		t.Errorf("endpoint = %q, want %q", got, want)
	}
}

func TestBuildGenerateContentRequestIncludesGCSImageRefs(t *testing.T) {
	g := &VertexFeedbackGenerator{}
	req, err := g.buildGenerateContentRequest(EvidencePack{
		BaselineFrames: []BaselineFrameRef{{MediaRef: "gs://bucket/baseline/frame.webp"}},
		EvidenceFrames: []EvidenceFrameRefOut{{MediaRef: "gs://bucket/evidence/frame.jpg", TMs: 123}},
	})
	if err != nil {
		t.Fatalf("buildGenerateContentRequest returned error: %v", err)
	}

	contents := req["contents"].([]map[string]any)
	parts := contents[0]["parts"].([]map[string]any)
	var fileURIs []string
	for _, part := range parts {
		fileData, ok := part["fileData"].(map[string]any)
		if ok {
			fileURIs = append(fileURIs, fileData["fileUri"].(string))
		}
	}

	joined := strings.Join(fileURIs, ",")
	if !strings.Contains(joined, "gs://bucket/baseline/frame.webp") {
		t.Errorf("file URIs = %q, want baseline ref", joined)
	}
	if !strings.Contains(joined, "gs://bucket/evidence/frame.jpg") {
		t.Errorf("file URIs = %q, want evidence ref", joined)
	}
}

func TestExtractVertexText(t *testing.T) {
	body := []byte(`{"candidates":[{"content":{"parts":[{"text":"{\"message\":\"ok\"}"}]}}]}`)
	got, err := extractVertexText(body)
	if err != nil {
		t.Fatalf("extractVertexText returned error: %v", err)
	}
	if got != `{"message":"ok"}` {
		t.Errorf("text = %q, want JSON text", got)
	}
}

func TestStripJSONFence(t *testing.T) {
	got := stripJSONFence("```json\n{\"message\":\"ok\"}\n```")
	if got != `{"message":"ok"}` {
		t.Errorf("stripJSONFence = %q", got)
	}
}
