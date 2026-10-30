// API key repository — stores user-provided keys encrypted with AES-256-GCM.
// Keys are NEVER logged or returned in plaintext after storage.

package db

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/jmoiron/sqlx"
)

// UserAPIKey is the DB row shape (encrypted_key is never sent to frontend).
type UserAPIKey struct {
	ID        string    `db:"id"            json:"id"`
	UserID    string    `db:"user_id"       json:"-"`
	Provider  string    `db:"provider"      json:"provider"`
	KeyHint   string    `db:"key_hint"      json:"keyHint"` // e.g. "****ab12"
	IsActive  bool      `db:"is_active"     json:"isActive"`
	Status    string    `db:"status"        json:"status"`
	CreatedAt time.Time `db:"created_at"    json:"createdAt"`
	UpdatedAt time.Time `db:"updated_at"    json:"updatedAt"`
}

type APIKeyRepo struct {
	db *sqlx.DB
}

func NewAPIKeyRepo(db *sqlx.DB) *APIKeyRepo {
	return &APIKeyRepo{db: db}
}

// Upsert saves (or replaces) a provider key for a user.
// Returns the masked row — never the plaintext key.
func (r *APIKeyRepo) Upsert(userID, provider, plaintextKey string) (*UserAPIKey, error) {
	encrypted, err := encrypt(plaintextKey)
	if err != nil {
		return nil, fmt.Errorf("encrypt: %w", err)
	}

	hint := maskKey(plaintextKey)

	var row UserAPIKey
	err = r.db.Get(&row, `
		INSERT INTO user_api_keys (user_id, provider, key_hint, encrypted_key, status)
		VALUES ($1, $2, $3, $4, 'untested')
		ON CONFLICT (user_id, provider) DO UPDATE
			SET key_hint     = EXCLUDED.key_hint,
			    encrypted_key = EXCLUDED.encrypted_key,
			    status        = 'untested',
			    is_active     = FALSE,
			    updated_at    = NOW()
		RETURNING id, user_id, provider, key_hint, is_active, status, created_at, updated_at`,
		userID, provider, hint, encrypted,
	)
	return &row, err
}

// List returns all keys for a user (masked, no plaintext).
func (r *APIKeyRepo) List(userID string) ([]UserAPIKey, error) {
	var rows []UserAPIKey
	err := r.db.Select(&rows, `
		SELECT id, user_id, provider, key_hint, is_active, status, created_at, updated_at
		FROM user_api_keys
		WHERE user_id = $1
		ORDER BY provider ASC`,
		userID,
	)
	if rows == nil {
		rows = []UserAPIKey{}
	}
	return rows, err
}

// SetActive marks one provider active and deactivates all others for the user.
func (r *APIKeyRepo) SetActive(userID, keyID string) error {
	tx, err := r.db.Beginx()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	_, err = tx.Exec(`UPDATE user_api_keys SET is_active = FALSE WHERE user_id = $1`, userID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`UPDATE user_api_keys SET is_active = TRUE, updated_at = NOW() WHERE id = $1 AND user_id = $2`, keyID, userID)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// SetStatus updates the test result for a key.
func (r *APIKeyRepo) SetStatus(keyID, status string) error {
	_, err := r.db.Exec(`UPDATE user_api_keys SET status = $1, updated_at = NOW() WHERE id = $2`, status, keyID)
	return err
}

// Delete removes a key.
func (r *APIKeyRepo) Delete(userID, keyID string) error {
	_, err := r.db.Exec(`DELETE FROM user_api_keys WHERE id = $1 AND user_id = $2`, keyID, userID)
	return err
}

// GetDecrypted returns the plaintext key for backend use only.
// NEVER expose this value to the frontend.
func (r *APIKeyRepo) GetDecrypted(userID, provider string) (string, error) {
	var encryptedKey string
	err := r.db.Get(&encryptedKey, `
		SELECT encrypted_key FROM user_api_keys
		WHERE user_id = $1 AND provider = $2 AND is_active = TRUE`,
		userID, provider,
	)
	if err != nil {
		return "", err
	}
	return decrypt(encryptedKey)
}

// GetActiveProvider returns the active provider name for a user, or "" if none.
func (r *APIKeyRepo) GetActiveProvider(userID string) (string, error) {
	var provider string
	err := r.db.Get(&provider, `
		SELECT provider FROM user_api_keys
		WHERE user_id = $1 AND is_active = TRUE
		LIMIT 1`,
		userID,
	)
	if errors.Is(err, errSQLNoRows) {
		return "", nil
	}
	return provider, err
}

// GetDecryptedByID returns the plaintext key for a specific key ID (for testing).
func (r *APIKeyRepo) GetDecryptedByID(userID, keyID string) (string, string, error) {
	var row struct {
		Provider     string `db:"provider"`
		EncryptedKey string `db:"encrypted_key"`
	}
	err := r.db.Get(&row, `
		SELECT provider, encrypted_key FROM user_api_keys
		WHERE id = $1 AND user_id = $2`,
		keyID, userID,
	)
	if err != nil {
		return "", "", err
	}
	plain, err := decrypt(row.EncryptedKey)
	return row.Provider, plain, err
}

// ── AES-256-GCM helpers ───────────────────────────────────────────────────────

var errSQLNoRows = fmt.Errorf("sql: no rows in result set")

func encryptionKey() ([]byte, error) {
	secret := os.Getenv("API_KEY_ENCRYPTION_SECRET")
	if len(secret) < 32 {
		return nil, fmt.Errorf("API_KEY_ENCRYPTION_SECRET must be at least 32 characters")
	}
	return []byte(secret[:32]), nil
}

func encrypt(plaintext string) (string, error) {
	key, err := encryptionKey()
	if err != nil {
		return "", err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}

	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

func decrypt(encoded string) (string, error) {
	key, err := encryptionKey()
	if err != nil {
		return "", err
	}

	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	if len(data) < gcm.NonceSize() {
		return "", fmt.Errorf("ciphertext too short")
	}

	nonce, ciphertext := data[:gcm.NonceSize()], data[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}

	return string(plaintext), nil
}

// maskKey returns a display-safe version: "sk-****ab12"
func maskKey(key string) string {
	if len(key) <= 4 {
		return "****"
	}
	suffix := key[len(key)-4:]
	return "****" + suffix
}
