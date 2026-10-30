// Email/password auth: signup, email verification, and login.
//
// Flow:
//  1. POST /api/auth/signup               — name + email only, sends OTP
//  2. POST /api/auth/complete-registration — email + OTP + password, creates user
//  3. POST /api/auth/login                 — email + password, returns JWT

package api

import (
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"

	"foxvue-api/db"
	"foxvue-api/email"
)

// ── Rate limiter (5 requests / 10 min / IP) ───────────────────────────────────

type ipEntry struct {
	count     int
	windowEnd time.Time
}

var (
	signupLimiter   = map[string]*ipEntry{}
	signupLimiterMu sync.Mutex
)

func signupRateLimited(ip string) bool {
	signupLimiterMu.Lock()
	defer signupLimiterMu.Unlock()
	now := time.Now()
	e, ok := signupLimiter[ip]
	if !ok || now.After(e.windowEnd) {
		signupLimiter[ip] = &ipEntry{count: 1, windowEnd: now.Add(10 * time.Minute)}
		return false
	}
	e.count++
	return e.count > 5
}

// ── Handler ───────────────────────────────────────────────────────────────────

type authHandler struct {
	users  *db.UserRepo
	verifs *db.VerificationRepo
}

func newAuthHandler(users *db.UserRepo, verifs *db.VerificationRepo) *authHandler {
	return &authHandler{users: users, verifs: verifs}
}

// POST /api/auth/signup
// Accepts name + email only. Generates OTP, stores pending record, sends email.
func (h *authHandler) signup(c *gin.Context) {
	if signupRateLimited(c.ClientIP()) {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "Too many signup attempts. Please wait a few minutes."})
		return
	}

	var body struct {
		Name  string `json:"name"  binding:"required"`
		Email string `json:"email" binding:"required,email"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	body.Email = strings.ToLower(strings.TrimSpace(body.Email))
	body.Name = strings.TrimSpace(body.Name)

	// Reject if a verified account already exists
	if _, err := h.users.GetByEmail(body.Email); err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "An account with that email already exists."})
		return
	}

	// Generate cryptographically secure 6-digit OTP
	code, err := generateOTP()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate verification code."})
		return
	}

	// Store pending verification — no password yet, use empty string placeholder
	expiresAt := time.Now().Add(15 * time.Minute)
	if err := h.verifs.Upsert(body.Email, body.Name, "", hashOTP(code), expiresAt); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to initiate verification."})
		return
	}

	// Send OTP email with link back to verify page
	frontendURL := os.Getenv("FRONTEND_URL")
	if frontendURL == "" {
		frontendURL = "http://localhost:5173"
	}
	verifyURL := frontendURL + "/verify-email?email=" + body.Email

	if err := email.SendVerificationCode(body.Email, body.Name, code, verifyURL); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to send verification email. Please try again."})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Verification code sent. Please check your email.",
		"email":   body.Email,
	})
}

// POST /api/auth/check-code
// Step 1 of verification: validates the OTP only. Marks the record as verified.
// Does NOT create a user or require a password.
func (h *authHandler) checkCode(c *gin.Context) {
	var body struct {
		Email string `json:"email" binding:"required,email"`
		Code  string `json:"code"  binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	body.Email = strings.ToLower(strings.TrimSpace(body.Email))
	body.Code = strings.TrimSpace(body.Code)

	pending, err := h.verifs.GetByEmail(body.Email)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No pending verification found for this email."})
		return
	}

	// PIN already used — don't allow re-entry
	if pending.Verified {
		c.JSON(http.StatusBadRequest, gin.H{"error": "This code has already been used. Please create your password."})
		return
	}

	if time.Now().After(pending.ExpiresAt) {
		_ = h.verifs.Delete(body.Email)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Verification code has expired. Please sign up again."})
		return
	}

	if pending.Attempts >= 5 {
		_ = h.verifs.Delete(body.Email)
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "Too many incorrect attempts. Please sign up again."})
		return
	}

	if hashOTP(body.Code) != pending.CodeHash {
		_ = h.verifs.IncrementAttempts(body.Email)
		remaining := 5 - (pending.Attempts + 1)
		c.JSON(http.StatusUnauthorized, gin.H{
			"error":     "Incorrect verification code.",
			"remaining": remaining,
		})
		return
	}

	// PIN correct — mark as verified so complete-registration can proceed
	_ = h.verifs.MarkVerified(body.Email)

	c.JSON(http.StatusOK, gin.H{
		"message": "Code verified. Please create your password.",
		"email":   body.Email,
	})
}

