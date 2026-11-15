// Package mcp is the MCP server (streamable HTTP, stateless) that chat clients and routines use to
// create, list and update tasks. Every request needs the MCP_API_KEY.
package mcp

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/time/rate"

	"github.com/mandamas/api/internal/notifications"
	"github.com/mandamas/api/internal/store"
	"github.com/mandamas/api/internal/validate"
)

const (
	maxQuestion = 2000
	maxOptions  = 6
	maxOption   = 200
	maxError    = 500
	maxPRURL    = 500
	listLimit   = 50
)

type Store interface {
	GetTask(ctx context.Context, id string) (store.Task, error)
	ListTasksBy(ctx context.Context, project, status string, limit int) ([]store.Task, error)
	CreateTask(ctx context.Context, t store.Task) (store.Task, error)
	ReportFromRoutine(ctx context.Context, id string, r store.RoutineReport) (store.Task, error)
	AddQuestion(ctx context.Context, id, question string, options []string) (store.Task, error)
}

type Projects interface {
	Project(key string) (store.Project, bool)
	Projects() []store.Project
}

type Notifier interface {
	Notify(ctx context.Context, taskID, kind, title, body string)
}

type Options struct {
	Store    Store
	Projects Projects
	Notifier Notifier
	APIKey   string
	Version  string
}

// NewServer builds the MCP server with its tools (no transport, no auth).
func NewServer(o Options) *sdk.Server {
	s := sdk.NewServer(&sdk.Implementation{Name: "mandamas", Title: "Mandamás tasks", Version: o.Version}, &sdk.ServerOptions{
		Instructions: "Mandamás holds coding tasks for several repos. Use list_projects to see project keys and categories, " +
			"create_task to add work, get_task_status to check on it. Routines working on a task report back with update_task " +
			"and, only when truly blocked, request_input (then push your branch and end the session).",
	})
	t := &tools{o: o}
	sdk.AddTool(s, &sdk.Tool{Name: "list_projects", Description: "List active projects with their keys, labels and allowed task categories.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true}}, t.listProjects)
	sdk.AddTool(s, &sdk.Tool{Name: "create_task", Description: "Queue a new task for a project. A Claude Code routine picks it up and opens a PR. " +
		"project and category must come from list_projects."}, t.createTask)
	sdk.AddTool(s, &sdk.Tool{Name: "get_task_status", Description: "Get one task by taskId, or list recent tasks filtered by project and/or status " +
		"(queued, dispatching, dispatched, needs_input, pr_open, done, failed).", Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true}}, t.getTaskStatus)
	sdk.AddTool(s, &sdk.Tool{Name: "update_task", Description: "Report progress on the task you are working on: status pr_open (with prUrl) when the PR is open, " +
		"done when finished, failed (with error) if you can't complete it. Can't change the task's project or category."}, t.updateTask)
	sdk.AddTool(s, &sdk.Tool{Name: "request_input", Description: "Ask the user one clear question when you are truly blocked or a choice is costly to reverse. " +
		"BEFORE calling: commit and push your progress to branch task/<id>. AFTER calling: end the session; you will be restarted with the answer. " +
		"Give options when possible."}, t.requestInput)
	return s
}

// Handler serves MCP over streamable HTTP behind API-key auth and a rate limit.
// The key is accepted as "Authorization: Bearer <key>" or, for clients that only take a URL, as /mcp/<key>.
func Handler(o Options) http.Handler {
	srv := NewServer(o)
	h := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return srv }, &sdk.StreamableHTTPOptions{
		Stateless: true, JSONResponse: true, MaxRequestBodyBytes: 512 << 10,
	})
	want := sha256.Sum256([]byte(o.APIKey))
	limiter := rate.NewLimiter(5, 60)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		key, viaPath := requestKey(r)
		got := sha256.Sum256([]byte(key))
		if key == "" || subtle.ConstantTimeCompare(got[:], want[:]) != 1 {
			slog.Warn("mcp: unauthorized", "method", r.Method)
			w.Header().Set("WWW-Authenticate", `Bearer realm="mandamas"`)
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		if !limiter.Allow() {
			http.Error(w, `{"error":"too many requests"}`, http.StatusTooManyRequests)
			return
		}
		if viaPath {
			r = r.Clone(r.Context())
			r.URL.Path, r.URL.RawPath = "/mcp", ""
		}
		w.Header().Set("Cache-Control", "no-store")
		sw := &statusWriter{ResponseWriter: w, status: 200}
		h.ServeHTTP(sw, r)
		slog.Info("mcp request", "method", r.Method, "status", sw.status, "ms", time.Since(start).Milliseconds())
	})
}

