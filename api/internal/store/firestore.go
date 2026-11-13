package store

import (
	"context"
	"errors"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Firestore struct {
	c   *firestore.Client
	now func() time.Time
}

func NewFirestore(c *firestore.Client) *Firestore { return &Firestore{c: c, now: time.Now} }

func (s *Firestore) projects() *firestore.CollectionRef { return s.c.Collection("projects") }
func (s *Firestore) secrets() *firestore.CollectionRef  { return s.c.Collection("projectSecrets") }
func (s *Firestore) tasks() *firestore.CollectionRef    { return s.c.Collection("tasks") }

func (s *Firestore) ListProjects(ctx context.Context) ([]Project, error) {
	docs, err := s.projects().Where("archived", "==", false).Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	// Only the IDs of secret docs, never their contents.
	secretRefs, err := s.secrets().Select().Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	has := map[string]bool{}
	for _, d := range secretRefs {
		has[d.Ref.ID] = true
	}
	out := make([]Project, 0, len(docs))
	for _, d := range docs {
		var p Project
		if err := d.DataTo(&p); err != nil {
			return nil, err
		}
		p.Key, p.HasSecrets = d.Ref.ID, has[d.Ref.ID]
		if p.Done, err = s.count(ctx, p.Key, "done"); err != nil {
			return nil, err
		}
		if p.NeedsInput, err = s.count(ctx, p.Key, "needs_input"); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

func (s *Firestore) count(ctx context.Context, project, status string) (int, error) {
	q := s.tasks().Where("project", "==", project).Where("status", "==", status)
	res, err := q.NewAggregationQuery().WithCount("n").Get(ctx)
	if err != nil {
		return 0, err
	}
	switch v := res["n"].(type) {
	case int64:
		return int(v), nil
	case interface{ GetIntegerValue() int64 }:
		return int(v.GetIntegerValue()), nil
	}
	return 0, nil
}

func (s *Firestore) GetProject(ctx context.Context, key string) (Project, error) {
	d, err := s.projects().Doc(key).Get(ctx)
	if status.Code(err) == codes.NotFound {
		return Project{}, ErrNotFound
	}
	if err != nil {
		return Project{}, err
	}
	var p Project
	if err := d.DataTo(&p); err != nil {
		return Project{}, err
	}
	p.Key = key
	if _, err := s.secrets().Doc(key).Get(ctx); err == nil {
		p.HasSecrets = true
	}
	return p, nil
}

func (s *Firestore) CreateProject(ctx context.Context, key string, f ProjectFields, createdBy string, sec Secrets) (Project, error) {
	now := s.now().UTC()
	p := Project{Key: key, Label: f.Label, Description: f.Description, Color: f.Color, Logo: f.Logo,
		Categories: f.Categories, CreatedBy: createdBy, CreatedAt: now, UpdatedAt: now, HasSecrets: true}
	sec.UpdatedAt = now
	err := s.c.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		if err := tx.Create(s.projects().Doc(key), p); err != nil {
			return err
		}
		return tx.Set(s.secrets().Doc(key), sec)
	})
	if status.Code(err) == codes.AlreadyExists {
		return Project{}, ErrExists
	}
	return p, err
}

func (s *Firestore) UpdateProject(ctx context.Context, key string, f ProjectFields, sec *Secrets) (Project, error) {
	now := s.now().UTC()
	err := s.c.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		ref := s.projects().Doc(key)
		if _, err := tx.Get(ref); err != nil {
			return err
		}
		if err := tx.Update(ref, []firestore.Update{
			{Path: "label", Value: f.Label}, {Path: "description", Value: f.Description},
			{Path: "color", Value: f.Color}, {Path: "logo", Value: f.Logo},
			{Path: "categories", Value: f.Categories}, {Path: "updatedAt", Value: now},
		}); err != nil {
			return err
		}
		if sec != nil {
			sec.UpdatedAt = now
			return tx.Set(s.secrets().Doc(key), *sec)
		}
		return nil
	})
	if status.Code(err) == codes.NotFound {
		return Project{}, ErrNotFound
	}
	if err != nil {
		return Project{}, err
	}
	return s.GetProject(ctx, key)
}

func (s *Firestore) ArchiveProject(ctx context.Context, key string) error {
	_, err := s.projects().Doc(key).Update(ctx, []firestore.Update{{Path: "archived", Value: true}, {Path: "updatedAt", Value: s.now().UTC()}})
	if status.Code(err) == codes.NotFound {
		return ErrNotFound
	}
	return err
}

func (s *Firestore) ListTasks(ctx context.Context, project string, limit int) ([]Task, error) {
	docs, err := s.tasks().Where("project", "==", project).OrderBy("createdAt", firestore.Desc).Limit(limit).Documents(ctx).GetAll()
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
	return out, nil
}

func (s *Firestore) CreateTask(ctx context.Context, t Task) (Task, error) {
	now := s.now().UTC()
	t.Status, t.CreatedAt, t.UpdatedAt = "queued", now, now
	ref := s.tasks().NewDoc()
	if _, err := ref.Create(ctx, t); err != nil {
		return Task{}, err
	}
	t.ID = ref.ID
	return t, nil
}

func (s *Firestore) AnswerTask(ctx context.Context, id, answer string) error {
	ref := s.tasks().Doc(id)
	err := s.c.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		d, err := tx.Get(ref)
		if err != nil {
			return err
		}
		var t Task
		if err := d.DataTo(&t); err != nil {
			return err
		}
		if t.Status != "needs_input" || len(t.Questions) == 0 || t.Questions[len(t.Questions)-1].Answer != "" {
			return ErrConflict
		}
		now := s.now().UTC()
		last := &t.Questions[len(t.Questions)-1]
		last.Answer, last.AnsweredAt = answer, &now
		return tx.Update(ref, []firestore.Update{
			{Path: "questions", Value: t.Questions}, {Path: "status", Value: "queued"}, {Path: "updatedAt", Value: now},
		})
	})
	if status.Code(err) == codes.NotFound {
		return ErrNotFound
	}
	if errors.Is(err, ErrConflict) {
		return ErrConflict
	}
	return err
}
