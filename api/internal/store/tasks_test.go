package store

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"cloud.google.com/go/firestore"
)

// Task lifecycle against the Firestore emulator: FIRESTORE_EMULATOR_HOST=host:port go test ./internal/store/
func TestTaskLifecycleEmulator(t *testing.T) {
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("FIRESTORE_EMULATOR_HOST not set")
	}
	ctx := context.Background()
	c, err := firestore.NewClient(ctx, "demo-mandamas-store-test")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	s := NewFirestore(c)
	project := "lc" + time.Now().Format("150405999")

	// Listener: queued tasks arrive, including after a re-queue.
	seen := make(chan string, 10)
	wctx, stop := context.WithCancel(ctx)
	defer stop()
	go s.WatchQueuedTasks(wctx, func(id string) { seen <- id })
	waitFor := func(id string) {
		t.Helper()
		for {
			select {
			case got := <-seen:
				if got == id {
					return
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("listener never delivered %s", id)
			}
		}
	}

	tk, err := s.CreateTask(ctx, Task{Project: project, Category: "bug", Title: "x", CreatedBy: "mcp"})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(tk.ID)

	if _, err := s.ClaimTask(ctx, tk.ID); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := s.ClaimTask(ctx, tk.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("double claim: %v", err)
	}
	if err := s.RequeueTask(ctx, tk.ID, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	waitFor(tk.ID)
	got, _ := s.GetTask(ctx, tk.ID)
	if got.Status != StatusQueued || got.RetryAfter == nil {
		t.Fatalf("requeued: %+v", got)
	}

	if _, err := s.ClaimTask(ctx, tk.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDispatched(ctx, tk.ID, "https://claude.ai/code/s1", false); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetTask(ctx, tk.ID)
	if got.Status != StatusDispatched || got.Branch != "task/"+tk.ID || got.RetryAfter != nil || got.SessionURL == "" {
		t.Fatalf("dispatched: %+v", got)
	}

	// Ask, answer, resume.
	if _, err := s.AddQuestion(ctx, tk.ID, "A or B?", []string{"A", "B"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddQuestion(ctx, tk.ID, "again?", nil); !errors.Is(err, ErrConflict) {
		t.Fatalf("second question: %v", err)
	}
	if err := s.MarkReminded(ctx, tk.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.AnswerTask(ctx, tk.ID, "A"); err != nil {
		t.Fatal(err)
	}
	waitFor(tk.ID)
	if _, err := s.ClaimTask(ctx, tk.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDispatched(ctx, tk.ID, "https://claude.ai/code/s2", true); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetTask(ctx, tk.ID)
	if got.ResumeCount != 1 || !got.HasAnswers() || got.Questions[0].Options[1] != "B" {
		t.Fatalf("resumed: %+v", got)
	}

	// Routine reports back; project and category untouched.
	if _, err := s.ReportFromRoutine(ctx, tk.ID, RoutineReport{Status: StatusPROpen, PRURL: "https://github.com/o/r/pull/1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReportFromRoutine(ctx, tk.ID, RoutineReport{Status: StatusDone}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReportFromRoutine(ctx, tk.ID, RoutineReport{Status: StatusFailed, Error: "x"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("report after done: %v", err)
	}
	got, _ = s.GetTask(ctx, tk.ID)
	if got.Status != StatusDone || got.PRURL == "" || got.Project != project || got.Category != "bug" {
		t.Fatalf("done: %+v", got)
	}

	// Stuck sweeper and failure.
	t2, _ := s.CreateTask(ctx, Task{Project: project, Category: "bug", Title: "stuck", CreatedBy: "mcp"})
	if _, err := s.ClaimTask(ctx, t2.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := s.ResetStuck(ctx, time.Now().Add(-10*time.Minute)); err != nil || n != 0 {
		t.Fatalf("fresh dispatch must not reset: %d %v", n, err)
	}
	if n, err := s.ResetStuck(ctx, time.Now().Add(time.Second)); err != nil || n < 1 {
		t.Fatalf("reset stuck: %d %v", n, err)
	}
	if _, err := s.ClaimTask(ctx, t2.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FailDispatch(ctx, t2.ID, "Project not configured"); err != nil {
		t.Fatal(err)
	}
	ts, err := s.ListTasksBy(ctx, project, StatusFailed, 10)
	if err != nil || len(ts) != 1 || ts[0].Error != "Project not configured" {
		t.Fatalf("list failed: %+v %v", ts, err)
	}

	// Devices.
	if _, err := c.Collection("devices").Doc("dev1").Set(ctx, map[string]any{"token": "tok", "platform": "web"}); err != nil {
		t.Fatal(err)
	}
	devs, err := s.ListDevices(ctx)
	if err != nil || len(devs) == 0 {
		t.Fatalf("devices: %v %v", devs, err)
	}
	if err := s.DeleteDevice(ctx, "dev1"); err != nil {
		t.Fatal(err)
	}
}
