// Package routines is the only code that calls the Claude Code routine fire API (experimental),
// so a change in its shape is a one-file fix.
package routines

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	APIVersion = "2026-10-21"
	MaxText    = 65_536 // characters in the fire body's text
)

// Result is the fire response. The call returns once the session exists; it doesn't wait for the work.
type Result struct {
	Type       string `json:"type"`
	SessionID  string `json:"claude_code_session_id"`
	SessionURL string `json:"claude_code_session_url"`
}

// RateLimitError is a 429. RetryAfter is from the Retry-After header (0 if absent).
type RateLimitError struct{ RetryAfter time.Duration }

func (e *RateLimitError) Error() string { return "routine rate limited" }

// HTTPError is any other non-2xx answer. Message is a short, secret-free summary for the task's error field.
type HTTPError struct {
	Status  int
	Message string
}

func (e *HTTPError) Error() string { return e.Message }

type Client struct {
	HTTP *http.Client
	now  func() time.Time
}

func New() *Client { return &Client{HTTP: &http.Client{Timeout: 30 * time.Second}, now: time.Now} }

// Fire starts one routine session. There is no idempotency key: every successful call is a new session.
func (c *Client) Fire(ctx context.Context, routineURL, token, text string) (Result, error) {
	body, _ := json.Marshal(map[string]string{"text": text})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, routineURL, bytes.NewReader(body))
	if err != nil {
		return Result{}, errors.New("invalid routine URL")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("anthropic-version", APIVersion)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		// Don't wrap: the error text includes the URL. The request may or may not have reached the API.
		var timeout interface{ Timeout() bool }
		if errors.As(err, &timeout) && timeout.Timeout() {
			return Result{}, errors.New("routine call timed out; check claude.ai for a session before retrying")
		}
		return Result{}, errors.New("couldn't reach the routine API")
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))

	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		return Result{}, &RateLimitError{RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"), c.now())}
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return Result{}, &HTTPError{Status: resp.StatusCode, Message: fmt.Sprintf("routine API answered %d%s", resp.StatusCode, apiMessage(raw))}
	}
	var r Result
	if err := json.Unmarshal(raw, &r); err != nil {
		return Result{}, errors.New("routine API returned an unexpected response")
	}
	return r, nil
}

// apiMessage extracts the API's error message ({"error":{"message":...}}), shortened. It never contains our token.
func apiMessage(raw []byte) string {
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &e) != nil || e.Error.Message == "" {
		return ""
	}
	m := strings.TrimSpace(e.Error.Message)
	if r := []rune(m); len(r) > 200 {
		m = string(r[:200]) + "…"
	}
	return ": " + m
}

func parseRetryAfter(v string, now time.Time) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if n, err := strconv.Atoi(v); err == nil && n >= 0 {
		return time.Duration(n) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil && t.After(now) {
		return t.Sub(now)
	}
	return 0
}

// RoutineID returns the <id> in .../routines/<id>/fire, used to key per-routine limits.
func RoutineID(routineURL string) string {
	parts := strings.Split(strings.TrimSuffix(routineURL, "/"), "/")
	for i := len(parts) - 2; i > 0; i-- {
		if parts[i-1] == "routines" {
			return parts[i]
		}
	}
	return routineURL
}
