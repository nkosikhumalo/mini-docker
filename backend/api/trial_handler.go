package api

import (
	"net/http"

	"foxvue-api/db"

	"github.com/gin-gonic/gin"
)

type trialHandler struct {
	trials *db.TrialRepo
}

func newTrialHandler(trials *db.TrialRepo) *trialHandler {
	return &trialHandler{trials: trials}
}

// GET /api/trial/status
// Returns the current trial state for the caller (by cookie or IP).
func (h *trialHandler) status(c *gin.Context) {
	clientIP := realIP(c)

	cookieVal, err := c.Cookie(trialCookieName)
	if err != nil || cookieVal == "" {
		// Check by IP
		trial, err := h.trials.GetByIP(clientIP)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"hasTrialCookie": false,
				"triesRemaining": db.TrialMaxTries,
				"trialExhausted": false,
			})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"hasTrialCookie": false,
			"triesRemaining": trial.TriesRemaining,
			"trialExhausted": trial.TriesRemaining <= 0,
		})
		return
	}

	trial, err := h.trials.GetByID(cookieVal)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"hasTrialCookie": true,
			"triesRemaining": 0,
			"trialExhausted": true,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"hasTrialCookie": true,
		"triesRemaining": trial.TriesRemaining,
		"trialExhausted": trial.TriesRemaining <= 0,
	})
}
