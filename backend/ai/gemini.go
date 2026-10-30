// GeminiProvider implements the Provider interface using Google Gemini.

package ai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"foxvue-api/models"
)

const geminiEndpoint = "https://generativelanguage.googleapis.com/v1beta/models/gemini-flash-latest:generateContent"

// retryable HTTP status codes — move to next key on these
var retryOn = map[int]bool{
	429: true,
	500: true,
	503: true,
	401: true,
	403: true,
}

// GeminiProvider satisfies the Provider interface.
type GeminiProvider struct {
	keys []string
}

// NewGeminiProvider creates a provider with the given API keys.
// Keys are tried in order; the first successful response wins.
func NewGeminiProvider(keys []string) Provider {
	return &GeminiProvider{keys: keys}
}

// GenerateQuestions implements Provider.
func (p *GeminiProvider) GenerateQuestions(jobTitle, jobDescription string) ([]models.Question, error) {
	desc := jobDescription
	if len(desc) > 800 {
		desc = desc[:800]
	}

	prompt := fmt.Sprintf(
		"Job: %s\nDescription: %s\n\nGenerate 5 interview questions (mix behavioral + technical, tailored to this role).\n\nJSON only:\n{\"questions\":[{\"text\":\"Q1\",\"category\":\"Behavioral\",\"skill\":\"Communication\"},{\"text\":\"Q2\",\"category\":\"Technical\",\"skill\":\"System Design\"},{\"text\":\"Q3\",\"category\":\"Technical\",\"skill\":\"Problem Solving\"},{\"text\":\"Q4\",\"category\":\"Behavioral\",\"skill\":\"Leadership\"},{\"text\":\"Q5\",\"category\":\"Technical\",\"skill\":\"Coding\"}]}",
		jobTitle, desc,
	)

	raw, err := p.call(prompt)
	if err != nil {
		return nil, err
	}
	raw = extractJSONObject(raw)

	var result struct {
		Questions []struct {
			Text     string `json:"text"`
			Category string `json:"category"`
			Skill    string `json:"skill"`
		} `json:"questions"`
	}
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return nil, fmt.Errorf("parse questions: %w", err)
	}

	questions := make([]models.Question, len(result.Questions))
	if len(result.Questions) == 0 {
		return nil, fmt.Errorf("question response was empty")
	}
	for i, q := range result.Questions {
		questions[i] = models.Question{ID: i + 1, Text: q.Text, Category: q.Category, Skill: q.Skill}
	}
	return questions, nil
}

// EvaluateAnswer implements Provider.
func (p *GeminiProvider) EvaluateAnswer(question models.Question, transcript string) (*models.EvaluationResult, error) {
	prompt := fmt.Sprintf(
		"Question: %s (Skill: %s)\nAnswer: %s\n\nScore 1-10 on clarity, technical accuracy, communication. Give strengths, weaknesses, sample answer, follow-up, summary.\n\nJSON only:\n{\"score\":7,\"clarity\":8,\"technicalScore\":6,\"communication\":7,\"strengths\":[\"s1\",\"s2\"],\"weaknesses\":[\"w1\",\"w2\"],\"sampleAnswer\":\"...\",\"followUp\":\"...\",\"summary\":\"...\"}",
		question.Text, question.Skill, transcript,
	)

	raw, err := p.call(prompt)
	if err != nil {
		return nil, err
	}

	var result models.EvaluationResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return nil, fmt.Errorf("parse evaluation: %w — raw: %s", err, raw)
	}

	result.FillerWords = countFillerWords(transcript)
	result.Star = starRating(result.Score)
	return &result, nil
}

// call sends a prompt to Gemini, rotating through keys on retryable errors.
func (p *GeminiProvider) call(prompt string) (string, error) {
	if len(p.keys) == 0 {
		return "", fmt.Errorf("no Gemini API keys configured — set GEMINI_API_KEYS in .env")
	}

	body := map[string]any{
		"contents": []map[string]any{
			{"parts": []map[string]any{{"text": prompt}}},
		},
		"generationConfig": map[string]any{
			"temperature":      0.3,
			"maxOutputTokens":  1500,
			"responseMimeType": "application/json",
		},
	}
	payload, _ := json.Marshal(body)

	var lastErr error
	for i, key := range p.keys {
		for attempt := 0; attempt < 2; attempt++ {
			if attempt > 0 {
				time.Sleep(500 * time.Millisecond)
			}

			req, err := http.NewRequest(http.MethodPost, geminiEndpoint, bytes.NewReader(payload))
			if err != nil {
				lastErr = err
				break
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-goog-api-key", key)

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				lastErr = fmt.Errorf("key %d: %w", i+1, err)
				break
			}

			respBody, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()

			if resp.StatusCode == http.StatusOK {
				return parseGeminiText(respBody)
			}

			lastErr = fmt.Errorf("key %d: status %d: %s", i+1, resp.StatusCode, truncate(string(respBody), 120))

			if !retryOn[resp.StatusCode] {
				return "", lastErr
			}
			if resp.StatusCode != http.StatusServiceUnavailable {
				break // try next key immediately
			}
		}
	}

	return "", fmt.Errorf("all %d key(s) failed: %w", len(p.keys), lastErr)
}

func parseGeminiText(body []byte) (string, error) {
	var r struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return "", fmt.Errorf("parse gemini response: %w", err)
	}
	if len(r.Candidates) == 0 || len(r.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("gemini returned empty response")
	}
	text := strings.TrimSpace(r.Candidates[0].Content.Parts[0].Text)
	text = strings.TrimPrefix(text, "```json")
	text = strings.TrimPrefix(text, "```")
	text = strings.TrimSuffix(text, "```")
	return strings.TrimSpace(text), nil
}

// extractJSONObject handles Markdown fences and incidental model prose.
func extractJSONObject(text string) string {
	text = strings.TrimSpace(text)
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start >= 0 && end > start {
		return text[start : end+1]
	}
	return text
}

func truncate(s string, n int) string {

	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

var fillerList = []string{"um", "uh", "like", "you know", "actually", "basically"}

func countFillerWords(transcript string) map[string]int {
	t := strings.ToLower(transcript)
	counts := map[string]int{}
	for _, fw := range fillerList {
		counts[fw] = strings.Count(t, fw)
	}
	return counts
}

func starRating(score int) string {
	switch {
	case score >= 9:
		return "Strong"
	case score >= 7:
		return "Good"
	case score >= 5:
		return "Fair"
	default:
		return "Needs work"
	}
}
