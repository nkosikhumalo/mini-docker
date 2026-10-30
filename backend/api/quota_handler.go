package api

import (
	"net/http"

	"foxvue-api/db"

	"github.com/gin-gonic/gin"
)

type quotaHandler struct {
	quota *db.QuotaRepo
}

func newQuotaHandler(quota *db.QuotaRepo) *quotaHandler {
	return &quotaHandler{quota: quota}
}

// GET /api/quota
func (h *quotaHandler) get(c *gin.Context) {
	userID, _ := c.Get("userID")
	uid, _ := userID.(string)
	if uid == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	status, err := h.quota.Get(uid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load quota"})
		return
	}
	c.JSON(http.StatusOK, status)
}
