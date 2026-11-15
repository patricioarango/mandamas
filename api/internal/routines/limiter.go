package routines

import (
	"sync"
	"time"
)

// Limiter keeps fires under the published limits: per routine and per account, in a sliding hour.
// It is in memory only; "Run now" in the web UI shares the account budget, so stay below the real limits.
type Limiter struct {
	mu         sync.Mutex
	perRoutine int
	perAccount int
	window     time.Duration
	byRoutine  map[string][]time.Time
	all        []time.Time
	now        func() time.Time
}

func NewLimiter(perRoutine, perAccount int) *Limiter {
	return &Limiter{perRoutine: perRoutine, perAccount: perAccount, window: time.Hour, byRoutine: map[string][]time.Time{}, now: time.Now}
}

// Wait returns how long until one more fire for this routine fits (0 = now).
func (l *Limiter) Wait(routine string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.prune(now)
	var wait time.Duration
	if h := l.byRoutine[routine]; len(h) >= l.perRoutine {
		wait = h[len(h)-l.perRoutine].Add(l.window).Sub(now)
	}
	if len(l.all) >= l.perAccount {
		wait = max(wait, l.all[len(l.all)-l.perAccount].Add(l.window).Sub(now))
	}
	return max(wait, 0)
}

// Record counts a fire attempt.
func (l *Limiter) Record(routine string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.byRoutine[routine] = append(l.byRoutine[routine], now)
	l.all = append(l.all, now)
}

func (l *Limiter) prune(now time.Time) {
	cut := now.Add(-l.window)
	keep := func(ts []time.Time) []time.Time {
		i := 0
		for i < len(ts) && !ts[i].After(cut) {
			i++
		}
		return ts[i:]
	}
	l.all = keep(l.all)
	for k, ts := range l.byRoutine {
		if ts = keep(ts); len(ts) == 0 {
			delete(l.byRoutine, k)
		} else {
			l.byRoutine[k] = ts
		}
	}
}
