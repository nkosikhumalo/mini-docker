package db

import (
	"encoding/json"
	"time"

	"foxvue-api/models"

	"github.com/jmoiron/sqlx"
)

type SessionRepo struct {
	db *sqlx.DB
}

func NewSessionRepo(db *sqlx.DB) *SessionRepo {
	return &SessionRepo{db: db}
}

// LinkSession attaches an authenticated user and job title to a session row
// that was created by the store with a nil user_id placeholder.
func (r *SessionRepo) LinkSession(sessionID, userID, jobTitle string) error {
	_, err := r.db.Exec(`
		UPDATE interview_sessions
		SET user_id = $1, job_title = $2
		WHERE id = $3`,
		userID, jobTitle, sessionID,
	)
	return err
}

func (r *SessionRepo) CreateSession(sessionID, userID, jobTitle, jobDescription string) error {
	_, err := r.db.Exec(`
		INSERT INTO interview_sessions (id, user_id, job_title, job_description)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (id) DO NOTHING`,
		sessionID, userID, jobTitle, jobDescription,
	)
	return err
}

func (r *SessionRepo) SaveQuestions(sessionID string, questions []models.Question) error {
	for i, q := range questions {
		_, err := r.db.Exec(`
			INSERT INTO session_questions (session_id, question_idx, question_text, category, skill)
			VALUES ($1, $2, $3, $4, $5)`,
			sessionID, i, q.Text, q.Category, q.Skill,
		)
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *SessionRepo) GetQuestions(sessionID string) ([]models.Question, error) {
	rows, err := r.db.Queryx(`
		SELECT question_idx, question_text, category, skill
		FROM session_questions
		WHERE session_id = $1
		ORDER BY question_idx ASC`,
		sessionID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var questions []models.Question
	for rows.Next() {
		var idx int
		var q models.Question
		if err := rows.Scan(&idx, &q.Text, &q.Category, &q.Skill); err != nil {
			return nil, err
		}
		q.ID = idx + 1
		questions = append(questions, q)
	}
	return questions, rows.Err()
}

func (r *SessionRepo) SaveAnswer(sessionID string, q models.Question, transcript string, eval *models.EvaluationResult) error {
	strengthsJSON, _ := json.Marshal(eval.Strengths)
	weaknessesJSON, _ := json.Marshal(eval.Weaknesses)
	fillerJSON, _ := json.Marshal(eval.FillerWords)

	_, err := r.db.Exec(`
		INSERT INTO interview_answers
			(session_id, question_id, question_text, category, skill, transcript,
			 score, clarity, technical_score, communication,
			 star, summary, strengths, weaknesses, sample_answer, follow_up, filler_words, answered_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)`,
		sessionID, q.ID, q.Text, q.Category, q.Skill, transcript,
		eval.Score, eval.Clarity, eval.TechnicalScore, eval.Communication,
		eval.Star, eval.Summary,
		string(strengthsJSON), string(weaknessesJSON),
		eval.SampleAnswer, eval.FollowUp,
		string(fillerJSON),
		time.Now(),
	)
	return err
}

func (r *SessionRepo) GetSessionHistory(sessionID string) ([]models.HistoryEntry, error) {
	rows, err := r.db.Queryx(`
		SELECT question_id, question_text, category, skill, transcript,
		       score, star, summary, filler_words, answered_at
		FROM interview_answers
		WHERE session_id = $1
		ORDER BY answered_at ASC`,
		sessionID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var entries []models.HistoryEntry
	for rows.Next() {
		var (
			qID, score                      int
			qText, qCat, qSkill, transcript string
			star, summary, fillerRaw        string
			answeredAt                      time.Time
		)
		if err := rows.Scan(&qID, &qText, &qCat, &qSkill, &transcript,
			&score, &star, &summary, &fillerRaw, &answeredAt); err != nil {
			return nil, err
		}
		var fillerWords map[string]int
		_ = json.Unmarshal([]byte(fillerRaw), &fillerWords)

		entries = append(entries, models.HistoryEntry{
			Question:   models.Question{ID: qID, Text: qText, Category: qCat, Skill: qSkill},
			Transcript: transcript,
			Feedback: models.Feedback{
				Score:       score,
				Star:        star,
				Summary:     summary,
				FillerWords: fillerWords,
			},
			AnsweredAt: answeredAt,
		})
	}
	return entries, rows.Err()
}

// GetAllSessions returns all sessions for a user, newest first.
func (r *SessionRepo) GetAllSessions(userID string) ([]map[string]any, error) {
	rows, err := r.db.Queryx(`
		SELECT s.id, s.job_title, s.job_description, s.created_at,
		       COUNT(a.id) AS answer_count,
		       COALESCE(AVG(a.score), 0) AS avg_score
		FROM interview_sessions s
		LEFT JOIN interview_answers a ON a.session_id = s.id
		WHERE s.user_id = $1
		GROUP BY s.id
		ORDER BY s.created_at DESC`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var sessions []map[string]any
	for rows.Next() {
		var (
			id, jobTitle, jobDesc string
			createdAt             time.Time
			answerCount           int
			avgScore              float64
		)
		if err := rows.Scan(&id, &jobTitle, &jobDesc, &createdAt, &answerCount, &avgScore); err != nil {
			return nil, err
		}
		sessions = append(sessions, map[string]any{
			"sessionId":      id,
			"jobTitle":       jobTitle,
			"jobDescription": jobDesc,
			"createdAt":      createdAt,
			"answerCount":    answerCount,
			"avgScore":       int(avgScore),
		})
	}
	if sessions == nil {
		sessions = []map[string]any{}
	}
	return sessions, rows.Err()
}

func (r *SessionRepo) GetSessionMeta(sessionID string) (jobTitle, jobDesc string, createdAt time.Time, err error) {
	err = r.db.QueryRow(`
		SELECT job_title, job_description, created_at FROM interview_sessions WHERE id = $1`,
		sessionID,
	).Scan(&jobTitle, &jobDesc, &createdAt)
	return
}

// GetSessionMetaForUser returns session metadata only if the session belongs to userID.
func (r *SessionRepo) GetSessionMetaForUser(sessionID, userID string) (jobTitle, jobDesc string, createdAt time.Time, err error) {
	err = r.db.QueryRow(`
		SELECT job_title, job_description, created_at
		FROM interview_sessions
		WHERE id = $1 AND user_id = $2`,
		sessionID, userID,
	).Scan(&jobTitle, &jobDesc, &createdAt)
	return
}

// DeleteSession removes a session owned by userID. Related questions/answers cascade.
func (r *SessionRepo) DeleteSession(sessionID, userID string) (bool, error) {
	res, err := r.db.Exec(`
		DELETE FROM interview_sessions
		WHERE id = $1 AND user_id = $2`,
		sessionID, userID,
	)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}
