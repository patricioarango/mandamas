// Package config loads and validates the server's environment.
package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

const MinMCPKey = 32

type Config struct {
	ProjectID       string
	Port            string
	AppDir          string
	CredentialsFile string // empty when running against emulators
	TrustedProxies  []string
	AllowlistParam  string
	AllowlistTTL    time.Duration

	// MCP endpoint auth. Empty or too short disables /mcp (see MCPDisabledReason).
	MCPAPIKey         string
	MCPDisabledReason string

	// Dispatcher: off with DISPATCHER=off (e.g. a second instance that only serves the app).
	Dispatcher bool

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
		MCPAPIKey:      strings.TrimSpace(os.Getenv("MCP_API_KEY")),
		Dispatcher:     os.Getenv("DISPATCHER") != "off",
	}
	switch {
	case c.MCPAPIKey == "":
		c.MCPDisabledReason = "MCP_API_KEY is not set"
	case len(c.MCPAPIKey) < MinMCPKey || c.MCPAPIKey == "change-me":
		c.MCPAPIKey, c.MCPDisabledReason = "", fmt.Sprintf("MCP_API_KEY is too short (min %d chars; try: openssl rand -hex 32)", MinMCPKey)
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
