// Transcription handler — receives audio from the browser and returns text.
// Uses Gemini's multimodal (audio) capability so it works server-side
// without any device-specific browser APIs.

package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"foxvue-api/db"
)

const transcribeURL = "https://generativelanguage.googleapis.com/v1beta/models/gemini-flash-latest:generateContent"

// transcribeHandler holds the apikey repo so it can resolve user keys.
type transcribeHandler struct {
	apiKeys *db.APIKeyRepo
}

func newTranscribeHandler(apiKeys *db.APIKeyRepo) *transcribeHandler {
	return &transcribeHandler{apiKeys: apiKeys}
}

// resolveGeminiKeys returns the user's active Gemini key (if any) prepended
// to the platform fallback keys.
func (h *transcribeHandler) resolveKeys(c *gin.Context) []string {
	platformKeys := getGeminiKeys()
	userID, _ := c.Get("userID")
	uid, _ := userID.(string)
	if uid == "" {
		return platformKeys
	}
	userKey, err := h.apiKeys.GetDecrypted(uid, "gemini")
	if err != nil || userKey == "" {
		return platformKeys
	}
	return append([]string{userKey}, platformKeys...)
}

// POST /api/transcribe
func (h *transcribeHandler) transcribeAudio(c *gin.Context) {
	file, header, err := c.Request.FormFile("audio")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing audio file"})
		return
	}
	defer func() { _ = file.Close() }()

	if header.Size > 25*1024*1024 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "audio file too large (max 25 MB)"})
		return
	}

	audioBytes, err := io.ReadAll(file)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read audio"})
		return
	}

	mimeType := header.Header.Get("Content-Type")
	if mimeType == "" || mimeType == "application/octet-stream" {
		mimeType = guessMimeType(header.Filename)
	}

	// User's active Gemini key first, then platform fallback
	keys := h.resolveKeys(c)

	transcript, err := transcribeWithKeys(audioBytes, mimeType, keys)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "transcription failed: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"transcript": transcript})
}

func transcribeWithKeys(audioBytes []byte, mimeType string, keys []string) (string, error) {
	if len(keys) == 0 {
		return "", fmt.Errorf("no Gemini API keys configured")
	}

	audioB64 := base64.StdEncoding.EncodeToString(audioBytes)

	body := map[string]any{
		"contents": []map[string]any{
			{
				"parts": []map[string]any{
					{
						"inline_data": map[string]any{
							"mime_type": mimeType,
							"data":      audioB64,
						},
					},
					{
						"text": "Transcribe this audio recording exactly as spoken. Return only the spoken words, no timestamps, no labels, no extra commentary.",
					},
				},
			},
		},
		"generationConfig": map[string]any{
			"temperature":     0.0,
			"maxOutputTokens": 2048,
		},
	}

	payload, _ := json.Marshal(body)

	var lastErr error
	for _, key := range keys {
		for attempt := 0; attempt < 2; attempt++ {
			if attempt > 0 {
				time.Sleep(time.Second)
			}

			req, err := http.NewRequest(http.MethodPost, transcribeURL, bytes.NewReader(payload))
			if err != nil {
				lastErr = err
				break
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-goog-api-key", key)

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				lastErr = err
				break
			}

			respBody, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()

			if resp.StatusCode == http.StatusServiceUnavailable {
				lastErr = fmt.Errorf("gemini overloaded")
				continue
			}

			if resp.StatusCode != http.StatusOK {
				lastErr = fmt.Errorf("gemini %d: %s", resp.StatusCode, truncateStr(string(respBody), 120))
				break
			}

			var geminiResp struct {
				Candidates []struct {
					Content struct {
						Parts []struct {
							Text string `json:"text"`
						} `json:"parts"`
					} `json:"content"`
				} `json:"candidates"`
			}
			if err := json.Unmarshal(respBody, &geminiResp); err != nil {
				lastErr = fmt.Errorf("parse error: %w", err)
				break
			}
			if len(geminiResp.Candidates) == 0 || len(geminiResp.Candidates[0].Content.Parts) == 0 {
				lastErr = fmt.Errorf("empty response")
				break
			}

			return strings.TrimSpace(geminiResp.Candidates[0].Content.Parts[0].Text), nil
		}
	}

	return "", fmt.Errorf("all keys failed: %w", lastErr)
}

func getGeminiKeys() []string {
	raw := os.Getenv("GEMINI_API_KEYS")
	if raw == "" {
		raw = os.Getenv("GEMINI_API_KEY")
	}
	var keys []string
	for _, k := range strings.Split(raw, ",") {
		k = strings.TrimSpace(k)
		if k != "" {
			keys = append(keys, k)
		}
	}
	return keys
}

func guessMimeType(filename string) string {
	lower := strings.ToLower(filename)
	switch {
	case strings.HasSuffix(lower, ".webm"):
		return "audio/webm"
	case strings.HasSuffix(lower, ".ogg"):
		return "audio/ogg"
	case strings.HasSuffix(lower, ".mp4"), strings.HasSuffix(lower, ".m4a"):
		return "audio/mp4"
	case strings.HasSuffix(lower, ".wav"):
		return "audio/wav"
	case strings.HasSuffix(lower, ".mp3"):
		return "audio/mp3"
	default:
		return "audio/webm"
	}
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
