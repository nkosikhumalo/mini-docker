// API key management — secure BYOK (Bring Your Own Key) for AI providers.

package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"foxvue-api/db"
)

type apiKeyHandler struct {
	keys *db.APIKeyRepo
}

func newAPIKeyHandler(keys *db.APIKeyRepo) *apiKeyHandler {
	return &apiKeyHandler{keys: keys}
}

// POST /api/apikeys — save a new key (encrypted) and auto-activate it
func (h *apiKeyHandler) save(c *gin.Context) {
	var body struct {
		Provider string `json:"provider" binding:"required,oneof=gemini openai anthropic aws"`
		APIKey   string `json:"apiKey"   binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userID, _ := c.Get("userID")
	uid := userID.(string)

	row, err := h.keys.Upsert(uid, body.Provider, body.APIKey)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save key."})
		return
	}

	// Auto-activate the key immediately after saving
	_ = h.keys.SetActive(uid, row.ID)
	row.IsActive = true

	c.JSON(http.StatusOK, row)
}

// GET /api/apikeys — list all keys (masked)
func (h *apiKeyHandler) list(c *gin.Context) {
	userID, _ := c.Get("userID")
	uid := userID.(string)

	rows, err := h.keys.List(uid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load keys."})
		return
	}
	c.JSON(http.StatusOK, gin.H{"keys": rows})
}

// POST /api/apikeys/:id/test — test if a key works
func (h *apiKeyHandler) test(c *gin.Context) {
	keyID := c.Param("id")
	userID, _ := c.Get("userID")
	uid := userID.(string)

	provider, plainKey, err := h.keys.GetDecryptedByID(uid, keyID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Key not found."})
		return
	}

	// Test the key with a lightweight request
	valid, errMsg := testProviderKey(provider, plainKey)
	status := "valid"
	if !valid {
		status = "invalid"
	}

	_ = h.keys.SetStatus(keyID, status)

	if !valid {
		c.JSON(http.StatusOK, gin.H{"valid": false, "error": errMsg})
		return
	}

	c.JSON(http.StatusOK, gin.H{"valid": true})
}

// POST /api/apikeys/:id/activate — set this key as active
func (h *apiKeyHandler) activate(c *gin.Context) {
	keyID := c.Param("id")
	userID, _ := c.Get("userID")
	uid := userID.(string)

	if err := h.keys.SetActive(uid, keyID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to activate key."})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Key activated."})
}

// DELETE /api/apikeys/:id — delete a key
func (h *apiKeyHandler) delete(c *gin.Context) {
	keyID := c.Param("id")
	userID, _ := c.Get("userID")
	uid := userID.(string)

	if err := h.keys.Delete(uid, keyID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete key."})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Key deleted."})
}

// ── Provider test helpers ─────────────────────────────────────────────────────

func testProviderKey(provider, key string) (bool, string) {
	switch provider {
	case "gemini":
		return testGemini(key)
	case "openai":
		return testOpenAI(key)
	case "anthropic":
		return testAnthropic(key)
	case "aws":
		return testAWS(key)
	default:
		return false, "Unknown provider"
	}
}

func testGemini(key string) (bool, string) {
	url := "https://generativelanguage.googleapis.com/v1beta/models/gemini-flash-latest:generateContent"
	body := map[string]any{
		"contents": []map[string]any{
			{"parts": []map[string]any{{"text": "Say hello in one word"}}},
		},
	}
	payload, _ := json.Marshal(body)

	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-goog-api-key", key)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, "Network error"
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusOK {
		return true, ""
	}

	respBody, _ := io.ReadAll(resp.Body)
	return false, fmt.Sprintf("Status %d: %s", resp.StatusCode, truncate(string(respBody), 80))
}

func testOpenAI(key string) (bool, string) {
	url := "https://api.openai.com/v1/models"
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+key)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, "Network error"
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusOK {
		return true, ""
	}

	respBody, _ := io.ReadAll(resp.Body)
	return false, fmt.Sprintf("Status %d: %s", resp.StatusCode, truncate(string(respBody), 80))
}

func testAnthropic(key string) (bool, string) {
	// Anthropic doesn't have a simple test endpoint — we'd need to send a real message.
	// For now, just validate format.
	if !strings.HasPrefix(key, "sk-ant-") {
		return false, "Invalid Anthropic key format (should start with sk-ant-)"
	}
	return true, ""
}

func testAWS(key string) (bool, string) {
	// AWS uses access key + secret key — more complex.
	// For now, just validate format.
	if len(key) < 20 {
		return false, "Invalid AWS key format"
	}
	return true, ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
