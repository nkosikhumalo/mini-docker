// db package — PostgreSQL connection and schema migrations.
// Call db.Connect() once at startup; it returns a *sqlx.DB and runs migrations.

package db

import (
	"fmt"
	"log"
	"os"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
)

// DB wraps sqlx.DB so we can pass it around as a single dependency.
type DB struct {
	*sqlx.DB
}

// Connect opens a PostgreSQL connection and runs migrations.
func Connect() (*DB, error) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return nil, fmt.Errorf("DATABASE_URL is not set")
	}

	sqlxDB, err := sqlx.Connect("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to postgres: %w", err)
	}

	sqlxDB.SetMaxOpenConns(25)
	sqlxDB.SetMaxIdleConns(5)

	if err := migrate(sqlxDB); err != nil {
		return nil, fmt.Errorf("migration failed: %w", err)
	}

	log.Println("[db] connected and migrations applied")
	return &DB{sqlxDB}, nil
}

// migrate creates all tables if they don't already exist.
func migrate(db *sqlx.DB) error {
	schema := `
	-- Users table
	CREATE TABLE IF NOT EXISTS users (
		id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		email         TEXT UNIQUE NOT NULL,
		name          TEXT NOT NULL DEFAULT '',
		password_hash TEXT,
		provider      TEXT NOT NULL DEFAULT 'email',
		provider_id   TEXT,
		plan          TEXT NOT NULL DEFAULT 'free',
		free_sessions_used INT NOT NULL DEFAULT 0,
		role          TEXT NOT NULL DEFAULT 'user',
		created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);

	-- Add quota columns to existing users table
	ALTER TABLE users ADD COLUMN IF NOT EXISTS plan TEXT NOT NULL DEFAULT 'free';
	ALTER TABLE users ADD COLUMN IF NOT EXISTS free_sessions_used INT NOT NULL DEFAULT 0;
	ALTER TABLE users ADD COLUMN IF NOT EXISTS role TEXT NOT NULL DEFAULT 'user';

	-- Password reset columns
	ALTER TABLE users ADD COLUMN IF NOT EXISTS reset_token TEXT;
	ALTER TABLE users ADD COLUMN IF NOT EXISTS reset_token_expires_at TIMESTAMPTZ;

	-- Interview sessions table
	CREATE TABLE IF NOT EXISTS interview_sessions (
		id              TEXT PRIMARY KEY,
		user_id         UUID REFERENCES users(id) ON DELETE CASCADE,
		job_title       TEXT NOT NULL DEFAULT '',
		job_description TEXT NOT NULL DEFAULT '',
		created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);

	-- Add job_title to existing tables that predate this column
	ALTER TABLE interview_sessions ADD COLUMN IF NOT EXISTS job_title TEXT NOT NULL DEFAULT '';

	-- AI-generated questions per session
	CREATE TABLE IF NOT EXISTS session_questions (
		id           BIGSERIAL PRIMARY KEY,
		session_id   TEXT REFERENCES interview_sessions(id) ON DELETE CASCADE,
		question_idx INT NOT NULL,
		question_text TEXT NOT NULL,
		category     TEXT NOT NULL DEFAULT '',
		skill        TEXT NOT NULL DEFAULT ''
	);

	-- Interview answers table
	CREATE TABLE IF NOT EXISTS interview_answers (
		id             BIGSERIAL PRIMARY KEY,
		session_id     TEXT REFERENCES interview_sessions(id) ON DELETE CASCADE,
		question_id    INT NOT NULL,
		question_text  TEXT NOT NULL,
		category       TEXT NOT NULL DEFAULT '',
		skill          TEXT NOT NULL DEFAULT '',
		transcript     TEXT NOT NULL DEFAULT '',
		score          INT NOT NULL DEFAULT 0,
		clarity        INT NOT NULL DEFAULT 0,
		technical_score INT NOT NULL DEFAULT 0,
		communication  INT NOT NULL DEFAULT 0,
		star           TEXT NOT NULL DEFAULT '',
		summary        TEXT NOT NULL DEFAULT '',
		strengths      JSONB NOT NULL DEFAULT '[]',
		weaknesses     JSONB NOT NULL DEFAULT '[]',
		sample_answer  TEXT NOT NULL DEFAULT '',
		follow_up      TEXT NOT NULL DEFAULT '',
		filler_words   JSONB NOT NULL DEFAULT '{}',
		answered_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);

	CREATE INDEX IF NOT EXISTS idx_users_email       ON users(email);
	CREATE INDEX IF NOT EXISTS idx_sessions_user     ON interview_sessions(user_id);
	CREATE INDEX IF NOT EXISTS idx_questions_session ON session_questions(session_id);
	CREATE INDEX IF NOT EXISTS idx_answers_session   ON interview_answers(session_id);

	-- User-provided API keys (encrypted at rest)
	CREATE TABLE IF NOT EXISTS user_api_keys (
		id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		provider     TEXT NOT NULL,          -- 'gemini' | 'openai' | 'anthropic' | 'aws'
		key_hint     TEXT NOT NULL DEFAULT '', -- last 4 chars shown to user
		encrypted_key TEXT NOT NULL,          -- AES-256-GCM encrypted
		is_active    BOOLEAN NOT NULL DEFAULT FALSE,
		status       TEXT NOT NULL DEFAULT 'untested', -- 'untested' | 'valid' | 'invalid'
		created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		UNIQUE(user_id, provider)
	);

	CREATE INDEX IF NOT EXISTS idx_apikeys_user ON user_api_keys(user_id);

	-- Pending email verifications (pre-activation, not yet real users)
	CREATE TABLE IF NOT EXISTS email_verifications (
		id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		email        TEXT NOT NULL,
		name         TEXT NOT NULL DEFAULT '',
		password_hash TEXT NOT NULL,
		code_hash    TEXT NOT NULL,
		attempts     INT NOT NULL DEFAULT 0,
		verified     BOOLEAN NOT NULL DEFAULT FALSE,
		expires_at   TIMESTAMPTZ NOT NULL,
		created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		UNIQUE(email)
	);

	ALTER TABLE email_verifications ADD COLUMN IF NOT EXISTS verified BOOLEAN NOT NULL DEFAULT FALSE;

	CREATE INDEX IF NOT EXISTS idx_email_verif_email ON email_verifications(email);

	-- Guest free trials
	CREATE TABLE IF NOT EXISTS trials (
		id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		ip_address     TEXT NOT NULL,
		tries_remaining INT NOT NULL DEFAULT 3,
		created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);

	CREATE UNIQUE INDEX IF NOT EXISTS idx_trials_ip ON trials(ip_address);
	`

	_, err := db.Exec(schema)
	return err
}
