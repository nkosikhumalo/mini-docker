package models

import "time"

type User struct {
	ID                  string     `db:"id"                      json:"id"`
	Email               string     `db:"email"                   json:"email"`
	Name                string     `db:"name"                    json:"name"`
	PasswordHash        *string    `db:"password_hash"           json:"-"`
	Provider            string     `db:"provider"                json:"provider"`
	ProviderID          *string    `db:"provider_id"             json:"-"`
	Plan                string     `db:"plan"                    json:"plan"`
	FreeSessionsUsed    int        `db:"free_sessions_used"      json:"freeSessionsUsed"`
	Role                string     `db:"role"                    json:"role"`
	ResetToken          *string    `db:"reset_token"             json:"-"`
	ResetTokenExpiresAt *time.Time `db:"reset_token_expires_at"  json:"-"`
	CreatedAt           time.Time  `db:"created_at"              json:"createdAt"`
	UpdatedAt           time.Time  `db:"updated_at"              json:"updatedAt"`
}

// PublicUser is what we return in API responses.
type PublicUser struct {
	ID               string    `json:"id"`
	Email            string    `json:"email"`
	Name             string    `json:"name"`
	Provider         string    `json:"provider"`
	Plan             string    `json:"plan"`
	FreeSessionsUsed int       `json:"freeSessionsUsed"`
	Role             string    `json:"role"`
	CreatedAt        time.Time `json:"createdAt"`
}

func (u *User) ToPublic() PublicUser {
	return PublicUser{
		ID:               u.ID,
		Email:            u.Email,
		Name:             u.Name,
		Provider:         u.Provider,
		Plan:             u.Plan,
		FreeSessionsUsed: u.FreeSessionsUsed,
		Role:             u.Role,
		CreatedAt:        u.CreatedAt,
	}
}

func (u *User) IsAdmin() bool {
	return u.Role == "admin"
}
