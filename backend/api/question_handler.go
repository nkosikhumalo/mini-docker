// HTTP handlers related to interview questions.
// getQuestion is available for direct question lookup by job description.

package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"foxvue-api/interview"
	"foxvue-api/models"
)

// GET /api/question?jobDescription=...
func getQuestion(c *gin.Context) {
	jobDescription := c.Query("jobDescription")
	q := interview.NextQuestion(jobDescription)
	if q == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "no questions available"})
		return
	}
	c.JSON(http.StatusOK, models.Question{
		ID:       q.ID,
		Text:     q.Text,
		Category: q.Category,
	})
}
