package store

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"cloud.google.com/go/firestore"
)

// Runs only against the Firestore emulator: FIRESTORE_EMULATOR_HOST=host:port go test ./internal/store/
func TestFirestoreEmulator(t *testing.T) {
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
	key := "p" + time.Now().Format("150405999")

	f := ProjectFields{Label: "Billing", Color: "#3b6ff5", Categories: []string{"bug"}}
	sec := Secrets{RoutineURL: "https://api.anthropic.com/v1/claude_code/routines/x/fire", RoutineToken: "sk-ant-oat01-x"}
	if _, err := s.CreateProject(ctx, key, f, "owner@example.com", sec); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateProject(ctx, key, f, "owner@example.com", sec); !errors.Is(err, ErrExists) {
		t.Fatalf("want ErrExists, got %v", err)
	}
	p, err := s.GetProject(ctx, key)
	if err != nil || !p.HasSecrets || p.Label != "Billing" {
		t.Fatalf("get: %+v %v", p, err)
	}
	f.Label = "Billing API"
	if p, err = s.UpdateProject(ctx, key, f, nil); err != nil || p.Label != "Billing API" {
		t.Fatalf("update: %+v %v", p, err)
	}
	if _, err := s.UpdateProject(ctx, "missing-"+key, f, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update missing: %v", err)
	}

	tk, err := s.CreateTask(ctx, Task{Project: key, Category: "bug", Title: "x", CreatedBy: "owner@example.com"})
	if err != nil || tk.Status != "queued" {
		t.Fatalf("create task: %+v %v", tk, err)
	}
	if err := s.AnswerTask(ctx, tk.ID, "A"); !errors.Is(err, ErrConflict) {
		t.Fatalf("answer queued task: %v", err)
	}
	_, err = c.Collection("tasks").Doc(tk.ID).Update(ctx, []firestore.Update{
		{Path: "status", Value: "needs_input"},
		{Path: "questions", Value: []Question{{Question: "A or B?", AskedAt: time.Now()}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AnswerTask(ctx, tk.ID, "A"); err != nil {
		t.Fatalf("answer: %v", err)
	}
	if err := s.AnswerTask(ctx, tk.ID, "B"); !errors.Is(err, ErrConflict) {
		t.Fatalf("second answer: %v", err)
	}
	_, _ = s.CreateTask(ctx, Task{Project: key, Category: "bug", Title: "y", CreatedBy: "owner@example.com"})
	_, _ = c.Collection("tasks").Doc(tk.ID).Update(ctx, []firestore.Update{{Path: "status", Value: "done"}})

	ps, err := s.ListProjects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var found *Project
	for i := range ps {
		if ps[i].Key == key {
			found = &ps[i]
		}
	}
	if found == nil || found.Done != 1 || !found.HasSecrets {
		t.Fatalf("list: %+v", found)
	}
	ts, err := s.ListTasks(ctx, key, 100)
	if err != nil || len(ts) != 2 || ts[0].Title != "y" {
		t.Fatalf("tasks newest first: %+v %v", ts, err)
	}

	if err := s.ArchiveProject(ctx, key); err != nil {
		t.Fatal(err)
	}
	ps, _ = s.ListProjects(ctx)
	for _, p := range ps {
		if p.Key == key {
			t.Fatal("archived project must be hidden")
		}
	}
}
