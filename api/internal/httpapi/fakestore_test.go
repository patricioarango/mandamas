package httpapi

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/mandamas/api/internal/store"
)

// memStore is an in-memory store.Store for handler tests.
type memStore struct {
	mu       sync.Mutex
	projects map[string]store.Project
	secrets  map[string]store.Secrets
	tasks    map[string]store.Task
	seq      int
}

func newMemStore() *memStore {
	return &memStore{projects: map[string]store.Project{}, secrets: map[string]store.Secrets{}, tasks: map[string]store.Task{}}
}

func (m *memStore) ListProjects(context.Context) ([]store.Project, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []store.Project
	for k, p := range m.projects {
		if !p.Archived {
			_, p.HasSecrets = m.secrets[k]
			out = append(out, p)
		}
	}
	return out, nil
}

func (m *memStore) GetProject(_ context.Context, key string) (store.Project, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.projects[key]
	if !ok {
		return p, store.ErrNotFound
	}
	_, p.HasSecrets = m.secrets[key]
	return p, nil
}

func (m *memStore) CreateProject(_ context.Context, key string, f store.ProjectFields, by string, s store.Secrets) (store.Project, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.projects[key]; ok {
		return store.Project{}, store.ErrExists
	}
	p := store.Project{Key: key, Label: f.Label, Description: f.Description, Color: f.Color, Logo: f.Logo, Categories: f.Categories, CreatedBy: by, CreatedAt: time.Now(), HasSecrets: true}
	m.projects[key], m.secrets[key] = p, s
	return p, nil
}

func (m *memStore) UpdateProject(_ context.Context, key string, f store.ProjectFields, s *store.Secrets) (store.Project, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.projects[key]
	if !ok {
		return p, store.ErrNotFound
	}
	p.Label, p.Description, p.Color, p.Logo, p.Categories = f.Label, f.Description, f.Color, f.Logo, f.Categories
	m.projects[key] = p
	if s != nil {
		m.secrets[key] = *s
	}
	_, p.HasSecrets = m.secrets[key]
	return p, nil
}

func (m *memStore) ArchiveProject(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.projects[key]
	if !ok {
		return store.ErrNotFound
	}
	p.Archived = true
	m.projects[key] = p
	return nil
}

func (m *memStore) ListTasks(_ context.Context, project string, _ int) ([]store.Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []store.Task
	for _, t := range m.tasks {
		if t.Project == project {
			out = append(out, t)
		}
	}
	return out, nil
}

func (m *memStore) CreateTask(_ context.Context, t store.Task) (store.Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	t.ID, t.Status = fmt.Sprintf("t%d", m.seq), "queued"
	m.tasks[t.ID] = t
	return t, nil
}

func (m *memStore) AnswerTask(_ context.Context, id, answer string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tasks[id]
	if !ok {
		return store.ErrNotFound
	}
	if t.Status != "needs_input" || len(t.Questions) == 0 || t.Questions[len(t.Questions)-1].Answer != "" {
		return store.ErrConflict
	}
	t.Questions[len(t.Questions)-1].Answer, t.Status = answer, "queued"
	m.tasks[id] = t
	return nil
}
