// JWT middleware for all protected routes.

package api

import (
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// RequireAuth validates the Authorization: Bearer <token> header.
// On success it sets "userID", "email", and "name" in the Gin context.
// Used on all protected routes.
func RequireAuth(c *gin.Context) {
	claims, err := parseBearer(c)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}
	c.Set("userID", claims["sub"])
	c.Set("email", claims["email"])
	c.Set("name", claims["name"])
	c.Set("role", claims["role"])
	c.Next()
}

// RequireInterviewAuth is a stricter middleware for interview routes.
// In addition to JWT validation it ensures the token carries a real user ID
// (not a guest session) so interview data is always tied to an account.
func RequireInterviewAuth(c *gin.Context) {
	claims, err := parseBearer(c)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	uid, _ := claims["sub"].(string)
	if uid == "" {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"error": "Interview routes require a registered account.",
		})
		return
	}

	c.Set("userID", uid)
	c.Set("email", claims["email"])
	c.Set("name", claims["name"])
	c.Set("role", claims["role"])
	c.Next()
}

// parseBearer extracts and validates the JWT from the Authorization header.
func parseBearer(c *gin.Context) (jwt.MapClaims, error) {
	header := c.GetHeader("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		return nil, jwt.ErrTokenMalformed
	}

	tokenStr := strings.TrimPrefix(header, "Bearer ")
	secret := jwtSecret()

	token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, jwt.ErrSignatureInvalid
		}
		return []byte(secret), nil
	}, jwt.WithValidMethods([]string{"HS256"}))

	if err != nil || !token.Valid {
		return nil, jwt.ErrTokenSignatureInvalid
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, jwt.ErrTokenInvalidClaims
	}
	return claims, nil
}

func jwtSecret() string {
	s := os.Getenv("JWT_SECRET")
	if s == "" {
		panic("JWT_SECRET environment variable is not set")
	}
	return s
}