func requestKey(r *http.Request) (key string, viaPath bool) {
	if a := r.Header.Get("Authorization"); len(a) > 7 && strings.EqualFold(a[:7], "bearer ") {
		return strings.TrimSpace(a[7:]), false
	}
	if rest, ok := strings.CutPrefix(r.URL.Path, "/mcp/"); ok && rest != "" && !strings.Contains(rest, "/") {
		k, err := url.PathUnescape(rest)
		if err == nil {
			return k, true
		}
	}
	return "", false
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(c int) { s.status = c; s.ResponseWriter.WriteHeader(c) }
func (s *statusWriter) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// ---------- tools ----------

type tools struct{ o Options }

// toolErr returns a tool-level error (isError result) the model can read and act on.
func toolErr[Out any](format string, a ...any) (*sdk.CallToolResult, Out, error) {
	var zero Out
	return &sdk.CallToolResult{IsError: true, Content: []sdk.Content{&sdk.TextContent{Text: fmt.Sprintf(format, a...)}}}, zero, nil
}

func storeErr[Out any](err error, id string) (*sdk.CallToolResult, Out, error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return toolErr[Out]("task %s not found", id)
	case errors.Is(err, store.ErrConflict):
		return toolErr[Out]("task %s isn't in a state that allows this", id)
	}
	slog.Error("mcp: store error", "task", id, "err", err)
	var zero Out
	return nil, zero, errors.New("internal error, try again later")
}

type ProjectInfo struct {
	Key         string   `json:"key"`
	Label       string   `json:"label"`
	Description string   `json:"description,omitempty"`
	Categories  []string `json:"categories"`
	Ready       bool     `json:"ready" jsonschema:"false if the project has no routine configured yet; its tasks would fail"`
}

type ListProjectsIn struct{}
type ListProjectsOut struct {
	Projects []ProjectInfo `json:"projects"`
}

func (t *tools) listProjects(ctx context.Context, _ *sdk.CallToolRequest, _ ListProjectsIn) (*sdk.CallToolResult, ListProjectsOut, error) {
	out := ListProjectsOut{Projects: []ProjectInfo{}}
	for _, p := range t.o.Projects.Projects() {
		out.Projects = append(out.Projects, ProjectInfo{Key: p.Key, Label: p.Label, Description: p.Description, Categories: p.Categories, Ready: p.HasSecrets})
	}
	return nil, out, nil
}

type CreateTaskIn struct {
	Project     string `json:"project" jsonschema:"project key from list_projects"`
	Category    string `json:"category" jsonschema:"one of the project's categories"`
	Title       string `json:"title" jsonschema:"short summary, max 300 characters"`
	Description string `json:"description,omitempty" jsonschema:"full details: what to do, where, acceptance criteria"`
}

func (t *tools) createTask(ctx context.Context, _ *sdk.CallToolRequest, in CreateTaskIn) (*sdk.CallToolResult, TaskView, error) {
	in.Title, in.Description = strings.TrimSpace(in.Title), strings.TrimSpace(in.Description)
	cat := strings.ToLower(strings.TrimSpace(in.Category))
	if err := validate.Text("title", in.Title, 1, validate.MaxTitle); err != nil {
		return toolErr[TaskView]("%v", err)
	}
	if err := validate.Text("description", in.Description, 0, validate.MaxTaskText); err != nil {
		return toolErr[TaskView]("%v", err)
	}
	p, ok := t.o.Projects.Project(in.Project)
	if !ok {
		return toolErr[TaskView]("unknown or archived project %q; call list_projects", in.Project)
	}
	allowed := false
	for _, c := range p.Categories {
		allowed = allowed || c == cat
	}
	if !allowed {
		return toolErr[TaskView]("category %q isn't enabled for %s; use one of: %s", cat, p.Key, strings.Join(p.Categories, ", "))
	}
	task, err := t.o.Store.CreateTask(ctx, store.Task{Project: p.Key, Category: cat, Title: in.Title, Description: in.Description, CreatedBy: "mcp"})
	if err != nil {
		return storeErr[TaskView](err, "")
	}
	slog.Info("mcp: task created", "task", task.ID, "project", p.Key)
	return nil, view(task, true), nil
}

