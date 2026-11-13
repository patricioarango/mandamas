package validate

import (
	"strings"
	"testing"
)

func TestRules(t *testing.T) {
	ok := func(name string, err error) {
		t.Helper()
		if err != nil {
			t.Errorf("%s: unexpected error %v", name, err)
		}
	}
	bad := func(name string, err error) {
		t.Helper()
		if err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	ok("key", Key("billing-api"))
	bad("key upper", Key("Billing"))
	bad("key short", Key("a"))
	bad("key slash", Key("a/b"))
	ok("color", Color("#3b6ff5"))
	bad("color name", Color("red"))
	ok("logo empty", Logo(""))
	ok("logo", Logo("data:image/webp;base64,AAAA"))
	bad("logo url", Logo("https://x/y.png"))
	bad("logo svg", Logo("data:image/svg+xml;base64,AAAA"))
	bad("logo big", Logo("data:image/png;base64,"+strings.Repeat("A", MaxLogo)))
	ok("url", RoutineURL("https://api.anthropic.com/v1/claude_code/routines/trig_1/fire"))
	bad("url host", RoutineURL("https://evil.example/v1/claude_code/routines/x/fire"))
	ok("token", RoutineToken("sk-ant-oat01-abc_DEF-1"))
	bad("token kind", RoutineToken("sk-ant-api03-abc"))
	if c, err := Categories([]string{" Bug", "feature"}); err != nil || c[0] != "bug" {
		t.Errorf("categories clean: %v %v", c, err)
	}
	_, err := Categories([]string{"bug", "BUG"})
	bad("categories dup", err)
	_, err = Categories(nil)
	bad("categories empty", err)
	ok("task id", TaskID("aB3dE"))
	bad("task id path", TaskID("../x"))
}
