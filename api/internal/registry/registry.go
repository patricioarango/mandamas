// Package registry keeps the live project config (projects + their routine secrets) in memory,
// fed by Firestore listeners, so changes made in the app apply without a restart.
package registry

import (
	"context"
	"errors"
	"sort"
	"sync"

	"github.com/mandamas/api/internal/store"
)

// ErrNotConfigured means the project is missing, archived or has no routine secrets.
var ErrNotConfigured = errors.New("Project not configured")

type Watcher interface {
	WatchProjects(ctx context.Context, fn func(map[string]store.Project))
	WatchSecrets(ctx context.Context, fn func(map[string]store.Secrets))
}

type Registry struct {
	mu       sync.RWMutex
	projects map[string]store.Project
	secrets  map[string]store.Secrets
	ready    chan struct{}
	once     sync.Once
}

func New() *Registry { return &Registry{ready: make(chan struct{})} }

// Run starts both listeners and blocks until ctx ends.
func (r *Registry) Run(ctx context.Context, w Watcher) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); w.WatchProjects(ctx, r.SetProjects) }()
	go func() { defer wg.Done(); w.WatchSecrets(ctx, r.SetSecrets) }()
	wg.Wait()
}

func (r *Registry) SetProjects(m map[string]store.Project) {
	r.mu.Lock()
	r.projects = m
	r.mu.Unlock()
	r.checkReady()
}

func (r *Registry) SetSecrets(m map[string]store.Secrets) {
	r.mu.Lock()
	r.secrets = m
	r.mu.Unlock()
	r.checkReady()
}

func (r *Registry) checkReady() {
	r.mu.RLock()
	ok := r.projects != nil && r.secrets != nil
	r.mu.RUnlock()
	if ok {
		r.once.Do(func() { close(r.ready) })
	}
}

// Ready is closed once both listeners delivered their first snapshot.
func (r *Registry) Ready() <-chan struct{} { return r.ready }

// Project returns a non-archived project (no secrets needed). ok=false if missing or archived.
func (r *Registry) Project(key string) (store.Project, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.projects[key]
	if !ok || p.Archived {
		return store.Project{}, false
	}
	_, p.HasSecrets = r.secrets[key]
	return p, true
}

// Routine returns what's needed to fire the project's routine. On ErrNotConfigured, missing names
// what's wrong (for logs; never a value).
func (r *Registry) Routine(key string) (sec store.Secrets, missing string, err error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.projects[key]
	switch {
	case !ok:
		return sec, "project", ErrNotConfigured
	case p.Archived:
		return sec, "archived", ErrNotConfigured
	}
	sec, ok = r.secrets[key]
	switch {
	case !ok:
		return sec, "secrets", ErrNotConfigured
	case sec.RoutineURL == "":
		return sec, "routineUrl", ErrNotConfigured
	case sec.RoutineToken == "":
		return sec, "routineToken", ErrNotConfigured
	}
	return sec, "", nil
}

// Projects lists non-archived projects, sorted by key.
func (r *Registry) Projects() []store.Project {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]store.Project, 0, len(r.projects))
	for k, p := range r.projects {
		if !p.Archived {
			_, p.HasSecrets = r.secrets[k]
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}
