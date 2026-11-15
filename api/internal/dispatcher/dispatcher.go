// Package dispatcher watches queued tasks and fires each project's routine for them,
// one at a time, within the routine rate limits.
package dispatcher

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/mandamas/api/internal/notifications"
	"github.com/mandamas/api/internal/registry"
	"github.com/mandamas/api/internal/routines"
	"github.com/mandamas/api/internal/store"
)

type Store interface {
	WatchQueuedTasks(ctx context.Context, fn func(id string))
	GetTask(ctx context.Context, id string) (store.Task, error)
	ClaimTask(ctx context.Context, id string) (store.Task, error)
	MarkDispatched(ctx context.Context, id, sessionURL string, resumed bool) error
	RequeueTask(ctx context.Context, id string, retryAfter time.Time) error
	FailDispatch(ctx context.Context, id, reason string) (store.Task, error)
	ResetStuck(ctx context.Context, cutoff time.Time) (int, error)
	ListTasksBy(ctx context.Context, project, status string, limit int) ([]store.Task, error)
	MarkReminded(ctx context.Context, id string, at time.Time) error
}

type Firer interface {
	Fire(ctx context.Context, routineURL, token, text string) (routines.Result, error)
}

type Config interface {
	Routine(key string) (store.Secrets, string, error)
	Ready() <-chan struct{}
}

type Notifier interface {
	Notify(ctx context.Context, taskID, kind, title, body string)
}

type Limiter interface {
	Wait(routine string) time.Duration
	Record(routine string)
}

type Options struct {
	Store    Store
	Firer    Firer
	Config   Config
	Notifier Notifier
	Limiter  Limiter

	SweepEvery     time.Duration // default 3m
	StuckAfter     time.Duration // default 10m
	RemindAfter    time.Duration // default 24h
	DefaultBackoff time.Duration // after a 429 without Retry-After; default 1m
}

type Dispatcher struct {
	o   Options
	now func() time.Time

	mu      sync.Mutex
	queue   []string
	pending map[string]bool // in queue or waiting on a timer
	wake    chan struct{}
}

func New(o Options) *Dispatcher {
	if o.SweepEvery == 0 {
		o.SweepEvery = 3 * time.Minute
	}
	if o.StuckAfter == 0 {
		o.StuckAfter = 10 * time.Minute
	}
	if o.RemindAfter == 0 {
		o.RemindAfter = 24 * time.Hour
	}
	if o.DefaultBackoff == 0 {
		o.DefaultBackoff = time.Minute
	}
	return &Dispatcher{o: o, now: time.Now, pending: map[string]bool{}, wake: make(chan struct{}, 1)}
}

// Run blocks until ctx ends. It waits for the project config to load first, so tasks aren't failed
// as "not configured" during startup.
func (d *Dispatcher) Run(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-d.o.Config.Ready():
	}
	slog.Info("dispatcher started")
	go d.o.Store.WatchQueuedTasks(ctx, d.Enqueue)
	go d.sweepLoop(ctx)
	for {
		id, ok := d.next(ctx)
		if !ok {
			return
		}
		d.process(ctx, id)
	}
}

// Enqueue adds a task ID to be looked at (ignored if already pending).
func (d *Dispatcher) Enqueue(id string) {
	d.mu.Lock()
	if d.pending[id] {
		d.mu.Unlock()
		return
	}
	d.pending[id] = true
	d.queue = append(d.queue, id)
	d.mu.Unlock()
	select {
	case d.wake <- struct{}{}:
	default:
	}
}

func (d *Dispatcher) next(ctx context.Context) (string, bool) {
	for {
		d.mu.Lock()
		if len(d.queue) > 0 {
			id := d.queue[0]
			d.queue = d.queue[1:]
			delete(d.pending, id)
			d.mu.Unlock()
			return id, true
		}
		d.mu.Unlock()
		select {
		case <-ctx.Done():
			return "", false
		case <-d.wake:
		}
	}
}

// later re-enqueues id after wait. While waiting it counts as pending, so listener echoes don't stack timers.
func (d *Dispatcher) later(ctx context.Context, id string, wait time.Duration) {
	d.mu.Lock()
	if d.pending[id] {
		d.mu.Unlock()
		return
	}
	d.pending[id] = true
	d.mu.Unlock()
	time.AfterFunc(wait, func() {
		if ctx.Err() != nil {
			return
		}
		d.mu.Lock()
		delete(d.pending, id)
		d.mu.Unlock()
		d.Enqueue(id)
	})
}

func (d *Dispatcher) process(ctx context.Context, id string) {
	t, err := d.o.Store.GetTask(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return
	}
	if err != nil {
		slog.Error("dispatch: read task", "task", id, "err", err)
		d.later(ctx, id, 30*time.Second)
		return
	}
	if t.Status != store.StatusQueued {
		return
	}
	now := d.now()
	if t.RetryAfter != nil && t.RetryAfter.After(now) {
		d.later(ctx, id, t.RetryAfter.Sub(now))
		return
	}

	sec, missing, cfgErr := d.o.Config.Routine(t.Project)
	routine := ""
	if cfgErr == nil {
		routine = routines.RoutineID(sec.RoutineURL)
		if wait := d.o.Limiter.Wait(routine); wait > 0 {
			slog.Info("dispatch: rate limit, waiting", "task", id, "project", t.Project, "wait", wait.Round(time.Second).String())
			d.later(ctx, id, wait)
			return
		}
	}

	// Claim before firing: the fire API has no idempotency key, so a double fire means a double PR.
	t, err = d.o.Store.ClaimTask(ctx, id)
	if errors.Is(err, store.ErrConflict) || errors.Is(err, store.ErrNotFound) {
		return
	}
	if err != nil {
		slog.Error("dispatch: claim", "task", id, "err", err)
		d.later(ctx, id, 30*time.Second)
		return
	}

	if cfgErr != nil {
		slog.Warn("dispatch: project not configured", "task", id, "project", t.Project, "missing", missing)
		d.fail(ctx, t, registry.ErrNotConfigured.Error())
		return
	}

	resumed := t.HasAnswers()
	d.o.Limiter.Record(routine)
	fireCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	res, err := d.o.Firer.Fire(fireCtx, sec.RoutineURL, sec.RoutineToken, BuildText(t))
	cancel()

	var rl *routines.RateLimitError
	switch {
	case errors.As(err, &rl):
		wait := rl.RetryAfter
		if wait <= 0 {
			wait = d.o.DefaultBackoff
		}
		slog.Warn("dispatch: routine rate limited (429), re-queued", "task", id, "project", t.Project, "retryIn", wait.String())
		if err := d.o.Store.RequeueTask(ctx, id, d.now().Add(wait)); err != nil {
			slog.Error("dispatch: requeue", "task", id, "err", err)
		}
	case err != nil:
		slog.Error("dispatch: fire failed", "task", id, "project", t.Project, "err", err)
		d.fail(ctx, t, err.Error())
	default:
		if err := d.o.Store.MarkDispatched(ctx, id, res.SessionURL, resumed); err != nil {
			slog.Error("dispatch: mark dispatched (routine already fired)", "task", id, "err", err)
		}
		slog.Info("dispatched", "task", id, "project", t.Project, "resume", resumed)
	}
}

func (d *Dispatcher) fail(ctx context.Context, t store.Task, reason string) {
	if _, err := d.o.Store.FailDispatch(ctx, t.ID, reason); err != nil {
		slog.Error("dispatch: mark failed", "task", t.ID, "err", err)
		return
	}
	d.o.Notifier.Notify(ctx, t.ID, notifications.TypeFailed, "Task failed: "+t.Title, reason)
}
