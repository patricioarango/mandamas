package allowlist

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCachedDeniesUntilLoaded(t *testing.T) {
	c := NewCached(func(context.Context) ([]string, error) { return nil, errors.New("boom") }, time.Minute)
	ok, err := c.Allowed(context.Background(), "a@x.com")
	if ok || !errors.Is(err, ErrNotLoaded) {
		t.Fatalf("want denied + ErrNotLoaded, got %v %v", ok, err)
	}
}

func TestCachedCaseInsensitiveAndKeepsLastGood(t *testing.T) {
	var calls atomic.Int32
	fail := atomic.Bool{}
	c := NewCached(func(context.Context) ([]string, error) {
		calls.Add(1)
		if fail.Load() {
			return nil, errors.New("remote config down")
		}
		return []string{" Owner@Example.com "}, nil
	}, time.Minute)
	now := time.Now()
	c.now = func() time.Time { return now }

	if ok, err := c.Allowed(context.Background(), "OWNER@example.com"); !ok || err != nil {
		t.Fatalf("want allowed, got %v %v", ok, err)
	}
	if ok, _ := c.Allowed(context.Background(), "other@example.com"); ok {
		t.Fatal("other email must be denied")
	}

	// Stale + fetch failing: still answers from the last good copy.
	fail.Store(true)
	now = now.Add(2 * time.Minute)
	if ok, err := c.Allowed(context.Background(), "owner@example.com"); !ok || err != nil {
		t.Fatalf("want last good copy, got %v %v", ok, err)
	}
	time.Sleep(50 * time.Millisecond) // background refresh runs and fails
	if ok, _ := c.Allowed(context.Background(), "owner@example.com"); !ok {
		t.Fatal("failed refresh must not drop the list")
	}
	if calls.Load() < 2 {
		t.Fatal("expected a background refresh")
	}
}

func TestParseTemplate(t *testing.T) {
	body := `{"parameters":{"allowed_emails":{"defaultValue":{"value":"[\"a@x.com\",\"b@x.com\"]"}}}}`
	list, err := parseTemplate(strings.NewReader(body), "allowed_emails")
	if err != nil || len(list) != 2 {
		t.Fatalf("got %v %v", list, err)
	}
	if _, err := parseTemplate(strings.NewReader(body), "missing"); err == nil {
		t.Fatal("missing parameter must error")
	}
	bad := `{"parameters":{"allowed_emails":{"defaultValue":{"value":"a@x.com"}}}}`
	if _, err := parseTemplate(strings.NewReader(bad), "allowed_emails"); err == nil {
		t.Fatal("non-array value must error")
	}
}
