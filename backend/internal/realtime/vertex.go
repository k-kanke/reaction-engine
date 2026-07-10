package realtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"strings"

	"golang.org/x/oauth2/google"

	"github.com/k-kanke/reaction-engine/backend/internal/contract"
)

const vertexModelVersionPrefix = "vertex:"

type VertexFeedbackGenerator struct {
	ProjectID string
	Location  string
	Model     string
	Client    *http.Client
}

func NewVertexFeedbackGenerator(ctx context.Context, projectID, location, model string) (*VertexFeedbackGenerator, error) {
	if projectID == "" {
		return nil, fmt.Errorf("vertex: project id is required")
	}
	if location == "" {
		location = "asia-northeast1"
	}
	if model == "" {
		model = "gemini-2.5-flash"
	}

	client, err := google.DefaultClient(ctx, "https://www.googleapis.com/auth/cloud-platform")
	if err != nil {
		return nil, fmt.Errorf("vertex: create authenticated client: %w", err)
	}

	return &VertexFeedbackGenerator{
		ProjectID: projectID,
		Location:  location,
		Model:     model,
		Client:    client,
	}, nil
}

func (g *VertexFeedbackGenerator) GenerateFeedback(ctx context.Context, sessionID string, tMs int64, trigger contract.TriggerInfo, pack EvidencePack) (contract.FeedbackEvent, error) {
	if g == nil {
		return contract.FeedbackEvent{}, fmt.Errorf("vertex: nil generator")
	}
	client := g.Client
	if client == nil {
		client = http.DefaultClient
	}

	reqBody, err := g.buildGenerateContentRequest(pack)
	if err != nil {
		return contract.FeedbackEvent{}, err
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return contract.FeedbackEvent{}, fmt.Errorf("vertex: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.endpoint(), bytes.NewReader(body))
	if err != nil {
		return contract.FeedbackEvent{}, fmt.Errorf("vertex: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return contract.FeedbackEvent{}, fmt.Errorf("vertex: generate content: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return contract.FeedbackEvent{}, fmt.Errorf("vertex: read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return contract.FeedbackEvent{}, fmt.Errorf("vertex: non-2xx response %d: %s", resp.StatusCode, string(respBody))
	}

	text, err := extractVertexText(respBody)
	if err != nil {
		return contract.FeedbackEvent{}, err
	}
	if DebugLogEvidencePack {
		log.Printf("realtime: vertex raw response session=%s trigger_id=%s text=%q", sessionID, trigger.TriggerID, text)
	}

	var generated struct {
		FeedbackType  string   `json:"feedback_type"`
		Severity      string   `json:"severity"`
		Message       string   `json:"message"`
		ReasonCodes   []string `json:"reason_codes"`
		EvidenceQuote *string  `json:"evidence_quote"`
		Confidence    float64  `json:"confidence"`
	}
	if err := json.Unmarshal([]byte(stripJSONFence(text)), &generated); err != nil {
		return contract.FeedbackEvent{}, fmt.Errorf("vertex: parse feedback json: %w: %q", err, text)
	}
	if generated.FeedbackType == "" || generated.Message == "" {
		return contract.FeedbackEvent{}, fmt.Errorf("vertex: incomplete feedback json: %q", text)
	}
	if generated.Severity == "" {
		generated.Severity = "info"
	}
	if generated.Confidence == 0 {
		generated.Confidence = llmStubConfidence
	}

	return contract.FeedbackEvent{
		Type:          "feedback_event",
		SessionID:     sessionID,
		TMs:           tMs,
		TriggerID:     trigger.TriggerID,
		FeedbackType:  generated.FeedbackType,
		Severity:      generated.Severity,
		Message:       generated.Message,
		ReasonCodes:   generated.ReasonCodes,
		EvidenceQuote: generated.EvidenceQuote,
		Source:        "llm",
		ModelVersion:  vertexModelVersionPrefix + g.Model,
		Confidence:    generated.Confidence,
		CooldownMs:    feedbackCooldownMs,
	}, nil
}

func (g *VertexFeedbackGenerator) endpoint() string {
	modelPath := g.Model
	if !strings.Contains(modelPath, "/") {
		modelPath = "publishers/google/models/" + modelPath
	}
	return fmt.Sprintf(
		"https://%s-aiplatform.googleapis.com/v1/projects/%s/locations/%s/%s:generateContent",
		g.Location,
		g.ProjectID,
		g.Location,
		modelPath,
	)
}

func (g *VertexFeedbackGenerator) buildGenerateContentRequest(pack EvidencePack) (map[string]any, error) {
	packJSON, err := json.MarshalIndent(pack, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("vertex: marshal evidence pack: %w", err)
	}

	systemPrompt := "You are Reaction Engine's realtime presentation feedback model. Return only compact JSON with keys feedback_type, severity, message, reason_codes, evidence_quote, confidence. Do not overstate causality; phrase reactions as possibilities. Ground the message in transcript_window when it is non-empty: name the specific topic/phrase being discussed around the reaction change, and suggest one concrete next action (e.g. revisit that point, ask a question, slow down). If transcript_window is empty, describe only the mood trend generically. Use Japanese for message, written as natural spoken advice to the presenter (2 sentences max)."

	if len(pack.RecentFeedback) > 0 {
		systemPrompt += " IMPORTANT: recent_feedback contains your previous advice for this session. Do NOT repeat the same message or advice. Build on prior feedback — offer a new angle, acknowledge improvement, or escalate if the issue persists."
	}

	if pack.Purpose == "periodic_feedback" {
		systemPrompt += " This is a periodic check (not triggered by a specific reaction change). Summarize the overall trend and give general advice. If everything looks stable, say so briefly with an encouraging tone."
	}

	parts := []map[string]any{
		{
			"text": systemPrompt,
		},
		{
			"text": "Realtime evidence pack:\n" + string(packJSON),
		},
	}

	for _, ref := range pack.BaselineFrames {
		if part := filePart(ref.MediaRef); part != nil {
			parts = append(parts, map[string]any{"text": "Baseline frame reference:"})
			parts = append(parts, part)
		}
	}
	for _, ref := range pack.EvidenceFrames {
		if part := filePart(ref.MediaRef); part != nil {
			parts = append(parts, map[string]any{"text": fmt.Sprintf("Trigger evidence frame at t_ms=%d:", ref.TMs)})
			parts = append(parts, part)
		}
	}

	return map[string]any{
		"contents": []map[string]any{
			{
				"role":  "user",
				"parts": parts,
			},
		},
		"generationConfig": map[string]any{
			"temperature":      0.2,
			"maxOutputTokens":  512,
			"responseMimeType": "application/json",
			// gemini-2.5-flash defaults to spending its output budget on
			// internal "thinking" tokens first -- with the old
			// maxOutputTokens=256 that consumed the whole budget (measured
			// thoughtsTokenCount=253/256) and left zero tokens for the
			// actual JSON answer, so every real call hit finishReason
			// MAX_TOKENS with no text (extractVertexText's "response
			// contained no text" error, always falling back to
			// ruleFallback). This path has a 1.5s budget
			// (realtimeLLMTimeout) anyway, so thinking adds latency risk
			// for no benefit here.
			"thinkingConfig": map[string]any{"thinkingBudget": 0},
		},
	}, nil
}

func filePart(mediaRef string) map[string]any {
	if !strings.HasPrefix(mediaRef, "gs://") {
		return nil
	}
	return map[string]any{
		"fileData": map[string]any{
			"mimeType": guessImageMIME(mediaRef),
			"fileUri":  mediaRef,
		},
	}
}

func guessImageMIME(uri string) string {
	switch strings.ToLower(filepath.Ext(uri)) {
	case ".webp":
		return "image/webp"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	default:
		return "image/jpeg"
	}
}

func extractVertexText(body []byte) (string, error) {
	var decoded struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		return "", fmt.Errorf("vertex: decode response: %w", err)
	}
	for _, c := range decoded.Candidates {
		for _, p := range c.Content.Parts {
			if strings.TrimSpace(p.Text) != "" {
				return p.Text, nil
			}
		}
	}
	return "", fmt.Errorf("vertex: response contained no text")
}

func stripJSONFence(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}