type QuestionView struct {
	Question string   `json:"question"`
	Options  []string `json:"options,omitempty"`
	Answer   string   `json:"answer,omitempty"`
}

type TaskView struct {
	ID          string         `json:"id"`
	Project     string         `json:"project"`
	Category    string         `json:"category"`
	Title       string         `json:"title"`
	Description string         `json:"description,omitempty"`
	Status      string         `json:"status"`
	Branch      string         `json:"branch,omitempty"`
	Questions   []QuestionView `json:"questions,omitempty"`
	SessionURL  string         `json:"sessionUrl,omitempty"`
	PRURL       string         `json:"prUrl,omitempty"`
	Error       string         `json:"error,omitempty"`
	CreatedAt   time.Time      `json:"createdAt"`
	UpdatedAt   time.Time      `json:"updatedAt"`
}

func view(t store.Task, full bool) TaskView {
	v := TaskView{ID: t.ID, Project: t.Project, Category: t.Category, Title: t.Title, Status: t.Status, Branch: t.Branch,
		SessionURL: t.SessionURL, PRURL: t.PRURL, Error: t.Error, CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt}
	if full {
		v.Description = t.Description
		for _, q := range t.Questions {
			v.Questions = append(v.Questions, QuestionView{Question: q.Question, Options: q.Options, Answer: q.Answer})
		}
	}
	return v
}

type GetTaskStatusIn struct {
	TaskID  string `json:"taskId,omitempty" jsonschema:"a task ID; if set, project and status are ignored"`
	Project string `json:"project,omitempty" jsonschema:"filter by project key"`
	Status  string `json:"status,omitempty" jsonschema:"filter by status"`
}
type GetTaskStatusOut struct {
	Tasks []TaskView `json:"tasks"`
}

var statuses = map[string]bool{store.StatusQueued: true, store.StatusDispatching: true, store.StatusDispatched: true,
	store.StatusNeedsInput: true, store.StatusPROpen: true, store.StatusDone: true, store.StatusFailed: true}

func (t *tools) getTaskStatus(ctx context.Context, _ *sdk.CallToolRequest, in GetTaskStatusIn) (*sdk.CallToolResult, GetTaskStatusOut, error) {
	if in.TaskID != "" {
		if err := validate.TaskID(in.TaskID); err != nil {
			return toolErr[GetTaskStatusOut]("%v", err)
		}
		task, err := t.o.Store.GetTask(ctx, in.TaskID)
		if err != nil {
			return storeErr[GetTaskStatusOut](err, in.TaskID)
		}
		return nil, GetTaskStatusOut{Tasks: []TaskView{view(task, true)}}, nil
	}
	if in.Project != "" {
		if err := validate.Key(in.Project); err != nil {
			return toolErr[GetTaskStatusOut]("project: %v", err)
		}
	}
	if in.Status != "" && !statuses[in.Status] {
		return toolErr[GetTaskStatusOut]("unknown status %q", in.Status)
	}
	ts, err := t.o.Store.ListTasksBy(ctx, in.Project, in.Status, listLimit)
	if err != nil {
		return storeErr[GetTaskStatusOut](err, "")
	}
	out := GetTaskStatusOut{Tasks: make([]TaskView, 0, len(ts))}
	for _, task := range ts {
		out.Tasks = append(out.Tasks, view(task, false))
	}
	return nil, out, nil
}

// UpdateTaskIn deliberately has no project or category: routines can't move a task.
type UpdateTaskIn struct {
	TaskID string `json:"taskId" jsonschema:"the task ID you received"`
	Status string `json:"status" jsonschema:"pr_open, done or failed"`
	PRURL  string `json:"prUrl,omitempty" jsonschema:"the pull request URL (required for pr_open)"`
	Error  string `json:"error,omitempty" jsonschema:"short reason when status is failed; no secrets"`
}

