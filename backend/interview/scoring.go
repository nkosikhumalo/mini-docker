// Interview scoring logic.
// Computes simple feedback metrics from the submitted transcript.

package interview

import (
	"math"
	"regexp"
	"strings"

	"foxvue-api/models"
)

var fillerWords = []string{
	"um",
	"uh",
	"like",
	"you know",
	"actually",
	"basically",
}

func ScoreFeedback(transcript string) models.Feedback {
	t := strings.ToLower(transcript)

	counts := map[string]int{}

	for _, fw := range fillerWords {
		pattern := regexp.QuoteMeta(fw)
		// Word-boundary matching helps avoid counting substrings.
		re := regexp.MustCompile(`(?i)\b` + pattern + `\b`)
		// Special-case multi-word phrases.
		if strings.Contains(fw, " ") {
			re = regexp.MustCompile(`(?i)` + pattern)
		}
		matches := re.FindAllStringIndex(t, -1)
		if len(matches) > 0 {
			counts[fw] = len(matches)
		} else {
			counts[fw] = 0
		}
	}

	starScore, starLabel := starCompleteness(t)

	// Simple scoring heuristic:
	// - start with STAR score weight
	// - penalize filler words
	totalFillers := 0
	for _, c := range counts {
		totalFillers += c
	}

	raw := 40 + (starScore * 15) - (totalFillers * 2)
	score := int(math.Max(0, math.Min(100, float64(raw))))

	summary := buildSummary(score, totalFillers, counts, starLabel)

	return models.Feedback{
		Score:       score,
		Summary:     summary,
		FillerWords: counts,
		Star:        starLabel,
	}
}

func starCompleteness(transcriptLower string) (int, string) {
	// Look for explicit STAR markers. This is a heuristic and can be replaced
	// with an LLM-based evaluator later.
	markers := map[string]struct{}{
		"situation": {},
		"task":      {},
		"action":    {},
		"result":    {},
	}

	found := 0
	for m := range markers {
		if strings.Contains(transcriptLower, m) {
			found++
		}
	}

	switch found {
	case 4:
		return 4, "Strong"
	case 3:
		return 3, "Good"
	case 2:
		return 2, "Fair"
	default:
		return found, "Needs work"
	}
}

func buildSummary(score int, totalFillers int, counts map[string]int, star string) string {
	// Keep the summary short and actionable.
	if score >= 85 {
		return "Great structure and clarity. Keep your pace steady and watch for filler words."
	}

	if totalFillers > 6 {
		return "Good effort, but filler words are pulling attention. Try pausing before key points and aim to structure answers using STAR."
	}

	// Mention STAR explicitly if missing.
	if star != "Strong" {
		return "Your answer shows some STAR elements. Strengthen it by explicitly covering Situation, Task, Action, and Result."
	}

	// Default.
	return "Solid response. Refine clarity and reduce filler words to improve interview impact."
}
