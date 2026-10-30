// QuotaRepo manages per-user session quotas.
// Free plan: 2 platform-key sessions. BYOK users are exempt.

package db

import (
	"github.com/jmoiron/sqlx"
)

const FreePlanLimit = 2

type QuotaRepo struct {
	db *sqlx.DB
}

func NewQuotaRepo(db *sqlx.DB) *QuotaRepo {
	return &QuotaRepo{db: db}
}

type QuotaStatus struct {
	Plan             string `db:"plan"               json:"plan"`
	FreeSessionsUsed int    `db:"free_sessions_used" json:"freeSessionsUsed"`
	Limit            int    `json:"limit"`
	Remaining        int    `json:"remaining"`
	Exceeded         bool   `json:"exceeded"`
}

// Get returns the current quota status for a user.
func (r *QuotaRepo) Get(userID string) (*QuotaStatus, error) {
	var q QuotaStatus
	err := r.db.Get(&q, `
		SELECT plan, free_sessions_used
		FROM users WHERE id = $1`, userID)
	if err != nil {
		return nil, err
	}
	q.Limit = FreePlanLimit
	if q.Plan != "free" {
		q.Limit = -1 // unlimited for paid plans
	}
	// Check role — admins get unlimited
	var role string
	_ = r.db.Get(&role, `SELECT role FROM users WHERE id = $1`, userID)
	if role == "admin" {
		q.Limit = -1
	}
	remaining := q.Limit - q.FreeSessionsUsed
	if q.Limit == -1 {
		remaining = 9999
	}
	if remaining < 0 {
		remaining = 0
	}
	q.Remaining = remaining
	q.Exceeded = q.Plan == "free" && role != "admin" && q.FreeSessionsUsed >= FreePlanLimit
	return &q, nil
}

// Increment atomically increments free_sessions_used for a free-plan user.
// Returns the new count. No-op for paid plans.
func (r *QuotaRepo) Increment(userID string) error {
	_, err := r.db.Exec(`
		UPDATE users
		SET free_sessions_used = free_sessions_used + 1,
		    updated_at = NOW()
		WHERE id = $1 AND plan = 'free'`, userID)
	return err
}
