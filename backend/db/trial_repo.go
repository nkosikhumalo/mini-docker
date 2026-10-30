package db

import (
	"database/sql"
	"errors"
	"time"

	"github.com/jmoiron/sqlx"
)

const TrialMaxTries = 3

type Trial struct {
	ID             string    `db:"id"`
	IPAddress      string    `db:"ip_address"`
	TriesRemaining int       `db:"tries_remaining"`
	CreatedAt      time.Time `db:"created_at"`
}

type TrialRepo struct {
	db *sqlx.DB
}

func NewTrialRepo(db *sqlx.DB) *TrialRepo {
	return &TrialRepo{db: db}
}

// GetByID returns a trial by its UUID cookie value.
func (r *TrialRepo) GetByID(id string) (*Trial, error) {
	var t Trial
	err := r.db.Get(&t, `SELECT * FROM trials WHERE id = $1`, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &t, err
}

// GetByIP returns the trial for a given IP, if one exists.
func (r *TrialRepo) GetByIP(ip string) (*Trial, error) {
	var t Trial
	err := r.db.Get(&t, `SELECT * FROM trials WHERE ip_address = $1`, ip)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &t, err
}

// Create inserts a new trial for an IP and returns it.
// Returns an error if a trial already exists for that IP (unique constraint).
func (r *TrialRepo) Create(ip string) (*Trial, error) {
	var t Trial
	err := r.db.Get(&t, `
		INSERT INTO trials (ip_address, tries_remaining)
		VALUES ($1, $2)
		RETURNING *`,
		ip, TrialMaxTries,
	)
	return &t, err
}

// Decrement reduces tries_remaining by 1 atomically.
// Returns the updated trial. Returns ErrNotFound if exhausted or missing.
func (r *TrialRepo) Decrement(id string) (*Trial, error) {
	var t Trial
	err := r.db.Get(&t, `
		UPDATE trials
		SET tries_remaining = tries_remaining - 1
		WHERE id = $1 AND tries_remaining > 0
		RETURNING *`,
		id,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &t, err
}
