package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mandamas/api/internal/registry"
	"github.com/mandamas/api/internal/store"
)

const apiKey = "0123456789abcdef0123456789abcdef"

type memStore struct {
	mu    sync.Mutex
	tasks map[string]store.Task
	seq   int
}

func (m *memStore) GetTask(_ context.Context, id string) (store.Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tasks[id]
	if !ok {
		return t, store.ErrNotFound
	}
	return t, nil
}
func (m *memStore) ListTasksBy(_ context.Context, project, status string, limit int) ([]store.Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []store.Task
	for _, t := range m.tasks {
		if (project == "" || t.Project == project) && (status == "" || t.Status == status) {
			out = append(out, t)
		}
	}
	return out, nil
}
func (m *memStore) CreateTask(_ context.Context, t store.Task) (store.Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	t.ID, t.Status, t.CreatedAt = "T"+string(rune('0'+m.seq)), store.StatusQueued, time.Now()
	m.tasks[t.ID] = t
	return t, nil
}
func (m *memStore) ReportFromRoutine(_ context.Context, id string, r store.RoutineReport) (store.Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tasks[id]
	if !ok {
		return t, store.ErrNotFound
	}
	if t.Status == store.StatusQueued || t.Status == store.StatusDone || t.Status == store.StatusFailed {
		return t, store.ErrConflict
	}
	t.Status, t.Error = r.Status, r.Error
	if r.PRURL != "" {
		t.PRURL = r.PRURL
	}
	m.tasks[id] = t
	return t, nil
}
func (m *memStore) AddQuestion(_ context.Context, id, q string, opts []string) (store.Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tasks[id]
	if !ok {
		return t, store.ErrNotFound
	}
	if t.Status != store.StatusDispatched {
		return t, store.ErrConflict
	}
	t.Questions = append(t.Questions, store.Question{Question: q, Options: opts})
	t.Status = store.StatusNeedsInput
	m.tasks[id] = t
	return t, nil
}

type notes struct{ kinds []string }

func (n *notes) Notify(_ context.Context, id, kind, title, body string) { n.kinds = append(n.kinds, kind) }

func setup(t *testing.T) (*sdk.ClientSession, *memStore, *notes) {
	t.Helper()
	ms := &memStore{tasks: map[string]store.Task{
		"run1": {ID: "run1", Project: "billing", Category: "bug", Title: "Running", Status: store.StatusDispatched},
	}}
	reg := registry.New()
	reg.SetProjects(map[string]store.Project{
		"billing": {Key: "billing", Label: "Billing", Categories: []string{"bug", "feature"}},
		"old":     {Key: "old", Label: "Old", Categories: []string{"bug"}, Archived: true},
	})
	reg.SetSecrets(map[string]store.Secrets{"billing": {RoutineURL: "https://x", RoutineToken: "sk-ant-oat01-secret"}})
	n := &notes{}
	srv := NewServer(Options{Store: ms, Projects: reg, Notifier: n, Version: "test"})
	ctx := context.Background()
	ct, st := sdk.NewInMemoryTransports()
	if _, err := srv.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs, ms, n
}

func call(t *testing.T, cs *sdk.ClientSession, name string, args map[string]any) (*sdk.CallToolResult, string) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var text string
	for _, c := range res.Content {
		if tc, ok := c.(*sdk.TextContent); ok {
			text += tc.Text
		}
	}
	return res, text
}

func TestListProjectsHidesSecretsAndArchived(t *testing.T) {
	cs, _, _ := setup(t)
	res, text := call(t, cs, "list_projects", map[string]any{})
	if res.IsError || !strings.Contains(text, `"billing"`) || strings.Contains(text, `"old"`) ||
		strings.Contains(text, "sk-ant") || strings.Contains(text, "https://x") || !strings.Contains(text, `"ready":true`) {
		t.Fatalf("list_projects = %s", text)
	}
}

func TestCreateTask(t *testing.T) {
	cs, ms, _ := setup(t)
	res, text := call(t, cs, "create_task", map[string]any{"project": "billing", "category": "Feature", "title": " Add export ", "description": "CSV"})
	if res.IsError {
		t.Fatal(text)
	}
	var out TaskView
	b, _ := json.Marshal(res.StructuredContent)
	_ = json.Unmarshal(b, &out)
	got := ms.tasks[out.ID]
	if got.Status != store.StatusQueued || got.Category != "feature" || got.Title != "Add export" || got.CreatedBy != "mcp" {
		t.Fatalf("task = %+v", got)
	}

	for _, args := range []map[string]any{
		{"project": "old", "category": "bug", "title": "x"},
		{"project": "nope", "category": "bug", "title": "x"},
		{"project": "billing", "category": "ui", "title": "x"},
		{"project": "billing", "category": "bug", "title": "  "},
	} {
		if res, _ := call(t, cs, "create_task", args); !res.IsError {
			t.Fatalf("expected error for %v", args)
		}
	}
}

