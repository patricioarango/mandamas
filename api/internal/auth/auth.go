// Package auth verifies Firebase ID tokens and the email allowlist for every /api request.
package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	fbauth "firebase.google.com/go/v4/auth"
	"github.com/gin-gonic/gin"

	"github.com/mandamas/api/internal/allowlist"
)

// Verifier is the part of the Firebase Auth client we use (fake in tests).
type Verifier interface {
	VerifyIDToken(ctx context.Context, idToken string) (*fbauth.Token, error)
}

type User struct {
	UID   string `json:"uid"`
	Email string `json:"email"`
	Name  string `json:"name"`
}

const ctxKey = "mandamas.user"

// Middleware: Bearer token → VerifyIDToken → email_verified → allowlist.
func Middleware(v Verifier, list allowlist.Checker) gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.GetHeader("Authorization")
		raw, ok := strings.CutPrefix(h, "Bearer ")
		if !ok || strings.TrimSpace(raw) == "" {
			abort(c, http.StatusUnauthorized, "sign in required")
			return
		}
		tok, err := v.VerifyIDToken(c.Request.Context(), strings.TrimSpace(raw))
		if err != nil {
			abort(c, http.StatusUnauthorized, "invalid or expired session")
			return
		}
		email, _ := tok.Claims["email"].(string)
		verified, _ := tok.Claims["email_verified"].(bool)
		name, _ := tok.Claims["name"].(string)
		if email == "" || !verified {
			abort(c, http.StatusForbidden, "not allowed")
			return
		}
		allowed, err := list.Allowed(c.Request.Context(), email)
		if err != nil {
			slog.Error("allowlist unavailable", "err", err)
			if errors.Is(err, allowlist.ErrNotLoaded) {
				abort(c, http.StatusServiceUnavailable, "allowlist unavailable, try again later")
				return
			}
		}
		if !allowed {
			abort(c, http.StatusForbidden, "not allowed")
			return
		}
		c.Set(ctxKey, User{UID: tok.UID, Email: strings.ToLower(email), Name: name})
		c.Next()
	}
}

// FromContext returns the signed-in user set by Middleware.
func FromContext(c *gin.Context) User {
	u, _ := c.Get(ctxKey)
	user, _ := u.(User)
	return user
}

func abort(c *gin.Context, status int, msg string) {
	c.AbortWithStatusJSON(status, gin.H{"error": msg})
}
