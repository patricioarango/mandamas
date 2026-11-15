package store

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Task lifecycle used by the dispatcher, the sweepers and the MCP tools.

func (s *Firestore) devices() *firestore.CollectionRef { return s.c.Collection("devices") }

func notFound(err error) error {
	if status.Code(err) == codes.NotFound {
		return ErrNotFound
	}
	return err
}

func (s *Firestore) GetTask(ctx context.Context, id string) (Task, error) {
	d, err := s.tasks().Doc(id).Get(ctx)
	if err != nil {
		return Task{}, notFound(err)
	}
	var t Task
	if err := d.DataTo(&t); err != nil {
		return Task{}, err
	}
	t.ID = id
	return t, nil
}

// ListTasksBy returns up to limit tasks, newest first, filtered by project and/or status (either may be empty).
// Equality filters only, sorted in memory, so no extra composite index is needed.
func (s *Firestore) ListTasksBy(ctx context.Context, project, st string, limit int) ([]Task, error) {
	if project != "" && st == "" {
		return s.ListTasks(ctx, project, limit)
	}
	q := s.tasks().Query
	if project != "" {
		q = q.Where("project", "==", project)
	}
	if st != "" {
		q = q.Where("status", "==", st)
	} else {
		q = q.OrderBy("createdAt", firestore.Desc)
	}
	docs, err := q.Limit(500).Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	out := make([]Task, 0, len(docs))
	for _, d := range docs {
		var t Task
		if err := d.DataTo(&t); err != nil {
			return nil, err
		}
		t.ID = d.Ref.ID
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// update runs fn on the task inside a transaction and writes the updates it returns (plus updatedAt).
// fn returns nil updates to write nothing.
func (s *Firestore) update(ctx context.Context, id string, fn func(t *Task, now time.Time) ([]firestore.Update, error)) (Task, error) {
	ref := s.tasks().Doc(id)
	var out Task
	err := s.c.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		d, err := tx.Get(ref)
		if err != nil {
			return err
		}
		var t Task
		if err := d.DataTo(&t); err != nil {
			return err
		}
		t.ID = id
		now := s.now().UTC()
		ups, err := fn(&t, now)
		if err != nil {
			return err
		}
		out = t
		if ups == nil {
			return nil
		}
		return tx.Update(ref, append(ups, firestore.Update{Path: "updatedAt", Value: now}))
	})
	if err != nil {
		return Task{}, notFound(err)
	}
	return out, nil
}

// ClaimTask moves a task from queued to dispatching. ErrConflict if it isn't queued (someone else took it).
func (s *Firestore) ClaimTask(ctx context.Context, id string) (Task, error) {
	return s.update(ctx, id, func(t *Task, now time.Time) ([]firestore.Update, error) {
		if t.Status != StatusQueued {
			return nil, ErrConflict
		}
		t.Status, t.RetryAfter = StatusDispatching, nil
		return []firestore.Update{{Path: "status", Value: StatusDispatching}, {Path: "retryAfter", Value: firestore.Delete}}, nil
	})
}

// MarkDispatched records a successful fire. The status only moves to dispatched if it is still dispatching
// (the routine may already have reported back).
func (s *Firestore) MarkDispatched(ctx context.Context, id, sessionURL string, resumed bool) error {
	_, err := s.update(ctx, id, func(t *Task, now time.Time) ([]firestore.Update, error) {
		ups := []firestore.Update{{Path: "sessionUrl", Value: sessionURL}, {Path: "branch", Value: "task/" + id}, {Path: "error", Value: firestore.Delete}}
		if t.Status == StatusDispatching {
			ups = append(ups, firestore.Update{Path: "status", Value: StatusDispatched})
		}
		if resumed {
			ups = append(ups, firestore.Update{Path: "resumeCount", Value: firestore.Increment(1)})
		}
		return ups, nil
	})
	return err
}