func TestUpdateTask(t *testing.T) {
	cs, ms, n := setup(t)
	if res, _ := call(t, cs, "update_task", map[string]any{"taskId": "run1", "status": "pr_open"}); !res.IsError {
		t.Fatal("pr_open without prUrl should fail")
	}
	if res, _ := call(t, cs, "update_task", map[string]any{"taskId": "run1", "status": "queued"}); !res.IsError {
		t.Fatal("status queued should be rejected")
	}
	// project/category are not accepted at all.
	if res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: "update_task",
		Arguments: map[string]any{"taskId": "run1", "status": "done", "project": "other"}}); err == nil && !res.IsError {
		t.Fatal("unknown field project should be rejected")
	}
	if ms.tasks["run1"].Project != "billing" {
		t.Fatal("project changed")
	}
	res, text := call(t, cs, "update_task", map[string]any{"taskId": "run1", "status": "pr_open", "prUrl": "https://github.com/o/r/pull/1"})
	if res.IsError || ms.tasks["run1"].PRURL != "https://github.com/o/r/pull/1" || ms.tasks["run1"].Status != "pr_open" {
		t.Fatal(text)
	}
	if len(n.kinds) != 1 || n.kinds[0] != "pr_open" {
		t.Fatalf("pushes = %v", n.kinds)
	}
	if res, _ := call(t, cs, "update_task", map[string]any{"taskId": "nope", "status": "done"}); !res.IsError {
		t.Fatal("unknown task should fail")
	}
}

func TestRequestInput(t *testing.T) {
	cs, ms, n := setup(t)
	res, text := call(t, cs, "request_input", map[string]any{"taskId": "run1", "question": "Which DB?", "options": []string{"pg", "sqlite"}})
	if res.IsError || ms.tasks["run1"].Status != store.StatusNeedsInput || !strings.Contains(text, "end this session") {
		t.Fatal(text)
	}
	if len(n.kinds) != 1 || n.kinds[0] != "question" {
		t.Fatalf("pushes = %v", n.kinds)
	}
	if res, _ := call(t, cs, "request_input", map[string]any{"taskId": "run1", "question": "Again?"}); !res.IsError {
		t.Fatal("second question while waiting should fail")
	}
}

func TestGetTaskStatus(t *testing.T) {
	cs, _, _ := setup(t)
	res, text := call(t, cs, "get_task_status", map[string]any{"taskId": "run1"})
	if res.IsError || !strings.Contains(text, `"status":"dispatched"`) {
		t.Fatal(text)
	}
	if res, _ := call(t, cs, "get_task_status", map[string]any{"status": "bogus"}); !res.IsError {
		t.Fatal("bad status should fail")
	}
}

func TestHandlerAuth(t *testing.T) {
	ms := &memStore{tasks: map[string]store.Task{}}
	reg := registry.New()
	reg.SetProjects(map[string]store.Project{})
	reg.SetSecrets(map[string]store.Secrets{})
	h := Handler(Options{Store: ms, Projects: reg, Notifier: &notes{}, APIKey: apiKey, Version: "test"})
	mux := http.NewServeMux()
	mux.Handle("/mcp", h)
	mux.Handle("/mcp/", h)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	init := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`
	post := func(path, auth string) int {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+path, strings.NewReader(init))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if c := post("/mcp", ""); c != 401 {
		t.Fatalf("no key: %d", c)
	}
	if c := post("/mcp", "Bearer wrong"); c != 401 {
		t.Fatalf("wrong key: %d", c)
	}
	if c := post("/mcp/wrong", ""); c != 401 {
		t.Fatalf("wrong path key: %d", c)
	}
	if c := post("/mcp", "Bearer "+apiKey); c != 200 {
		t.Fatalf("bearer: %d", c)
	}
	if c := post("/mcp/"+apiKey, ""); c != 200 {
		t.Fatalf("path key: %d", c)
	}

	// Full client round trip over HTTP.
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "t"}, nil).Connect(context.Background(),
		&sdk.StreamableClientTransport{Endpoint: srv.URL + "/mcp/" + apiKey}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	tl, err := cs.ListTools(context.Background(), nil)
	if err != nil || len(tl.Tools) != 5 {
		t.Fatalf("tools: %v %v", tl, err)
	}
}
