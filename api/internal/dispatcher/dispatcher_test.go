package dispatcher

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/mandamas/api/internal/registry"
	"github.com/mandamas/api/internal/routines"
	"github.com/mandamas/api/internal/store"
)

const routineURL = "https://api.anthropic.com/v1/claude_code/routines/r1/fire"

// ---------- fakes ----------

type fakeStore struct {
	mu        sync.Mutex
	tasks     map[string]store.Task
	reminded  []string
	stuckFrom time.Time
}

func (f *fakeStore) WatchQueuedTasks(ctx context.Context, fn func(string)) { <-ctx.Done() }
func (f *fakeStore) GetTask(_ context.Context, id string) (store.Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.tasks[id]
	if !ok {
		return t, store.ErrNotFound
	}
	return t, nil
}
func (f *fakeStore) set(id string, fn func(*store.Task) error) (store.Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.tasks[id]
	if !ok {
		return t, store.ErrNotFound
	}
	if err := fn(&t); err != nil {
		return t, err
	}
	f.tasks[id] = t
	return t, nil
}
func (f *fakeStore) ClaimTask(_ context.Context, id string) (store.Task, error) {
	return f.set(id, func(t *store.Task) error {
		if t.Status != store.StatusQueued {
			return store.ErrConflict
		}
		t.Status, t.RetryAfter = store.StatusDispatching, nil
		return nil
	})
}
func (f *fakeStore) MarkDispatched(_ context.Context, id, url string, resumed bool) error {
	_, err := f.set(id, func(t *store.Task) error {
		t.Status, t.SessionURL, t.Branch = store.StatusDispatched, url, "task/"+id
		if resumed {
			t.ResumeCount++
		}
		return nil
	})
	return err
}
func (f *fakeStore) RequeueTask(_ context.Context, id string, at time.Time) error {
	_, err := f.set(id, func(t *store.Task) error { t.Status, t.RetryAfter = store.StatusQueued, &at; return nil })
	return err
}
func (f *fakeStore) FailDispatch(_ context.Context, id, reason string) (store.Task, error) {
	return f.set(id, func(t *store.Task) error { t.Status, t.Error = store.StatusFailed, reason; return nil })
}
func (f *fakeStore) ResetStuck(_ context.Context, cutoff time.Time) (int, error) {
	f.stuckFrom = cutoff
	return 0, nil
}
func (f *fakeStore) ListTasksBy(_ context.Context, _, status string, _ int) ([]store.Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []store.Task
	for _, t := range f.tasks {
		if t.Status == status {
			out = append(out, t)
		}
	}
	return out, nil
}
func (f *fakeStore) MarkReminded(_ context.Context, id string, at time.Time) error {
	f.reminded = append(f.reminded, id)
	_, err := f.set(id, func(t *store.Task) error { t.RemindedAt = &at; return nil })
	return err
}

type fakeFirer struct {
	calls []string
	err   error
}

func (f *fakeFirer) Fire(_ context.Context, url, token, text string) (routines.Result, error) {
	f.calls = append(f.calls, text)
	if f.err != nil {
		return routines.Result{}, f.err
	}
	return routines.Result{Type: "routine_fire", SessionURL: "https://claude.ai/code/s"}, nil
}

type fakeNotifier struct{ kinds []string }

func (n *fakeNotifier) Notify(_ context.Context, id, kind, title, body string) {
	n.kinds = append(n.kinds, kind)
}

type fakeLimiter struct{ wait time.Duration }

func (l *fakeLimiter) Wait(string) time.Duration { return l.wait }
func (l *fakeLimiter) Record(string)             {}

func setup(tasks ...store.Task) (*Dispatcher, *fakeStore, *fakeFirer, *fakeNotifier, *fakeLimiter) {
	fs := &fakeStore{tasks: map[string]store.Task{}}
	for _, t := range tasks {
		fs.tasks[t.ID] = t
	}
	reg := registry.New()
	reg.SetProjects(map[string]store.Project{
		"billing":  {Key: "billing", Categories: []string{"bug"}},
		"nosecret": {Key: "nosecret"},
		"old":      {Key: "old", Archived: true},
	})
	reg.SetSecrets(map[string]store.Secrets{
		"billing": {RoutineURL: routineURL, RoutineToken: "sk-ant-oat01-x"},
		"old":     {RoutineURL: routineURL, RoutineToken: "sk-ant-oat01-x"},
	})
	fr, n, l := &fakeFirer{}, &fakeNotifier{}, &fakeLimiter{}
	d := New(Options{Store: fs, Firer: fr, Config: reg, Notifier: n, Limiter: l})
	return d, fs, fr, n, l
}

func queued(id, project string) store.Task {
	return store.Task{ID: id, Project: project, Category: "bug", Title: "Fix it", Description: "details", Status: store.StatusQueued}
}

// ---------- tests ----------

func TestDispatchSuccess(t *testing.T) {
	d, fs, fr, _, _ := setup(queued("t1", "billing"))
	d.process(context.Background(), "t1")
	got := fs.tasks["t1"]
	if got.Status != store.StatusDispatched || got.SessionURL == "" || got.Branch != "task/t1" || got.ResumeCount != 0 {
		t.Fatalf("task = %+v", got)
	}
	want := "Task t1\nCategory: bug\nBranch: task/t1\nFix it\n\ndetails"
	if len(fr.calls) != 1 || fr.calls[0] != want {
		t.Fatalf("fire text = %q", fr.calls)
	}
	// Already dispatched: a second look must not fire again.
	d.process(context.Background(), "t1")
	if len(fr.calls) != 1 {
		t.Fatal("fired twice")
	}
}

