package ai

import "foxvue-api/models"

// Provider is the single interface every AI backend must satisfy.
type Provider interface {
	GenerateQuestions(jobTitle, jobDescription string) ([]models.Question, error)
	EvaluateAnswer(question models.Question, transcript string) (*models.EvaluationResult, error)
}
