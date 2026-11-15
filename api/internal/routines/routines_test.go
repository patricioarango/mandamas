package routines

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testToken = "sk-ant-oat01-testtoken"

func TestFireSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer "+testToken ||
			r.Header.Get("anthropic-version") != APIVersion || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("bad request: %s %v", r.Method, r.Header)
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["text"] != "hello" || len(body) != 1 {
			t.Errorf("body = %v", body)
		}
		w.Write([]byte(`{"type":"routine_fire","claude_code_session_id":"s1","claude_code_session_url":"https://claude.ai/code/s1"}`))
	}))
	defer srv.Close()
	res, err := New().Fire(context.Background(), srv.URL+"/v1/claude_code/routines/r1/fire", testToken, "hello")
	if err != nil || res.SessionURL != "https://claude.ai/code/s1" || res.SessionID != "s1" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestFireRateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(429)
	}))
	defer srv.Close()
	_, err := New().Fire(context.Background(), srv.URL, testToken, "x")
	var rl *RateLimitError
	if !errors.As(err, &rl) || rl.RetryAfter != 2*time.Minute {
		t.Fatalf("err = %v", err)
	}
}

func TestFireErrorHasNoSecrets(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte(`{"type":"error","error":{"type":"authentication_error","message":"invalid token"}}`))
	}))
	defer srv.Close()
	_, err := New().Fire(context.Background(), srv.URL+"/secret-path", testToken, "x")
	var he *HTTPError
	if !errors.As(err, &he) || he.Status != 401 || !strings.Contains(err.Error(), "invalid token") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), testToken) || strings.Contains(err.Error(), "secret-path") {
		t.Fatalf("error leaks secrets: %v", err)
	}

	_, err = New().Fire(context.Background(), "http://127.0.0.1:1/secret-path", testToken, "x")
	if err == nil || strings.Contains(err.Error(), "secret-path") {
		t.Fatalf("network error leaks URL: %v", err)
	}
}

func TestRoutineID(t *testing.T) {
	if got := RoutineID("https://api.anthropic.com/v1/claude_code/routines/trig_01abc/fire"); got != "trig_01abc" {
		t.Fatal(got)
	}
}

func TestLimiter(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	l := NewLimiter(2, 3)
	l.now = func() time.Time { return now }
	l.Record("a")
	now = now.Add(10 * time.Minute)
	l.Record("a")
	if w := l.Wait("a"); w != 50*time.Minute {
		t.Fatalf("per-routine wait = %v", w)
	}
	if w := l.Wait("b"); w != 0 {
		t.Fatalf("b wait = %v", w)
	}
	l.Record("b")
	if w := l.Wait("c"); w != 50*time.Minute {
		t.Fatalf("account wait = %v", w)
	}
	now = now.Add(51 * time.Minute)
	if w := l.Wait("a"); w != 0 {
		t.Fatalf("after window wait = %v", w)
	}
}
