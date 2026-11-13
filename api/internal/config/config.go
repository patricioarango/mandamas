// Package config loads and validates the server's environment.
package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

type Config struct {
	ProjectID       string
	Port            string
	AppDir          string
	CredentialsFile string // empty when running against emulators
	TrustedProxies  []string
	AllowlistParam  string
	AllowlistTTL    time.Duration

	// Emulator mode (local dev/tests only). Auth emulator tokens are unsigned, so this can't be used against production.
	AuthEmulator    bool
	DevAllowedEmail []string
}

// Load reads env vars. Errors name the missing variable, never a value.
func Load() (Config, error) {
	c := Config{
		ProjectID:      os.Getenv("FIREBASE_PROJECT_ID"),
		Port:           envOr("PORT", "9090"),
		AppDir:         envOr("APP_DIR", "/srv/app"),
		AllowlistParam: envOr("ALLOWLIST_PARAM", "allowed_emails"),
		AllowlistTTL:   5 * time.Minute,
		AuthEmulator:   os.Getenv("FIREBASE_AUTH_EMULATOR_HOST") != "",
	}
	if v := os.Getenv("TRUSTED_PROXIES"); v != "" {
		c.TrustedProxies = splitList(v)
	}
	if c.ProjectID == "" {
		return c, errors.New("missing env var FIREBASE_PROJECT_ID")
	}
	if c.AuthEmulator {
		c.DevAllowedEmail = splitList(os.Getenv("DEV_ALLOWED_EMAILS"))
		return c, nil
	}
	c.CredentialsFile = os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")
	if c.CredentialsFile == "" {
		return c, errors.New("missing env var GOOGLE_APPLICATION_CREDENTIALS")
	}
	st, err := os.Stat(c.CredentialsFile)
	if err != nil || st.IsDir() || st.Size() == 0 {
		return c, fmt.Errorf("GOOGLE_APPLICATION_CREDENTIALS does not point to a service account JSON file (set SERVICE_ACCOUNT_PATH in .env)")
	}
	return c, nil
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
