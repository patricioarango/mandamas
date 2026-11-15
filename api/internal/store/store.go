// Package store is the only place that talks to Firestore.
package store

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound = errors.New("not found")
	ErrExists   = errors.New("already exists")
	ErrConflict = errors.New("conflict")
)

type Project struct {
	Key         string    `firestore:"-" json:"key"`
	Label       string    `firestore:"label" json:"label"`
	Description string    `firestore:"description" json:"description"`
	Color       string    `firestore:"color" json:"color"`
	Logo        string    `firestore:"logo" json:"logo"`
	Categories  []string  `firestore:"categories" json:"categories"`
	Archived    bool      `firestore:"archived" json:"archived"`
	CreatedBy   string    `firestore:"createdBy" json:"createdBy"`
	CreatedAt   time.Time `firestore:"createdAt" json:"createdAt"`
	UpdatedAt   time.Time `firestore:"updatedAt" json:"updatedAt"`

	// Computed, never stored.
	HasSecrets bool `firestore:"-" json:"hasSecrets"`
	Done       int  `firestore:"-" json:"done"`
	NeedsInput int  `firestore:"-" json:"needsInput"`
}

// ProjectFields are the fields the app may set or edit.
type ProjectFields struct {
	Label, Description, Color, Logo string
	Categories                      []string
}

// Secrets are written, never returned to clients.
type Secrets struct {
	RoutineURL   string    `firestore:"routineUrl"`
	RoutineToken string    `firestore:"routineToken"`
	UpdatedAt    time.Time `firestore:"updatedAt"`
}

type Question struct {
	Question   string     `firestore:"question" json:"question"`
	Options    []string   `firestore:"options" json:"options"`
	AskedAt    time.Time  `firestore:"askedAt" json:"askedAt"`
	Answer     string     `firestore:"answer,omitempty" json:"answer,omitempty"`
	AnsweredAt *time.Time `firestore:"answeredAt,omitempty" json:"answeredAt,omitempty"`
}

type Task struct {
	ID          string     `firestore:"-" json:"id"`
	Project     string     `firestore:"project" json:"project"`
	Category    string     `firestore:"category" json:"category"`
	Title       string     `firestore:"title" json:"title"`
	Description string     `firestore:"description" json:"description"`
	Status      string     `firestore:"status" json:"status"`
	Questions   []Question `firestore:"questions,omitempty" json:"questions,omitempty"`
	Branch      string     `firestore:"branch,omitempty" json:"branch,omitempty"`
	ResumeCount int        `firestore:"resumeCount,omitempty" json:"resumeCount,omitempty"`
	RemindedAt  *time.Time `firestore:"remindedAt,omitempty" json:"remindedAt,omitempty"`
	RetryAfter  *time.Time `firestore:"retryAfter,omitempty" json:"retryAfter,omitempty"`
	SessionURL  string     `firestore:"sessionUrl,omitempty" json:"sessionUrl,omitempty"`
	PRURL       string     `firestore:"prUrl,omitempty" json:"prUrl,omitempty"`
	Error       string     `firestore:"error,omitempty" json:"error,omitempty"`
	CreatedBy   string     `firestore:"createdBy" json:"createdBy"`
	CreatedAt   time.Time  `firestore:"createdAt" json:"createdAt"`
	UpdatedAt   time.Time  `firestore:"updatedAt" json:"updatedAt"`
}

// Task statuses.
const (
	StatusQueued      = "queued"
	StatusDispatching = "dispatching"
	StatusDispatched  = "dispatched"
	StatusNeedsInput  = "needs_input"
	StatusPROpen      = "pr_open"
	StatusDone        = "done"
	StatusFailed      = "failed"
)

// LastQuestion returns the most recent question, or nil.
func (t Task) LastQuestion() *Question {
	if len(t.Questions) == 0 {
		return nil
	}
	return &t.Questions[len(t.Questions)-1]
}

// HasAnswers reports whether any question was answered (the next dispatch is a resume).
func (t Task) HasAnswers() bool {
	for _, q := range t.Questions {
		if q.Answer != "" {
			return true
		}
	}
	return false
}

// RoutineReport is what a routine may change on its task via update_task. Never project or category.
type RoutineReport struct {
	Status string // pr_open, done or failed
	PRURL  string // optional; required for pr_open unless already set
	Error  string // short reason when failed
}

// Device is an FCM registration token saved by the app.
type Device struct {
	ID    string `firestore:"-"`
	Token string `firestore:"token"`
}

// Store is what the app API needs.
type Store interface {
	ListProjects(ctx context.Context) ([]Project, error) // non-archived, with counts
	GetProject(ctx context.Context, key string) (Project, error)
	CreateProject(ctx context.Context, key string, f ProjectFields, createdBy string, s Secrets) (Project, error)
	UpdateProject(ctx context.Context, key string, f ProjectFields, s *Secrets) (Project, error)
	ArchiveProject(ctx context.Context, key string) error
	ListTasks(ctx context.Context, project string, limit int) ([]Task, error)
	CreateTask(ctx context.Context, t Task) (Task, error)
	AnswerTask(ctx context.Context, id, answer string) error
}
