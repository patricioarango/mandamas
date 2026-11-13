// Package allowlist decides which emails may use the app. The source of truth is the
// Firebase Remote Config parameter `allowed_emails` (a JSON array of emails).
package allowlist

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2/google"
)

type Checker interface {
	Allowed(ctx context.Context, email string) (bool, error)
}

// ErrNotLoaded means the list has never been fetched successfully, so everyone is denied.
var ErrNotLoaded = errors.New("allowlist not loaded")

// FetchFunc returns the current list of emails.
type FetchFunc func(ctx context.Context) ([]string, error)

// Cached keeps the last good list, refreshes it after ttl, and never falls back to "allow".
type Cached struct {
	fetch FetchFunc
	ttl   time.Duration
	now   func() time.Time

	mu         sync.Mutex
	emails     map[string]struct{}
	loaded     bool
	fetchedAt  time.Time
	refreshing bool
}

func NewCached(fetch FetchFunc, ttl time.Duration) *Cached {
	return &Cached{fetch: fetch, ttl: ttl, now: time.Now}
}

func (c *Cached) Allowed(ctx context.Context, email string) (bool, error) {
	c.mu.Lock()
	loaded, stale := c.loaded, c.now().Sub(c.fetchedAt) > c.ttl
	c.mu.Unlock()

	if !loaded {
		if err := c.refresh(ctx); err != nil {
			return false, fmt.Errorf("%w: %v", ErrNotLoaded, err)
		}
	} else if stale {
		c.mu.Lock()
		start := !c.refreshing
		c.refreshing = true
		c.mu.Unlock()
		if start {
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				if err := c.refresh(ctx); err != nil {
					slog.Warn("allowlist refresh failed, keeping last good copy", "err", err)
				}
				c.mu.Lock()
				c.refreshing = false
				c.mu.Unlock()
			}()
		}
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.emails[normalize(email)]
	return ok, nil
}

func (c *Cached) refresh(ctx context.Context) error {
	list, err := c.fetch(ctx)
	if err != nil {
		return err
	}
	set := make(map[string]struct{}, len(list))
	for _, e := range list {
		if e = normalize(e); e != "" {
			set[e] = struct{}{}
		}
	}
	c.mu.Lock()
	c.emails, c.loaded, c.fetchedAt = set, true, c.now()
	c.mu.Unlock()
	return nil
}

// Static is a fixed list (emulator/dev mode only).
type Static map[string]struct{}

func NewStatic(emails []string) Static {
	s := Static{}
	for _, e := range emails {
		s[normalize(e)] = struct{}{}
	}
	return s
}

func (s Static) Allowed(_ context.Context, email string) (bool, error) {
	_, ok := s[normalize(email)]
	return ok, nil
}

func normalize(e string) string { return strings.ToLower(strings.TrimSpace(e)) }

// RemoteConfigFetcher reads parameter `param` from the project's Remote Config template
// using the service account (Application Default Credentials).
func RemoteConfigFetcher(projectID, param string) FetchFunc {
	url := "https://firebaseremoteconfig.googleapis.com/v1/projects/" + projectID + "/remoteConfig"
	return func(ctx context.Context) ([]string, error) {
		client, err := google.DefaultClient(ctx, "https://www.googleapis.com/auth/firebase.remoteconfig")
		if err != nil {
			return nil, fmt.Errorf("credentials: %w", err)
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		res, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("remote config: HTTP %d", res.StatusCode)
		}
		return parseTemplate(res.Body, param)
	}
}

type template struct {
	Parameters map[string]struct {
		DefaultValue struct {
			Value string `json:"value"`
		} `json:"defaultValue"`
	} `json:"parameters"`
}

func parseTemplate(r io.Reader, param string) ([]string, error) {
	var t template
	if err := json.NewDecoder(r).Decode(&t); err != nil {
		return nil, fmt.Errorf("remote config: bad template: %w", err)
	}
	p, ok := t.Parameters[param]
	if !ok {
		return nil, fmt.Errorf("remote config: parameter %q not found", param)
	}
	var list []string
	if err := json.Unmarshal([]byte(p.DefaultValue.Value), &list); err != nil {
		return nil, fmt.Errorf("remote config: %q must be a JSON array of emails", param)
	}
	return list, nil
}
