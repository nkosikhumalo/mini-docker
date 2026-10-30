package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	"foxvue-api/db"
	"foxvue-api/email"
)

type resetHandler struct {
	users *db.UserRepo
}

func newResetHandler(users *db.UserRepo) *resetHandler {
	return &resetHandler{users: users}
}

// POST /api/auth/forgot-password
func (h *resetHandler) forgotPassword(c *gin.Context) {
	var body struct {
		Email string `json:"email" binding:"required,email"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Valid email required."})
		return
	}

	body.Email = strings.ToLower(strings.TrimSpace(body.Email))
	log.Printf("[reset] forgot-password request for: %s", body.Email)

	const successMsg = "If that email is registered, a reset link has been sent."

	user, err := h.users.GetByEmail(body.Email)
	if err != nil {
		log.Printf("[reset] email not found in DB: %s", body.Email)
		c.JSON(http.StatusOK, gin.H{"message": successMsg})
		return
	}

	rawToken, err := generateSecureToken(32)
	if err != nil {
		log.Printf("[reset] token generation failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate token."})
		return
	}

	hashedToken := hashToken(rawToken)
	expiresAt := time.Now().Add(30 * time.Minute)

	if err := h.users.SetResetToken(user.ID, hashedToken, expiresAt); err != nil {
		log.Printf("[reset] DB SetResetToken failed for %s: %v", user.ID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save reset token."})
		return
	}
	log.Printf("[reset] token stored in DB for user %s, expires %s", user.ID, expiresAt.Format(time.RFC3339))

	frontendURL := os.Getenv("FRONTEND_URL")
	if frontendURL == "" {
		frontendURL = "http://localhost:5173"
	}
	resetURL := frontendURL + "/reset-password?token=" + rawToken
	log.Printf("[reset] reset URL generated (not logged for security)")

	// Send email — NOT in a goroutine so we can log the real error
	if err := email.SendPasswordReset(user.Email, user.Name, resetURL); err != nil {
		log.Printf("[reset] SMTP send FAILED for %s: %v", user.Email, err)
		// Still return success to avoid enumeration, but log the real error
		c.JSON(http.StatusOK, gin.H{"message": successMsg})
		return
	}

	log.Printf("[reset] email sent successfully to %s", user.Email)
	c.JSON(http.StatusOK, gin.H{"message": successMsg})
}

// POST /api/auth/reset-password
func (h *resetHandler) resetPassword(c *gin.Context) {
	var body struct {
		Token    string `json:"token"    binding:"required"`
		Password string `json:"password" binding:"required,min=8"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := validatePassword(body.Password); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	hashedToken := hashToken(body.Token)
	log.Printf("[reset] reset-password attempt with token hash: %s...", hashedToken[:8])

	user, err := h.users.GetByResetToken(hashedToken)
	if err != nil {
		log.Printf("[reset] token not found or expired: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Reset link is invalid or has expired."})
		return
	}

	newHash, err := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to hash password."})
		return
	}

	if err := h.users.UpdatePassword(user.ID, string(newHash)); err != nil {
		log.Printf("[reset] UpdatePassword failed for %s: %v", user.ID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update password."})
		return
	}

	_ = h.users.ClearResetToken(user.ID)
	log.Printf("[reset] password reset successful for user %s", user.ID)

	c.JSON(http.StatusOK, gin.H{"message": "Password updated successfully. You can now sign in."})
}

func generateSecureToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}
