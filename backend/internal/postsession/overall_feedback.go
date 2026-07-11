package postsession

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"golang.org/x/oauth2/google"

	"github.com/k-kanke/reaction-engine/backend/internal/contract"
)

// OverallFeedbackGenerator produces the LLM-written replacement for
// WaveOverview.Overall (see cmd/post-session-job/main.go). It receives the
// already-assembled Report so BuildReport's deterministic summary
// (describeOverall) stays a zero-cost fallback: the caller only overwrites
// WaveOverview.Overall when this succeeds.
type OverallFeedbackGenerator interface {
	GenerateOverallFeedback(ctx context.Context, report Report) (string, error)
}

// VertexOverallFeedbackGenerator calls Vertex AI's Gemini generateContent
// endpoint directly, mirroring internal/realtime.VertexFeedbackGenerator's
// auth/HTTP pattern -- but simpler, since this returns free-form Markdown
// prose rather than a strict-JSON feedback_event, so there's no JSON
// schema enforcement or evidence-frame image parts to build.
type VertexOverallFeedbackGenerator struct {
	ProjectID string
	Location  string
	Model     string
	Client    *http.Client
}

func NewVertexOverallFeedbackGenerator(ctx context.Context, projectID, location, model string) (*VertexOverallFeedbackGenerator, error) {
	if projectID == "" {
		return nil, fmt.Errorf("postsession: project id is required")
	}
	if location == "" {
		location = "asia-northeast1"
	}
	if model == "" {
		model = "gemini-2.5-flash"
	}

	client, err := google.DefaultClient(ctx, "https://www.googleapis.com/auth/cloud-platform")
	if err != nil {
		return nil, fmt.Errorf("postsession: create authenticated client: %w", err)
	}

	return &VertexOverallFeedbackGenerator{
		ProjectID: projectID,
		Location:  location,
		Model:     model,
		Client:    client,
	}, nil
}

// overallFeedbackInput is the summarized subset of Report handed to the
// LLM -- never the raw mood_wave_sample/transcript series. Matches
// internal/realtime's evidence pack: pass already-summarized data, not
// everything BuildReport read from Cloud SQL/JSONL.
type overallFeedbackInput struct {
	DurationMin             float64                  `json:"duration_min"`
	MoodWaveSampleCount     int                      `json:"mood_wave_sample_count"`
	DropSections            []string                 `json:"drop_sections"`
	PeakPositiveSections    []string                 `json:"peak_positive_sections"`
	ImportantWindows        []ImportantWindow        `json:"important_windows"`
	RealtimeFeedbackHistory []contract.FeedbackEvent `json:"realtime_feedback_history"`
	ParticipantCount        int                      `json:"participant_count"`
}

func (g *VertexOverallFeedbackGenerator) GenerateOverallFeedback(ctx context.Context, report Report) (string, error) {
	if g == nil {
		return "", fmt.Errorf("postsession: nil generator")
	}
	client := g.Client
	if client == nil {
		client = http.DefaultClient
	}

	input := overallFeedbackInput{
		DurationMin:             report.Session.DurationMin,
		MoodWaveSampleCount:     report.MoodWaveSampleCount,
		DropSections:            report.WaveOverview.DropSections,
		PeakPositiveSections:    report.WaveOverview.PeakPositiveSections,
		ImportantWindows:        report.ImportantWindows,
		RealtimeFeedbackHistory: report.RealtimeFeedbackHistory,
		ParticipantCount:        len(report.BaselineContext.Participants),
	}

	reqBody, err := g.buildGenerateContentRequest(input)
	if err != nil {
		return "", err
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("postsession: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.endpoint(), bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("postsession: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("postsession: generate content: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return "", fmt.Errorf("postsession: read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("postsession: non-2xx response %d: %s", resp.StatusCode, string(respBody))
	}

	text, err := extractOverallFeedbackText(respBody)
	if err != nil {
		return "", err
	}

	markdown := stripMarkdownFence(text)
	if markdown == "" {
		return "", fmt.Errorf("postsession: empty overall feedback text")
	}
	return markdown, nil
}

func (g *VertexOverallFeedbackGenerator) endpoint() string {
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

func (g *VertexOverallFeedbackGenerator) buildGenerateContentRequest(input overallFeedbackInput) (map[string]any, error) {
	inputJSON, err := json.MarshalIndent(input, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("postsession: marshal overall feedback input: %w", err)
	}

	systemPrompt := "You are Reaction Engine's post-session report writer. Given this session's summarized data " +
		"(mood wave trend, important_windows around each notable reaction with transcript excerpts, feedback " +
		"already shown to the presenter live during the session, participant count), write a holistic overall " +
		"feedback section for the presenter in Markdown. Start with a \"## 総評\" heading. Use short paragraphs " +
		"and/or bullet points covering: how the session went overall, what specifically worked well (cite " +
		"specific moments/transcript excerpts when available), what could improve, and 1-2 concrete suggestions " +
		"for next time. Ground every specific claim in the data given -- never invent details not present in it. " +
		"Write naturally in Japanese, as if advising the presenter directly. Return only the Markdown body: no " +
		"code fences, no JSON, no preamble."

	return map[string]any{
		"contents": []map[string]any{
			{
				"role": "user",
				"parts": []map[string]any{
					{"text": systemPrompt},
					{"text": "Session data:\n" + string(inputJSON)},
				},
			},
		},
		"generationConfig": map[string]any{
			"temperature":     0.4,
			"maxOutputTokens": 1536,
			// See internal/realtime/vertex.go's identical comment:
			// gemini-2.5-flash spends its output budget on internal
			// "thinking" tokens first, which previously consumed the
			// whole budget and left nothing for the actual answer.
			// Disabled here too -- this call has no need for it.
			"thinkingConfig": map[string]any{"thinkingBudget": 0},
		},
	}, nil
}

func extractOverallFeedbackText(body []byte) (string, error) {
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
		return "", fmt.Errorf("postsession: decode response: %w", err)
	}
	for _, c := range decoded.Candidates {
		for _, p := range c.Content.Parts {
			if strings.TrimSpace(p.Text) != "" {
				return p.Text, nil
			}
		}
	}
	return "", fmt.Errorf("postsession: response contained no text")
}

func stripMarkdownFence(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "```markdown")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}
