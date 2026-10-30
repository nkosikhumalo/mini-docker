// OAuth 2.0 — Google and Microsoft only.

package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/endpoints"

	"foxvue-api/db"
)

type oauthHandler struct {
	users *db.UserRepo
}

func newOAuthHandler(users *db.UserRepo) *oauthHandler {
	return &oauthHandler{users: users}
}

// ── provider configs ──────────────────────────────────────────────────────────

func googleConfig() *oauth2.Config {
	return &oauth2.Config{
		ClientID:     os.Getenv("GOOGLE_CLIENT_ID"),
		ClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
		RedirectURL:  os.Getenv("OAUTH_REDIRECT_BASE") + "/auth/google/callback",
		Scopes:       []string{"openid", "email", "profile"},
		Endpoint:     endpoints.Google,
	}
}

func microsoftConfig() *oauth2.Config {
	tenant := os.Getenv("MICROSOFT_TENANT")
	if tenant == "" {
		tenant = "common"
	}
	return &oauth2.Config{
		ClientID:     os.Getenv("MICROSOFT_CLIENT_ID"),
		ClientSecret: os.Getenv("MICROSOFT_CLIENT_SECRET"),
		RedirectURL:  os.Getenv("OAUTH_REDIRECT_BASE") + "/auth/microsoft/callback",
		Scopes:       []string{"openid", "email", "profile"},
		Endpoint: oauth2.Endpoint{
			AuthURL:  fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/authorize", tenant),
			TokenURL: fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/token", tenant),
		},
	}
}

func configForProvider(provider string) (*oauth2.Config, error) {
	switch provider {
	case "google":
		return googleConfig(), nil
	case "microsoft":
		return microsoftConfig(), nil
	default:
		return nil, fmt.Errorf("unsupported provider: %s", provider)
	}
}

func newState() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ── user info fetchers ────────────────────────────────────────────────────────

type oauthUser struct {
	ID    string
	Email string
	Name  string
}

func fetchGoogleUser(token *oauth2.Token) (*oauthUser, error) {
	client := oauth2.NewClient(context.Background(), oauth2.StaticTokenSource(token))
	resp, err := client.Get("https://www.googleapis.com/oauth2/v3/userinfo")
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	var data struct {
		Sub   string `json:"sub"`
		Email string `json:"email"`
		Name  string `json:"name"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, err
	}
	return &oauthUser{ID: data.Sub, Email: data.Email, Name: data.Name}, nil
}

func fetchMicrosoftUser(token *oauth2.Token) (*oauthUser, error) {
	client := oauth2.NewClient(context.Background(), oauth2.StaticTokenSource(token))
	resp, err := client.Get("https://graph.microsoft.com/v1.0/me")
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	var data struct {
		ID                string `json:"id"`
		Mail              string `json:"mail"`
		UserPrincipalName string `json:"userPrincipalName"`
		DisplayName       string `json:"displayName"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, err
	}
	email := data.Mail
	if email == "" {
		email = data.UserPrincipalName
	}
	return &oauthUser{ID: data.ID, Email: email, Name: data.DisplayName}, nil
}

// ── handlers ──────────────────────────────────────────────────────────────────

// GET /auth/:provider
func (h *oauthHandler) redirect(c *gin.Context) {
	provider := c.Param("provider")
	cfg, err := configForProvider(provider)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	state := newState()
	c.SetCookie("oauth_state", state, 600, "/", "", false, true)
	c.Redirect(http.StatusTemporaryRedirect, cfg.AuthCodeURL(state, oauth2.AccessTypeOnline))
}

// GET /auth/:provider/callback
func (h *oauthHandler) callback(c *gin.Context) {
	provider := c.Param("provider")
	frontendURL := os.Getenv("FRONTEND_URL")
	if frontendURL == "" {
		frontendURL = "http://localhost:5173"
	}

	stateCookie, err := c.Cookie("oauth_state")
	if err != nil || stateCookie != c.Query("state") {
		c.Redirect(http.StatusTemporaryRedirect, frontendURL+"/login?error=state_mismatch")
		return
	}
	c.SetCookie("oauth_state", "", -1, "/", "", false, true)

	cfg, err := configForProvider(provider)
	if err != nil {
		c.Redirect(http.StatusTemporaryRedirect, frontendURL+"/login?error=unknown_provider")
		return
	}

	oauthToken, err := cfg.Exchange(context.Background(), c.Query("code"))
	if err != nil {
		c.Redirect(http.StatusTemporaryRedirect, frontendURL+"/login?error=token_exchange")
		return
	}

	var oauthUsr *oauthUser
	switch provider {
	case "google":
		oauthUsr, err = fetchGoogleUser(oauthToken)
	case "microsoft":
		oauthUsr, err = fetchMicrosoftUser(oauthToken)
	}
	if err != nil || oauthUsr == nil {
		c.Redirect(http.StatusTemporaryRedirect, frontendURL+"/login?error=user_fetch")
		return
	}

	dbUser, err := h.users.UpsertOAuth(oauthUsr.Email, oauthUsr.Name, provider, oauthUsr.ID)
	if err != nil {
		c.Redirect(http.StatusTemporaryRedirect, frontendURL+"/login?error=db")
		return
	}

	jwtToken, err := signJWT(dbUser.ID, dbUser.Email, dbUser.Name, dbUser.Role)
	if err != nil {
		c.Redirect(http.StatusTemporaryRedirect, frontendURL+"/login?error=jwt")
		return
	}

	c.Redirect(http.StatusTemporaryRedirect,
		fmt.Sprintf("%s/auth/callback?token=%s&name=%s", frontendURL, jwtToken, dbUser.Name))
}