// RequeueTask puts a dispatching task back in the queue, not to be fired before retryAfter.
func (s *Firestore) RequeueTask(ctx context.Context, id string, retryAfter time.Time) error {
	_, err := s.update(ctx, id, func(t *Task, now time.Time) ([]firestore.Update, error) {
		if t.Status != StatusDispatching {
			return nil, ErrConflict
		}
		return []firestore.Update{{Path: "status", Value: StatusQueued}, {Path: "retryAfter", Value: retryAfter.UTC()}}, nil
	})
	return err
}

// FailDispatch marks a dispatching task failed with a short, secret-free reason.
func (s *Firestore) FailDispatch(ctx context.Context, id, reason string) (Task, error) {
	return s.update(ctx, id, func(t *Task, now time.Time) ([]firestore.Update, error) {
		if t.Status != StatusDispatching {
			return nil, ErrConflict
		}
		t.Status, t.Error = StatusFailed, reason
		return []firestore.Update{{Path: "status", Value: StatusFailed}, {Path: "error", Value: reason}}, nil
	})
}

// ResetStuck re-queues tasks that have been dispatching since before cutoff. Returns how many.
func (s *Firestore) ResetStuck(ctx context.Context, cutoff time.Time) (int, error) {
	docs, err := s.tasks().Where("status", "==", StatusDispatching).Documents(ctx).GetAll()
	if err != nil {
		return 0, err
	}
	n := 0
	for _, d := range docs {
		t, err := s.update(ctx, d.Ref.ID, func(t *Task, now time.Time) ([]firestore.Update, error) {
			if t.Status != StatusDispatching || t.UpdatedAt.After(cutoff) {
				return nil, nil
			}
			t.Status = StatusQueued
			return []firestore.Update{{Path: "status", Value: StatusQueued}}, nil
		})
		if err != nil {
			return n, err
		}
		if t.Status == StatusQueued {
			n++
		}
	}
	return n, nil
}

// MarkReminded sets remindedAt if the task is still waiting for an answer. ErrConflict otherwise.
func (s *Firestore) MarkReminded(ctx context.Context, id string, at time.Time) error {
	_, err := s.update(ctx, id, func(t *Task, now time.Time) ([]firestore.Update, error) {
		if t.Status != StatusNeedsInput {
			return nil, ErrConflict
		}
		return []firestore.Update{{Path: "remindedAt", Value: at.UTC()}}, nil
	})
	return err
}

// ReportFromRoutine applies update_task. Allowed while the task is out with Claude
// (dispatching, dispatched, needs_input, pr_open); done and failed are final.
func (s *Firestore) ReportFromRoutine(ctx context.Context, id string, r RoutineReport) (Task, error) {
	return s.update(ctx, id, func(t *Task, now time.Time) ([]firestore.Update, error) {
		switch t.Status {
		case StatusDispatching, StatusDispatched, StatusNeedsInput, StatusPROpen:
		default:
			return nil, ErrConflict
		}
		ups := []firestore.Update{{Path: "status", Value: r.Status}}
		t.Status = r.Status
		if r.PRURL != "" {
			t.PRURL = r.PRURL
			ups = append(ups, firestore.Update{Path: "prUrl", Value: r.PRURL})
		}
		if r.Status == StatusFailed {
			t.Error = r.Error
			ups = append(ups, firestore.Update{Path: "error", Value: r.Error})
		} else {
			t.Error = ""
			ups = append(ups, firestore.Update{Path: "error", Value: firestore.Delete})
		}
		return ups, nil
	})
}