func (t *tools) updateTask(ctx context.Context, _ *sdk.CallToolRequest, in UpdateTaskIn) (*sdk.CallToolResult, TaskView, error) {
	if err := validate.TaskID(in.TaskID); err != nil {
		return toolErr[TaskView]("%v", err)
	}
	in.PRURL, in.Error = strings.TrimSpace(in.PRURL), strings.TrimSpace(in.Error)
	switch in.Status {
	case store.StatusPROpen, store.StatusDone, store.StatusFailed:
	default:
		return toolErr[TaskView]("status must be pr_open, done or failed")
	}
	if in.PRURL != "" {
		u, err := url.Parse(in.PRURL)
		if err != nil || u.Scheme != "https" || u.Host == "" || len(in.PRURL) > maxPRURL {
			return toolErr[TaskView]("prUrl must be an https URL")
		}
	}
	if in.Status == store.StatusFailed && in.Error == "" {
		return toolErr[TaskView]("give a short error when status is failed")
	}
	in.Error = notifications.Truncate(in.Error, maxError)
	if in.Status == store.StatusPROpen && in.PRURL == "" {
		cur, err := t.o.Store.GetTask(ctx, in.TaskID)
		if err != nil {
			return storeErr[TaskView](err, in.TaskID)
		}
		if cur.PRURL == "" {
			return toolErr[TaskView]("prUrl is required for pr_open")
		}
	}
	task, err := t.o.Store.ReportFromRoutine(ctx, in.TaskID, store.RoutineReport{Status: in.Status, PRURL: in.PRURL, Error: in.Error})
	if err != nil {
		return storeErr[TaskView](err, in.TaskID)
	}
	slog.Info("mcp: task updated", "task", task.ID, "status", task.Status)
	switch task.Status {
	case store.StatusPROpen:
		t.o.Notifier.Notify(ctx, task.ID, notifications.TypePROpen, "PR ready: "+task.Title, "Claude opened a pull request for review.")
	case store.StatusFailed:
		t.o.Notifier.Notify(ctx, task.ID, notifications.TypeFailed, "Task failed: "+task.Title, task.Error)
	}
	return nil, view(task, false), nil
}

type RequestInputIn struct {
	TaskID   string   `json:"taskId" jsonschema:"the task ID you received"`
	Question string   `json:"question" jsonschema:"one clear question, with the context needed to answer it"`
	Options  []string `json:"options,omitempty" jsonschema:"up to 6 suggested answers"`
}
type RequestInputOut struct {
	Status       string `json:"status"`
	Instructions string `json:"instructions"`
}

func (t *tools) requestInput(ctx context.Context, _ *sdk.CallToolRequest, in RequestInputIn) (*sdk.CallToolResult, RequestInputOut, error) {
	if err := validate.TaskID(in.TaskID); err != nil {
		return toolErr[RequestInputOut]("%v", err)
	}
	in.Question = strings.TrimSpace(in.Question)
	if err := validate.Text("question", in.Question, 1, maxQuestion); err != nil {
		return toolErr[RequestInputOut]("%v", err)
	}
	if len(in.Options) > maxOptions {
		return toolErr[RequestInputOut]("at most %d options", maxOptions)
	}
	opts := make([]string, 0, len(in.Options))
	for _, o := range in.Options {
		if o = strings.TrimSpace(o); o == "" {
			continue
		}
		if err := validate.Text("option", o, 1, maxOption); err != nil {
			return toolErr[RequestInputOut]("%v", err)
		}
		opts = append(opts, o)
	}
	task, err := t.o.Store.AddQuestion(ctx, in.TaskID, in.Question, opts)
	if errors.Is(err, store.ErrConflict) {
		return toolErr[RequestInputOut]("task %s can't take a question now (it isn't running, or a question is already waiting). End the session.", in.TaskID)
	}
	if err != nil {
		return storeErr[RequestInputOut](err, in.TaskID)
	}
	slog.Info("mcp: question asked", "task", task.ID, "count", len(task.Questions))
	t.o.Notifier.Notify(ctx, task.ID, notifications.TypeQuestion, "Claude needs input: "+task.Title, in.Question)
	return nil, RequestInputOut{
		Status: store.StatusNeedsInput,
		Instructions: fmt.Sprintf("Question saved and the user was notified. Make sure all progress is committed and pushed to branch task/%s, "+
			"then end this session now. Do not wait for the answer: a new session will start from that branch with the answer.", task.ID),
	}, nil
}
