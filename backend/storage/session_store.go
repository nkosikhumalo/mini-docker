// Session storage for the interview flow.
//
// DBSessionStore is the production implementation — backed by PostgreSQL so
// sessions survive restarts, redeploys, and scale-out events on Azure.
//
// The interface is kept minimal: Create generates an ID and persists the
// session row; everything else (questions, answers) is owned by SessionRepo.
// UpdateQuestion and AppendHistory are intentionally no-ops here because:
//   - Questions are returned directly to the frontend and sent back per request.
//   - Answers are saved by the interview handler via SessionRepo.SaveAnswer.

package storage

import (
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/jmoiron/sqlx"

	"foxvue-api/models"
)

type SessionStore interface {
	Create(jobDescription string) *models.Session
	Get(sessionID string) (*models.Session, bool)
	UpdateQuestion(sessionID string, q *models.Question) bool
	AppendHistory(sessionID string, entry models.HistoryEntry) bool
}

// NewSessionStore returns a DB-backed SessionStore.
func NewSessionStore(db *sqlx.DB) SessionStore {
	return &dbSessionStore{db: db}
}

type dbSessionStore struct {
	db *sqlx.DB
}

// Create generates a new session ID, persists a placeholder row (no user yet —
// the interview handler links it to a user after auth is resolved), and returns
// the Session so the handler can use the ID immediately.
func (s *dbSessionStore) Create(jobDescription string) *models.Session {
	id := newSessionID()
	now := time.Now()

	// Best-effort insert — if it fails the session ID is still returned so the
	// interview can proceed; the handler will retry via CreateSession once the
	// user ID is known.
	_, _ = s.db.Exec(`
		INSERT INTO interview_sessions (id, user_id, job_title, job_description)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (id) DO NOTHING`,
		id, nil, "", jobDescription,
	)

	return &models.Session{
		ID:             id,
		JobDescription: jobDescription,
		CreatedAt:      now,
		History:        []models.HistoryEntry{},
	}
}

// Get is not used by any handler at runtime — the frontend owns question state
// and sends it back with each request. Kept to satisfy the interface.
func (s *dbSessionStore) Get(sessionID string) (*models.Session, bool) {
	var jobDesc string
	var createdAt time.Time
	err := s.db.QueryRow(`
		SELECT job_description, created_at
		FROM interview_sessions WHERE id = $1`, sessionID,
	).Scan(&jobDesc, &createdAt)
	if err != nil {
		return nil, false
	}
	return &models.Session{
		ID:             sessionID,
		JobDescription: jobDesc,
		CreatedAt:      createdAt,
		History:        []models.HistoryEntry{},
	}, true
}

// UpdateQuestion is a no-op — questions are held on the frontend and sent back
// with each evaluate-answer request, so no server-side tracking is needed.
func (s *dbSessionStore) UpdateQuestion(_ string, _ *models.Question) bool {
	return true
}

// AppendHistory is a no-op — answers are persisted directly by the interview
// handler via SessionRepo.SaveAnswer.
func (s *dbSessionStore) AppendHistory(_ string, _ models.HistoryEntry) bool {
	return true
}

// newSessionID creates a cryptographically random 32-hex-char session ID.
func newSessionID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
