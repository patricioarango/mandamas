package dispatcher

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/mandamas/api/internal/notifications"
	"github.com/mandamas/api/internal/store"
)

func (d *Dispatcher) sweepLoop(ctx context.Context) {
	tick := time.NewTicker(d.o.SweepEvery)
	defer tick.Stop()
	for {
		d.Sweep(ctx)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// Sweep re-queues stuck dispatches and reminds about unanswered questions.
func (d *Dispatcher) Sweep(ctx context.Context) {
	now := d.now()
	if n, err := d.o.Store.ResetStuck(ctx, now.Add(-d.o.StuckAfter)); err != nil {
		slog.Error("sweep: stuck tasks", "err", err)
	} else if n > 0 {
		slog.Warn("sweep: re-queued stuck tasks", "count", n)
	}

	waiting, err := d.o.Store.ListTasksBy(ctx, "", store.StatusNeedsInput, 200)
	if err != nil {
		slog.Error("sweep: needs_input tasks", "err", err)
		return
	}
	for _, t := range waiting {
		q := t.LastQuestion()
		if q == nil || q.Answer != "" || now.Sub(q.AskedAt) < d.o.RemindAfter {
			continue
		}
		if t.RemindedAt != nil && now.Sub(*t.RemindedAt) < d.o.RemindAfter {
			continue
		}
		if err := d.o.Store.MarkReminded(ctx, t.ID, now); err != nil {
			if !errors.Is(err, store.ErrConflict) {
				slog.Error("sweep: mark reminded", "task", t.ID, "err", err)
			}
			continue
		}
		d.o.Notifier.Notify(ctx, t.ID, notifications.TypeReminder, "Still waiting on you: "+t.Title, q.Question)
	}
}
