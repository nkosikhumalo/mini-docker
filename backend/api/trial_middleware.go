package api

import (
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"foxvue-api/db"
)

const trialCookieName = "fv_trial"
const trialCookieTTL = 30 * 24 * time.Hour // 30 days

// TrialMiddleware handles guest (unauthenticated) interview requests.
//
// Flow:
//  1. If a valid JWT is present → treat as authenticated user, skip trial logic.
//  2. If no cookie → check IP hasn't had a trial before → create trial → set cookie.
//  3. If cookie present → validate trial in DB → enforce IP match → decrement.
//  4. If trial exhausted or IP already used → 402 TRIAL_EXHAUSTED.
func TrialMiddleware(trials *db.TrialRepo) gin.HandlerFunc {
	return func(c *gin.Context) {
		// ── If the request carries a valid JWT, let RequireInterviewAuth handle it ──
		// We just set userID and skip trial logic entirely.
		if claims, err := parseBearer(c); err == nil {
			uid, _ := claims["sub"].(string)
			if uid != "" {
				c.Set("userID", uid)
				c.Set("email", claims["email"])
				c.Set("name", claims["name"])
				c.Set("role", claims["role"])
				c.Next()
				return
			}
		}

		// ── Guest path ────────────────────────────────────────────────────────────
		clientIP := realIP(c)

		cookieVal, cookieErr := c.Cookie(trialCookieName)

		if cookieErr != nil || cookieVal == "" {
			// No cookie — check if this IP already has a trial (abuse prevention)
			existing, err := trials.GetByIP(clientIP)
			if err == nil {
				// IP already has a trial record
				if existing.TriesRemaining <= 0 {
					c.AbortWithStatusJSON(http.StatusPaymentRequired, gin.H{
						"error": "Your free trial has been used up. Please sign up to continue.",
						"code":  "TRIAL_EXHAUSTED",
					})
					return
				}
				// IP has tries left but lost their cookie — reuse existing trial
				setTrialCookie(c, existing.ID)
				cookieVal = existing.ID
			} else {
				// Brand new IP — create a trial
				trial, err := trials.Create(clientIP)
				if err != nil {
					// Unique constraint hit = race condition, another request just created it
					existing2, err2 := trials.GetByIP(clientIP)
					if err2 != nil {
						log.Printf("[trial] failed to create or fetch trial for IP %s: %v", clientIP, err)
						c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "Failed to initialise trial."})
						return
					}
					trial = existing2
				}
				setTrialCookie(c, trial.ID)
				cookieVal = trial.ID
				log.Printf("[trial] new trial created for IP %s, id=%s", clientIP, trial.ID)
			}
		}

		// ── Validate the trial from DB ────────────────────────────────────────────
		trial, err := trials.GetByID(cookieVal)
		if err != nil {
			// Cookie is invalid/tampered — clear it and reject
			clearTrialCookie(c)
			c.AbortWithStatusJSON(http.StatusPaymentRequired, gin.H{
				"error": "Invalid trial session. Please sign up to continue.",
				"code":  "TRIAL_INVALID",
			})
			return
		}

		// IP binding — prevent cookie sharing across IPs
		if trial.IPAddress != clientIP {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error": "Trial session is bound to a different network.",
				"code":  "TRIAL_IP_MISMATCH",
			})
			return
		}

		if trial.TriesRemaining <= 0 {
			c.AbortWithStatusJSON(http.StatusPaymentRequired, gin.H{
				"error": "Your free trial has been used up. Please sign up to continue.",
				"code":  "TRIAL_EXHAUSTED",
			})
			return
		}

		// ── Decrement atomically ──────────────────────────────────────────────────
		updated, err := trials.Decrement(trial.ID)
		if err != nil {
			// Decrement returned no rows = exhausted between check and update
			c.AbortWithStatusJSON(http.StatusPaymentRequired, gin.H{
				"error": "Your free trial has been used up. Please sign up to continue.",
				"code":  "TRIAL_EXHAUSTED",
			})
			return
		}

		log.Printf("[trial] id=%s ip=%s tries_remaining=%d", updated.ID, clientIP, updated.TriesRemaining)

		// Pass trial info downstream so handlers can include it in responses
		c.Set("userID", "")
		c.Set("trialID", updated.ID)
		c.Set("trialRemaining", updated.TriesRemaining)
		c.Next()
	}
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func setTrialCookie(c *gin.Context, id string) {
	maxAge := int(trialCookieTTL.Seconds())
	secure := os.Getenv("GIN_MODE") == "release"
	c.SetCookie(trialCookieName, id, maxAge, "/", "", secure, true)
}

func clearTrialCookie(c *gin.Context) {
	c.SetCookie(trialCookieName, "", -1, "/", "", true, true)
}

// realIP extracts the client IP.
// On Azure App Service the infrastructure appends the real client IP as the
// LAST entry in X-Forwarded-For, so we take that entry rather than the first
// (which a client can spoof by sending their own header).
// Falls back to RemoteAddr when no forwarded header is present.
func realIP(c *gin.Context) string {
	if xff := c.GetHeader("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		// Last entry is appended by the trusted Azure load-balancer
		ip := strings.TrimSpace(parts[len(parts)-1])
		if ip != "" {
			return ip
		}
	}
	if ip := c.GetHeader("X-Real-IP"); ip != "" {
		return strings.TrimSpace(ip)
	}
	return c.ClientIP()
}
