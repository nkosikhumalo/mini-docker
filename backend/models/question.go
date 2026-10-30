package models

// Question is a single interview question with skill mapping.
type Question struct {
	ID       int    `json:"id"       db:"question_id"`
	Text     string `json:"text"     db:"question_text"`
	Category string `json:"category" db:"category"`
	Skill    string `json:"skill"    db:"skill"`
}

// GenerateQuestionsRequest is the payload for POST /api/interview/generate-questions.
type GenerateQuestionsRequest struct {
	SessionID      string `json:"sessionId"`
	JobTitle       string `json:"jobTitle"`
	JobDescription string `json:"jobDescription"`
}

// GenerateQuestionsResponse is returned after Gemini generates questions.
type GenerateQuestionsResponse struct {
	SessionID string     `json:"sessionId"`
	Questions []Question `json:"questions"`
}

// EvaluateAnswerRequest is the payload for POST /api/interview/evaluate-answer.
type EvaluateAnswerRequest struct {
	SessionID  string   `json:"sessionId"`
	Question   Question `json:"question"`
	Transcript string   `json:"transcript"`
}

// EvaluationResult is the rich AI feedback returned per answer.
type EvaluationResult struct {
	Score          int      `json:"score"`          // 0-10
	Clarity        int      `json:"clarity"`        // 0-10
	TechnicalScore int      `json:"technicalScore"` // 0-10
	Communication  int      `json:"communication"`  // 0-10
	Strengths      []string `json:"strengths"`
	Weaknesses     []string `json:"weaknesses"`
	SampleAnswer   string   `json:"sampleAnswer"`
	FollowUp       string   `json:"followUp"`
	Summary        string   `json:"summary"`
	// Legacy fields kept for history compatibility
	FillerWords map[string]int `json:"fillerWords"`
	Star        string         `json:"star"`
}