func TestDispatchNotConfigured(t *testing.T) {
	for _, p := range []string{"missing", "nosecret", "old"} {
		d, fs, fr, n, _ := setup(queued("t1", p))
		d.process(context.Background(), "t1")
		got := fs.tasks["t1"]
		if got.Status != store.StatusFailed || got.Error != "Project not configured" || len(fr.calls) != 0 || len(n.kinds) != 1 {
			t.Fatalf("%s: task = %+v, fires %d", p, got, len(fr.calls))
		}
	}
}

func TestDispatch429Requeues(t *testing.T) {
	d, fs, fr, _, _ := setup(queued("t1", "billing"))
	fr.err = &routines.RateLimitError{RetryAfter: 5 * time.Minute}
	d.process(context.Background(), "t1")
	got := fs.tasks["t1"]
	if got.Status != store.StatusQueued || got.RetryAfter == nil || time.Until(*got.RetryAfter) < 4*time.Minute {
		t.Fatalf("task = %+v", got)
	}
	// The listener sees it queued again; it must wait, not fire.
	fr.err = nil
	d.process(context.Background(), "t1")
	if len(fr.calls) != 1 || !d.pending["t1"] {
		t.Fatalf("fired before retryAfter (calls=%d)", len(fr.calls))
	}
}

func TestDispatchOtherErrorFails(t *testing.T) {
	d, fs, fr, n, _ := setup(queued("t1", "billing"))
	fr.err = &routines.HTTPError{Status: 500, Message: "routine API answered 500"}
	d.process(context.Background(), "t1")
	if got := fs.tasks["t1"]; got.Status != store.StatusFailed || got.Error != "routine API answered 500" || n.kinds[0] != "failed" {
		t.Fatalf("task = %+v", got)
	}
}

func TestDispatchWaitsForLimiter(t *testing.T) {
	d, fs, fr, _, l := setup(queued("t1", "billing"))
	l.wait = time.Hour
	d.process(context.Background(), "t1")
	if fs.tasks["t1"].Status != store.StatusQueued || len(fr.calls) != 0 {
		t.Fatal("should wait without claiming")
	}
}

func TestDispatchResume(t *testing.T) {
	task := queued("t1", "billing")
	now := time.Now()
	task.Questions = []store.Question{{Question: "Which DB?", Options: []string{"pg", "sqlite"}, Answer: "pg", AnsweredAt: &now}}
	d, fs, fr, _, _ := setup(task)
	d.process(context.Background(), "t1")
	if fs.tasks["t1"].ResumeCount != 1 {
		t.Fatal("resumeCount not incremented")
	}
	text := fr.calls[0]
	for _, s := range []string{"Q1: Which DB?", "Options: pg | sqlite", "A1: pg", "Resume: continue from existing branch task/t1; do not start over."} {
		if !strings.Contains(text, s) {
			t.Fatalf("missing %q in %q", s, text)
		}
	}
}

func TestBuildTextTruncatesOldestQA(t *testing.T) {
	task := queued("t1", "billing")
	task.Description = strings.Repeat("d", 30_000)
	for i := 0; i < 5; i++ {
		task.Questions = append(task.Questions, store.Question{Question: "q", Answer: strings.Repeat(string(rune('a'+i)), 15_000)})
	}
	text := BuildText(task)
	if n := utf8.RuneCountInString(text); n > routines.MaxText {
		t.Fatalf("text too long: %d", n)
	}
	if strings.Contains(text, "A1: a") || !strings.Contains(text, "A5: eee") || !strings.Contains(text, "older Q&A omitted") ||
		!strings.HasSuffix(text, "do not start over.") {
		t.Fatal("expected oldest Q&A dropped, newest kept, resume line last")
	}
}

func TestSweepReminds(t *testing.T) {
	old := time.Now().Add(-25 * time.Hour)
	recent := time.Now().Add(-time.Hour)
	mk := func(id string, asked time.Time, reminded *time.Time) store.Task {
		t := queued(id, "billing")
		t.Status, t.RemindedAt = store.StatusNeedsInput, reminded
		t.Questions = []store.Question{{Question: "?", AskedAt: asked}}
		return t
	}
	d, fs, _, n, _ := setup(mk("due", old, nil), mk("fresh", recent, nil), mk("remindedRecently", old, &recent), mk("remindedLongAgo", old, &old))
	d.Sweep(context.Background())
	if len(fs.reminded) != 2 || len(n.kinds) != 2 {
		t.Fatalf("reminded = %v", fs.reminded)
	}
	for _, id := range fs.reminded {
		if id != "due" && id != "remindedLongAgo" {
			t.Fatalf("unexpected reminder for %s", id)
		}
	}
	if time.Since(fs.stuckFrom) < 10*time.Minute {
		t.Fatal("stuck cutoff should be 10 minutes ago")
	}
}

func TestRunWaitsForConfig(t *testing.T) {
	fs := &fakeStore{tasks: map[string]store.Task{}}
	d := New(Options{Store: fs, Firer: &fakeFirer{}, Config: registry.New(), Notifier: &fakeNotifier{}, Limiter: &fakeLimiter{}})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	d.Run(ctx) // must return on ctx end even though config never loads
	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatal("Run returned early")
	}
}
