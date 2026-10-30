package db

import (
	"database/sql"
	"errors"

	"foxvue-api/models"

	"github.com/jmoiron/sqlx"
)

// ErrNotFound is returned when a record doesn't exist.
var ErrNotFound = errors.New("not found")

type UserRepo struct {
	db *sqlx.DB
}

func NewUserRepo(db *sqlx.DB) *UserRepo {
	return &UserRepo{db: db}
}

// GetByEmail returns a user by email, or ErrNotFound.
func (r *UserRepo) GetByEmail(email string) (*models.User, error) {
	var u models.User
	err := r.db.Get(&u, `SELECT * FROM users WHERE email = $1`, email)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &u, err
}

// GetByID returns a user by primary key.
func (r *UserRepo) GetByID(id string) (*models.User, error) {
	var u models.User
	err := r.db.Get(&u, `SELECT * FROM users WHERE id = $1`, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &u, err
}

// CreateEmail inserts a new email/password user and returns the created row.
func (r *UserRepo) CreateEmail(email, name, passwordHash string) (*models.User, error) {
	var u models.User
	err := r.db.Get(&u, `
		INSERT INTO users (email, name, password_hash, provider)
		VALUES ($1, $2, $3, 'email')
		RETURNING *`,
		email, name, passwordHash,
	)
	return &u, err
}

// UpsertOAuth inserts or updates an OAuth user and returns the row.
// If a user with the same email already exists, we update their name and provider info.
func (r *UserRepo) UpsertOAuth(email, name, provider, providerID string) (*models.User, error) {
	var u models.User
	err := r.db.Get(&u, `
		INSERT INTO users (email, name, provider, provider_id)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (email) DO UPDATE
			SET name        = EXCLUDED.name,
			    provider    = EXCLUDED.provider,
			    provider_id = EXCLUDED.provider_id,
			    updated_at  = NOW()
		RETURNING *`,
		email, name, provider, providerID,
	)
	return &u, err
}

// SetResetToken stores a hashed reset token and expiry for a user.
func (r *UserRepo) SetResetToken(userID, hashedToken string, expiresAt interface{}) error {
	_, err := r.db.Exec(`
		UPDATE users
		SET reset_token = $1, reset_token_expires_at = $2, updated_at = NOW()
		WHERE id = $3`,
		hashedToken, expiresAt, userID,
	)
	return err
}

// GetByResetToken finds a user whose hashed reset token matches and has not expired.
func (r *UserRepo) GetByResetToken(hashedToken string) (*models.User, error) {
	var u models.User
	err := r.db.Get(&u, `
		SELECT * FROM users
		WHERE reset_token = $1
		  AND reset_token_expires_at > NOW()`,
		hashedToken,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &u, err
}

// ClearResetToken removes the reset token after successful use.
func (r *UserRepo) ClearResetToken(userID string) error {
	_, err := r.db.Exec(`
		UPDATE users
		SET reset_token = NULL, reset_token_expires_at = NULL, updated_at = NOW()
		WHERE id = $1`,
		userID,
	)
	return err
}

// UpdatePassword sets a new bcrypt password hash for a user.
func (r *UserRepo) UpdatePassword(userID, newHash string) error {
	_, err := r.db.Exec(`
		UPDATE users SET password_hash = $1, updated_at = NOW() WHERE id = $2`,
		newHash, userID,
	)
	return err
}
