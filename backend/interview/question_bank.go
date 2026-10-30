// Interview question bank logic.
// Generates tailored questions based on the user's job description.

package interview

import (
	"math/rand"
	"strings"

	"foxvue-api/models"
)

// rng is a package-level random source. As of Go 1.20 the global rand is
// automatically seeded — no need to call rand.Seed manually.
var rng = rand.New(rand.NewSource(rand.Int63()))

// NextQuestion returns a question candidate based on job description context.
// This is a simple heuristic implementation; later you can replace it with
// a model-driven question generator.
func NextQuestion(jobDescription string) *models.Question {
	jobLower := strings.ToLower(jobDescription)

	candidates := []*models.Question{}

	all := []*models.Question{
		{ID: 1, Text: "Explain what a Goroutine is and why it's useful.", Category: "Go"},
		{ID: 2, Text: "Describe a time you applied design patterns in OOP.", Category: "General OOP"},
		{ID: 3, Text: "How does React's virtual DOM improve performance?", Category: "Frontend"},
		{ID: 4, Text: "Walk me through your debugging process when production breaks.", Category: "Engineering Practice"},
	}

	for _, q := range all {
		if jobLower == "" {
			candidates = append(candidates, q)
			continue
		}

		// Very lightweight matching.
		if q.Category == "Go" && strings.Contains(jobLower, "go") {
			candidates = append(candidates, q)
		} else if q.Category == "Frontend" && (strings.Contains(jobLower, "react") || strings.Contains(jobLower, "javascript")) {
			candidates = append(candidates, q)
		} else if q.Category == "General OOP" && strings.Contains(jobLower, "oop") {
			candidates = append(candidates, q)
		} else if q.Category == "Engineering Practice" && (strings.Contains(jobLower, "debug") || strings.Contains(jobLower, "production") || strings.Contains(jobLower, "incident")) {
			candidates = append(candidates, q)
		}
	}

	if len(candidates) == 0 {
		candidates = all
	}

	return candidates[rng.Intn(len(candidates))]
}