// AddQuestion appends a question and sets needs_input. ErrConflict if the task isn't with Claude
// or a previous question is still unanswered.
func (s *Firestore) AddQuestion(ctx context.Context, id, question string, options []string) (Task, error) {
	return s.update(ctx, id, func(t *Task, now time.Time) ([]firestore.Update, error) {
		switch t.Status {
		case StatusDispatching, StatusDispatched, StatusPROpen:
		default:
			return nil, ErrConflict
		}
		if last := t.LastQuestion(); last != nil && last.Answer == "" {
			return nil, ErrConflict
		}
		if options == nil {
			options = []string{}
		}
		t.Questions = append(t.Questions, Question{Question: question, Options: options, AskedAt: now})
		t.Status, t.RemindedAt = StatusNeedsInput, nil
		return []firestore.Update{
			{Path: "questions", Value: t.Questions}, {Path: "status", Value: StatusNeedsInput}, {Path: "remindedAt", Value: firestore.Delete},
		}, nil
	})
}

// ---------- devices ----------

func (s *Firestore) ListDevices(ctx context.Context) ([]Device, error) {
	docs, err := s.devices().Select("token").Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	out := make([]Device, 0, len(docs))
	for _, d := range docs {
		var dev Device
		if err := d.DataTo(&dev); err != nil || dev.Token == "" {
			continue
		}
		dev.ID = d.Ref.ID
		out = append(out, dev)
	}
	return out, nil
}

func (s *Firestore) DeleteDevice(ctx context.Context, id string) error {
	_, err := s.devices().Doc(id).Delete(ctx)
	return err
}

// ---------- listeners ----------

// WatchQueuedTasks calls fn with the ID of every task that enters status == queued (including all
// queued tasks on the first snapshot). It reconnects on errors and returns when ctx ends.
func (s *Firestore) WatchQueuedTasks(ctx context.Context, fn func(id string)) {
	q := s.tasks().Where("status", "==", StatusQueued)
	watch(ctx, "queued tasks", q, func(snap *firestore.QuerySnapshot) {
		for _, ch := range snap.Changes {
			if ch.Kind == firestore.DocumentAdded {
				fn(ch.Doc.Ref.ID)
			}
		}
	})
}

// WatchProjects calls fn with every project (archived included) on each change.
func (s *Firestore) WatchProjects(ctx context.Context, fn func(map[string]Project)) {
	watch(ctx, "projects", s.projects().Query, func(snap *firestore.QuerySnapshot) {
		all, err := snap.Documents.GetAll()
		if err != nil {
			slog.Error("projects snapshot", "err", err)
			return
		}
		m := make(map[string]Project, len(all))
		for _, d := range all {
			var p Project
			if err := d.DataTo(&p); err != nil {
				slog.Error("bad project doc", "key", d.Ref.ID, "err", err)
				continue
			}
			p.Key = d.Ref.ID
			m[p.Key] = p
		}
		fn(m)
	})
}

// WatchSecrets calls fn with every project's secrets on each change. Only this server may call it.
func (s *Firestore) WatchSecrets(ctx context.Context, fn func(map[string]Secrets)) {
	watch(ctx, "project secrets", s.secrets().Query, func(snap *firestore.QuerySnapshot) {
		all, err := snap.Documents.GetAll()
		if err != nil {
			slog.Error("secrets snapshot", "err", err)
			return
		}
		m := make(map[string]Secrets, len(all))
		for _, d := range all {
			var sec Secrets
			if err := d.DataTo(&sec); err != nil {
				slog.Error("bad secrets doc", "key", d.Ref.ID) // never log the error, it may echo values
				continue
			}
			m[d.Ref.ID] = sec
		}
		fn(m)
	})
}

func watch(ctx context.Context, name string, q firestore.Query, fn func(*firestore.QuerySnapshot)) {
	backoff := time.Second
	for ctx.Err() == nil {
		it := q.Snapshots(ctx)
		for {
			snap, err := it.Next()
			if err != nil {
				if ctx.Err() == nil && !errors.Is(err, context.Canceled) {
					slog.Error("listener stopped, reconnecting", "listener", name, "err", err, "in", backoff.String())
				}
				break
			}
			backoff = time.Second
			fn(snap)
		}
		it.Stop()
		select {
		case <-ctx.Done():
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, time.Minute)
	}
}
