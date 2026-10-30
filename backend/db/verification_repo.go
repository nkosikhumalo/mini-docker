package db

import (
	"database/sql"
	"errors"
	"time"

	"github.com/jmoiron/sqlx"
)

// PendingVerification holds a not-yet-activated signup.
type PendingVerification struct {
	ID           string    `db:"id"`
	Email        string    `db:"email"`
	Name         string    `db:"name"`
	PasswordHash string    `db:"password_hash"`
	CodeHash     string    `db:"code_hash"`
	Attempts     int       `db:"attempts"`
	Verified     bool      `db:"verified"`
	ExpiresAt    time.Time `db:"expires_at"`
	CreatedAt    time.Time `db:"created_at"`
}

type VerificationRepo struct {
	db *sqlx.DB
}

func NewVerificationRepo(db *sqlx.DB) *VerificationRepo {
	return &VerificationRepo{db: db}
}

// Upsert inserts or replaces a pending verification for an email.
// Replaces on conflict so users can re-register if their code expired.
func (r *VerificationRepo) Upsert(email, name, passwordHash, codeHash string, expiresAt time.Time) error {
	_, err := r.db.Exec(`
		INSERT INTO email_verifications (email, name, password_hash, code_hash, attempts, expires_at)
		VALUES ($1, $2, $3, $4, 0, $5)
		ON CONFLICT (email) DO UPDATE
			SET name          = EXCLUDED.name,
			    password_hash = EXCLUDED.password_hash,
			    code_hash     = EXCLUDED.code_hash,
			    attempts      = 0,
			    expires_at    = EXCLUDED.expires_at,
			    created_at    = NOW()`,
		email, name, passwordHash, codeHash, expiresAt,
	)
	return err
}

// GetByEmail returns the pending record or ErrNotFound.
func (r *VerificationRepo) GetByEmail(email string) (*PendingVerification, error) {
	var v PendingVerification
	err := r.db.Get(&v, `SELECT * FROM email_verifications WHERE email = $1`, email)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &v, err
}

// IncrementAttempts bumps the failed-attempt counter.
func (r *VerificationRepo) IncrementAttempts(email string) error {
	_, err := r.db.Exec(`
		UPDATE email_verifications SET attempts = attempts + 1 WHERE email = $1`, email)
	return err
}

// MarkVerified sets verified=true and clears the code_hash so the PIN cannot be reused.
func (r *VerificationRepo) MarkVerified(email string) error {
	_, err := r.db.Exec(`
		UPDATE email_verifications 
		SET verified = TRUE, code_hash = '', attempts = 0
		WHERE email = $1`, email)
	return err
}

// Delete removes the pending record (called after successful verification).
func (r *VerificationRepo) Delete(email string) error {
	_, err := r.db.Exec(`DELETE FROM email_verifications WHERE email = $1`, email)
	return err
}

// DeleteExpired cleans up stale rows (call periodically or at startup).
func (r *VerificationRepo) DeleteExpired() error {
	_, err := r.db.Exec(`DELETE FROM email_verifications WHERE expires_at < NOW()`)
	return err
}
