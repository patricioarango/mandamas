package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	fbauth "firebase.google.com/go/v4/auth"
	"github.com/gin-gonic/gin"

	"github.com/mandamas/api/internal/allowlist"
)

type fakeVerifier map[string]map[string]any

func (f fakeVerifier) VerifyIDToken(_ context.Context, tok string) (*fbauth.Token, error) {
	c, ok := f[tok]
	if !ok {
		return nil, errors.New("bad token")
	}
	return &fbauth.Token{UID: "uid-" + tok, Claims: c}, nil
}

type brokenList struct{}

func (brokenList) Allowed(context.Context, string) (bool, error) { return false, allowlist.ErrNotLoaded }

func TestMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	v := fakeVerifier{
		"good":       {"email": "Owner@Example.com", "email_verified": true, "name": "Owner"},
		"unverified": {"email": "owner@example.com", "email_verified": false},
		"stranger":   {"email": "x@example.com", "email_verified": true},
	}
	list := allowlist.NewStatic([]string{"owner@example.com"})

	cases := []struct {
		name, header string
		list         allowlist.Checker
		want         int
	}{
		{"no header", "", list, 401},
		{"not bearer", "Basic abc", list, 401},
		{"bad token", "Bearer nope", list, 401},
		{"unverified email", "Bearer unverified", list, 403},
		{"not on list", "Bearer stranger", list, 403},
		{"allowed, case-insensitive", "Bearer good", list, 200},
		{"allowlist never loaded", "Bearer good", brokenList{}, 503},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := gin.New()
			r.GET("/x", Middleware(v, tc.list), func(c *gin.Context) {
				if u := FromContext(c); u.Email != "owner@example.com" {
					t.Errorf("user email not normalized: %q", u.Email)
				}
				c.Status(200)
			})
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/x", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			r.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Fatalf("got %d want %d (%s)", w.Code, tc.want, w.Body.String())
			}
		})
	}
}
