package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	fbauth "firebase.google.com/go/v4/auth"
	"github.com/gin-gonic/gin"

	"github.com/mandamas/api/internal/allowlist"
	"github.com/mandamas/api/internal/store"
)

type okVerifier struct{}

func (okVerifier) VerifyIDToken(_ context.Context, tok string) (*fbauth.Token, error) {
	if tok != "good" {
		return nil, errors.New("bad")
	}
	return &fbauth.Token{UID: "u1", Claims: map[string]any{"email": "owner@example.com", "email_verified": true}}, nil
}

const (
	goodURL   = "https://api.anthropic.com/v1/claude_code/routines/trig_1/fire"
	goodToken = "sk-ant-oat01-SECRETVALUE"
)

func init() {
	gin.SetMode(gin.TestMode)
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil))) // keep test output readable
}

func setup(t *testing.T) (*gin.Engine, *memStore) {
	t.Helper()
	app := t.TempDir()
	_ = os.WriteFile(filepath.Join(app, "index.html"), []byte("<!doctype html>INDEX"), 0o644)
	_ = os.WriteFile(filepath.Join(app, "sw.js"), []byte("SW"), 0o644)
	m := newMemStore()
	r := NewRouter(Options{Verifier: okVerifier{}, Allowlist: allowlist.NewStatic([]string{"owner@example.com"}),
		Store: m, AppDir: app, MaxBody: 64 << 10, RatePerSecond: 1000, RateBurst: 1000})
	return r, m
}

