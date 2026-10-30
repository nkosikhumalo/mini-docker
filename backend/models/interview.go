// Interview request/response models and session entities.

package models

import "time"

type CreateSessionRequest struct {
	JobDescription string `json:"jobDescription"`
}

type CreateSessionResponse struct {
	SessionID string `json:"sessionId"`
}

type NextQuestionRequest struct {
	SessionID string
}

type SubmitAnswerRequest struct {
	SessionID  string `json:"sessionId"`
	QuestionID int    `json:"questionId"`
	Transcript string `json:"transcript"`
}

type Feedback struct {
	Score       int            `json:"score"`
	Summary     string         `json:"summary"`
	FillerWords map[string]int `json:"fillerWords"`
	Star        string         `json:"star"`
}

type SubmitAnswerResponse struct {
	Feedback     Feedback  `json:"feedback"`
	NextQuestion *Question `json:"nextQuestion,omitempty"`
}

// HistoryEntry records one answered question within a session.
type HistoryEntry struct {
	Question   Question  `json:"question"`
	Transcript string    `json:"transcript"`
	Feedback   Feedback  `json:"feedback"`
	AnsweredAt time.Time `json:"answeredAt"`
}

// Session holds interview context for the lifetime of the process.
type Session struct {
	ID              string
	JobDescription  string
	CurrentQuestion *Question
	History         []HistoryEntry
	CreatedAt       time.Time
}

// SessionSummary is the public shape returned by GET /api/interview/history.
type SessionSummary struct {
	SessionID      string         `json:"sessionId"`
	JobDescription string         `json:"jobDescription"`
	CreatedAt      time.Time      `json:"createdAt"`
	History        []HistoryEntry `json:"history"`
}