// POST /api/auth/complete-registration
// Step 2: sets password and creates the real user. Requires PIN already verified.
func (h *authHandler) completeRegistration(c *gin.Context) {
	var body struct {
		Email    string `json:"email"    binding:"required,email"`
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	body.Email = strings.ToLower(strings.TrimSpace(body.Email))

	// Validate password strength
	if err := validatePassword(body.Password); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	pending, err := h.verifs.GetByEmail(body.Email)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No pending verification found. Please sign up again."})
		return
	}

	// Must have passed PIN check first
	if !pending.Verified {
		c.JSON(http.StatusForbidden, gin.H{"error": "Please verify your code before creating a password."})
		return
	}

	if time.Now().After(pending.ExpiresAt) {
		_ = h.verifs.Delete(body.Email)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Session expired. Please sign up again."})
		return
	}

	// Hash password
	hash, err := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to process password."})
		return
	}

	// Create the real user
	user, err := h.users.CreateEmail(body.Email, pending.Name, string(hash))
	if err != nil {
		if strings.Contains(err.Error(), "duplicate") || strings.Contains(err.Error(), "unique") {
			_ = h.verifs.Delete(body.Email)
			c.JSON(http.StatusConflict, gin.H{"error": "An account with that email already exists."})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create account."})
		return
	}

	_ = h.verifs.Delete(body.Email)

	token, err := signJWT(user.ID, user.Email, user.Name, user.Role)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to issue token."})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"token": token, "user": user.ToPublic()})
}

// POST /api/auth/resend-verification
func (h *authHandler) resendVerification(c *gin.Context) {
	if signupRateLimited(c.ClientIP()) {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "Too many requests. Please wait a few minutes."})
		return
	}

	var body struct {
		Email string `json:"email" binding:"required,email"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	body.Email = strings.ToLower(strings.TrimSpace(body.Email))

	pending, err := h.verifs.GetByEmail(body.Email)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "If a pending signup exists, a new code has been sent."})
		return
	}

	code, err := generateOTP()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate code."})
		return
	}

	expiresAt := time.Now().Add(15 * time.Minute)
	if err := h.verifs.Upsert(body.Email, pending.Name, "", hashOTP(code), expiresAt); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to refresh verification."})
		return
	}

	frontendURL := os.Getenv("FRONTEND_URL")
	if frontendURL == "" {
		frontendURL = "http://localhost:5173"
	}
	verifyURL := frontendURL + "/verify-email?email=" + body.Email

	if err := email.SendVerificationCode(body.Email, pending.Name, code, verifyURL); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to send email. Please try again."})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "A new verification code has been sent."})
}

// POST /api/auth/login
func (h *authHandler) login(c *gin.Context) {
	var body struct {
		Email    string `json:"email"    binding:"required,email"`
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	body.Email = strings.ToLower(strings.TrimSpace(body.Email))

	user, err := h.users.GetByEmail(body.Email)
	if err != nil || user.PasswordHash == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Wrong credentials! Please check your email and password."})
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(*user.PasswordHash), []byte(body.Password)); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Wrong credentials! Please check your email and password."})
		return
	}

	token, err := signJWT(user.ID, user.Email, user.Name, user.Role)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to issue token."})
		return
	}

	c.JSON(http.StatusOK, gin.H{"token": token, "user": user.ToPublic()})
}

// GET /api/auth/me
func (h *authHandler) me(c *gin.Context) {
	userID, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Not authenticated."})
		return
	}
	user, err := h.users.GetByID(userID.(string))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found."})
		return
	}
	c.JSON(http.StatusOK, user.ToPublic())
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// validatePassword enforces: min 8 chars, at least one uppercase, one digit, one symbol.
func validatePassword(pw string) error {
	if len(pw) < 8 {
		return fmt.Errorf("password must be at least 8 characters")
	}
	var hasUpper, hasDigit, hasSymbol bool
	for _, r := range pw {
		switch {
		case unicode.IsUpper(r):
			hasUpper = true
		case unicode.IsDigit(r):
			hasDigit = true
		case unicode.IsPunct(r) || unicode.IsSymbol(r):
			hasSymbol = true
		}
	}
	if !hasUpper {
		return fmt.Errorf("password must contain at least one uppercase letter")
	}
	if !hasDigit {
		return fmt.Errorf("password must contain at least one number")
	}
	if !hasSymbol {
		return fmt.Errorf("password must contain at least one symbol (e.g. !@#$)")
	}
	return nil
}

func generateOTP() (string, error) {
	max := big.NewInt(1_000_000)
	n, err := rand.Int(rand.Reader, max)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

func hashOTP(code string) string {
	sum := sha256.Sum256([]byte(code))
	return fmt.Sprintf("%x", sum)
}

func signJWT(userID, email, name, role string) (string, error) {
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		return "", fmt.Errorf("JWT_SECRET environment variable is not set")
	}
	claims := jwt.MapClaims{
		"sub":   userID,
		"email": email,
		"name":  name,
		"role":  role,
		"exp":   time.Now().Add(7 * 24 * time.Hour).Unix(),
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
}
