// Package validate holds the input rules shared by the HTTP API (and later the MCP tools).
package validate

import (
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

var (
	keyRE      = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,39}$`)
	categoryRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,19}$`)
	colorRE    = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	urlRE      = regexp.MustCompile(`^https://api\.anthropic\.com/v1/claude_code/routines/[A-Za-z0-9_-]+/fire$`)
	tokenRE    = regexp.MustCompile(`^sk-ant-oat01-[A-Za-z0-9_-]+$`)
	logoRE     = regexp.MustCompile(`^data:image/(webp|png|jpeg);base64,([A-Za-z0-9+/=]+)$`)
	taskIDRE   = regexp.MustCompile(`^[A-Za-z0-9]{1,64}$`)
)

const (
	MaxLogo        = 200_000
	MaxCategories  = 12
	MaxLabel       = 60
	MaxDescription = 300
	MaxTitle       = 300
	MaxTaskText    = 60_000
	MaxAnswer      = 20_000
)

func Key(k string) error {
	if !keyRE.MatchString(k) {
		return errors.New("key: 2–40 lowercase letters, numbers or dashes")
	}
	return nil
}

func TaskID(id string) error {
	if !taskIDRE.MatchString(id) {
		return errors.New("invalid task id")
	}
	return nil
}

func Color(c string) error {
	if !colorRE.MatchString(c) {
		return errors.New("color must be #rrggbb")
	}
	return nil
}

func Logo(l string) error {
	if l == "" {
		return nil
	}
	if len(l) > MaxLogo {
		return errors.New("logo is too large")
	}
	m := logoRE.FindStringSubmatch(l)
	if m == nil {
		return errors.New("logo must be a webp, png or jpeg data URL")
	}
	if _, err := base64.StdEncoding.DecodeString(m[2]); err != nil {
		return errors.New("logo is not valid base64")
	}
	return nil
}

func RoutineURL(u string) error {
	if !urlRE.MatchString(u) {
		return errors.New("routine URL must look like https://api.anthropic.com/v1/claude_code/routines/<id>/fire")
	}
	return nil
}

func RoutineToken(t string) error {
	if !tokenRE.MatchString(t) {
		return errors.New("routine token must start with sk-ant-oat01-")
	}
	return nil
}

// Categories lowercases, trims and checks for duplicates; returns the cleaned list.
func Categories(in []string) ([]string, error) {
	if len(in) == 0 || len(in) > MaxCategories {
		return nil, fmt.Errorf("pick 1–%d categories", MaxCategories)
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, c := range in {
		c = strings.ToLower(strings.TrimSpace(c))
		if !categoryRE.MatchString(c) {
			return nil, fmt.Errorf("category %q: up to 20 lowercase letters, numbers or dashes", c)
		}
		if seen[c] {
			return nil, fmt.Errorf("category %q is repeated", c)
		}
		seen[c] = true
		out = append(out, c)
	}
	return out, nil
}

// Text checks a trimmed string's length in characters.
func Text(field, s string, min, max int) error {
	n := utf8.RuneCountInString(s)
	if n < min {
		return fmt.Errorf("%s is required", field)
	}
	if n > max {
		return fmt.Errorf("%s is too long (max %d)", field, max)
	}
	return nil
}
