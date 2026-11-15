package dispatcher

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/mandamas/api/internal/routines"
	"github.com/mandamas/api/internal/store"
)

// BuildText is the routine's input. On a resume it adds the answered Q&A history and the Resume line,
// dropping the oldest Q&A first to stay under the API's text limit.
func BuildText(t store.Task) string {
	head := fmt.Sprintf("Task %s\nCategory: %s\nBranch: task/%s\n%s\n\n%s", t.ID, t.Category, t.ID, t.Title, t.Description)
	if !t.HasAnswers() {
		return clip(head, routines.MaxText)
	}
	resume := fmt.Sprintf("\n\nResume: continue from existing branch task/%s; do not start over.", t.ID)

	var qa []string
	for i, q := range t.Questions {
		if q.Answer == "" {
			continue
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Q%d: %s\n", i+1, q.Question)
		if len(q.Options) > 0 {
			fmt.Fprintf(&b, "Options: %s\n", strings.Join(q.Options, " | "))
		}
		fmt.Fprintf(&b, "A%d: %s", i+1, q.Answer)
		qa = append(qa, b.String())
	}

	budget := routines.MaxText - utf8.RuneCountInString(resume)
	head = clip(head, budget-200) // keep room for at least a note about the Q&A
	for dropped := 0; ; dropped++ {
		var b strings.Builder
		b.WriteString(head)
		b.WriteString("\n\nQuestions you asked and the user's answers:\n")
		if dropped > 0 {
			fmt.Fprintf(&b, "(%d older Q&A omitted for length)\n", dropped)
		}
		b.WriteString(strings.Join(qa[dropped:], "\n\n"))
		if s := b.String(); utf8.RuneCountInString(s) <= budget || dropped == len(qa) {
			return clip(s, budget) + resume
		}
	}
}

func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}