func do(r *gin.Engine, method, path, body string, hdr ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer good")
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func projectJSON(over map[string]any) string {
	m := map[string]any{"key": "billing-api", "label": "Billing API", "description": "Payments", "color": "#3b6ff5",
		"logo": "", "categories": []string{"bug", "feature"}, "routineUrl": goodURL, "routineToken": goodToken}
	for k, v := range over {
		if v == nil {
			delete(m, k)
		} else {
			m[k] = v
		}
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func TestProjectCreateValidation(t *testing.T) {
	r, _ := setup(t)
	cases := []struct {
		name string
		body string
		ct   string
		want int
	}{
		{"bad key", projectJSON(map[string]any{"key": "Bad Key"}), "", 400},
		{"bad color", projectJSON(map[string]any{"color": "red"}), "", 400},
		{"bad logo", projectJSON(map[string]any{"logo": "https://x/y.png"}), "", 400},
		{"no categories", projectJSON(map[string]any{"categories": []string{}}), "", 400},
		{"bad url", projectJSON(map[string]any{"routineUrl": "https://evil.example/fire"}), "", 400},
		{"bad token", projectJSON(map[string]any{"routineToken": "nope"}), "", 400},
		{"missing secrets", projectJSON(map[string]any{"routineUrl": "", "routineToken": ""}), "", 400},
		{"unknown field", projectJSON(map[string]any{"archived": true}), "", 400},
		{"not json", projectJSON(nil), "text/plain", 415},
		{"too large", projectJSON(map[string]any{"description": strings.Repeat("x", 70<<10)}), "", 413},
		{"ok", projectJSON(nil), "", 201},
		{"existing key", projectJSON(nil), "", 409},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var w *httptest.ResponseRecorder
			if tc.ct != "" {
				w = do(r, "POST", "/api/projects", tc.body, "Content-Type", tc.ct)
			} else {
				w = do(r, "POST", "/api/projects", tc.body)
			}
			if w.Code != tc.want {
				t.Fatalf("got %d want %d: %s", w.Code, tc.want, w.Body.String())
			}
		})
	}
}

func TestSecretsNeverReturned(t *testing.T) {
	r, m := setup(t)
	for _, w := range []*httptest.ResponseRecorder{
		do(r, "POST", "/api/projects", projectJSON(nil)),
		do(r, "GET", "/api/projects", ""),
		do(r, "GET", "/api/projects/billing-api", ""),
		do(r, "PATCH", "/api/projects/billing-api", projectJSON(map[string]any{"label": "Billing", "routineToken": "sk-ant-oat01-NEWSECRET"})),
	} {
		if w.Code >= 300 {
			t.Fatalf("status %d: %s", w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "SECRET") || strings.Contains(w.Body.String(), "routineUrl") {
			t.Fatalf("secret leaked: %s", w.Body.String())
		}
		if !strings.Contains(w.Body.String(), `"hasSecrets":true`) {
			t.Fatalf("hasSecrets missing: %s", w.Body.String())
		}
	}
	if m.secrets["billing-api"].RoutineToken != "sk-ant-oat01-NEWSECRET" {
		t.Fatal("patch with secrets must replace them")
	}
	// Patch without secrets keeps them.
	w := do(r, "PATCH", "/api/projects/billing-api", projectJSON(map[string]any{"routineUrl": "", "routineToken": ""}))
	if w.Code != 200 || m.secrets["billing-api"].RoutineToken != "sk-ant-oat01-NEWSECRET" {
		t.Fatalf("patch without secrets must keep them: %d", w.Code)
	}
	// Only one of the two is an error.
	if w := do(r, "PATCH", "/api/projects/billing-api", projectJSON(map[string]any{"routineUrl": ""})); w.Code != 400 {
		t.Fatalf("half secrets: got %d", w.Code)
	}
	if w := do(r, "PATCH", "/api/projects/billing-api", projectJSON(map[string]any{"key": "other"})); w.Code != 400 {
		t.Fatalf("key change: got %d", w.Code)
	}
}

func TestTasks(t *testing.T) {
	r, m := setup(t)
	do(r, "POST", "/api/projects", projectJSON(nil))
	task := func(over string) string {
		return `{"project":"billing-api","category":"bug","title":"Fix it","description":""` + over + `}`
	}
	if w := do(r, "POST", "/api/tasks", task(`,"status":"done"`)); w.Code != 400 {
		t.Fatalf("unknown field status: %d", w.Code)
	}
	if w := do(r, "POST", "/api/tasks", strings.Replace(task(""), `"bug"`, `"ui"`, 1)); w.Code != 400 {
		t.Fatalf("category not enabled: %d", w.Code)
	}
	if w := do(r, "POST", "/api/tasks", strings.Replace(task(""), `"Fix it"`, `"  "`, 1)); w.Code != 400 {
		t.Fatalf("empty title: %d", w.Code)
	}
	if w := do(r, "POST", "/api/tasks", strings.Replace(task(""), "billing-api", "nope", 1)); w.Code != 404 {
		t.Fatalf("unknown project: %d", w.Code)
	}
	w := do(r, "POST", "/api/tasks", strings.Replace(task(""), `"bug"`, `"BUG"`, 1))
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var created store.Task
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	if created.Status != "queued" || created.CreatedBy != "owner@example.com" || created.Category != "bug" {
		t.Fatalf("server must set status/createdBy and clean category: %+v", created)
	}

	// Answering only works while Claude is waiting.
	if w := do(r, "POST", "/api/tasks/"+created.ID+"/answer", `{"answer":"A"}`); w.Code != 409 {
		t.Fatalf("answer when queued: %d", w.Code)
	}
	tk := m.tasks[created.ID]
	tk.Status, tk.Questions = "needs_input", []store.Question{{Question: "A or B?"}}
	m.tasks[created.ID] = tk
	if w := do(r, "POST", "/api/tasks/"+created.ID+"/answer", `{"answer":"  "}`); w.Code != 400 {
		t.Fatalf("empty answer: %d", w.Code)
	}
	if w := do(r, "POST", "/api/tasks/"+created.ID+"/answer", `{"answer":"A"}`); w.Code != 200 {
		t.Fatalf("answer: %d", w.Code)
	}
	if m.tasks[created.ID].Status != "queued" {
		t.Fatal("answer must re-queue")
	}
	if w := do(r, "POST", "/api/tasks/nope/answer", `{"answer":"A"}`); w.Code != 404 {
		t.Fatalf("unknown task: %d", w.Code)
	}

	// Archived projects take no new tasks.
	do(r, "POST", "/api/projects/billing-api/archive", `{}`)
	if w := do(r, "POST", "/api/tasks", task("")); w.Code != 409 {
		t.Fatalf("archived project: %d", w.Code)
	}
}

func TestAuthAndStatic(t *testing.T) {
	r, _ := setup(t)
	req := httptest.NewRequest("GET", "/api/projects", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatalf("no token: %d", w.Code)
	}
	if w := do(r, "GET", "/api/me", ""); w.Code != 200 || !strings.Contains(w.Body.String(), "owner@example.com") {
		t.Fatalf("me: %d %s", w.Code, w.Body.String())
	}
	if w := do(r, "GET", "/api/projects/Bad_Key", ""); w.Code != 400 {
		t.Fatalf("bad key param: %d", w.Code)
	}
	if w := do(r, "GET", "/api/nope", ""); w.Code != 404 || !strings.Contains(w.Body.String(), "error") {
		t.Fatalf("unknown api route must be JSON 404, got %d %s", w.Code, w.Body.String())
	}
	for _, path := range []string{"/../../etc/passwd", "/..%2f..%2fetc/passwd", "/icons/../../go.mod"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if strings.Contains(w.Body.String(), "root:") || strings.Contains(w.Body.String(), "module ") {
			t.Fatalf("%s served a file outside the app folder", path)
		}
	}
	for path, want := range map[string]string{"/": "INDEX", "/p/billing-api": "INDEX", "/sw.js": "SW"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 || !strings.Contains(w.Body.String(), want) {
			t.Fatalf("%s: %d %q", path, w.Code, w.Body.String())
		}
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/sw.js", nil))
	if w.Header().Get("Cache-Control") != "no-cache" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("headers: %v", w.Header())
	}
}

func TestRateLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := NewRouter(Options{Verifier: okVerifier{}, Allowlist: allowlist.NewStatic([]string{"owner@example.com"}),
		Store: newMemStore(), RatePerSecond: 0.001, RateBurst: 2})
	codes := []int{}
	for i := 0; i < 3; i++ {
		codes = append(codes, do(r, "GET", "/api/me", "").Code)
	}
	if codes[2] != http.StatusTooManyRequests {
		t.Fatalf("want 429 on 3rd request, got %v", codes)
	}
}
