package loop

import (
	_ "embed"
	"regexp"
	"strings"

	"github.com/Torwalt/ploopy/internal/plan"
)

//go:embed prompt.md
var promptTemplate string

var (
	placeholderRe = regexp.MustCompile(`\{\{(\w+)\}\}`)
	frontMatterRe = regexp.MustCompile(`(?s)\A---\n.*?\n---\n+`)
)

const (
	noPrevious = "None. This is the first unit of the run, or the previous one left no handover."
	noFailure  = "None. This is the first attempt."
)

// skillBody drops a skill's front matter, which is for the harness that loads
// it, not for the session reading it inline.
func skillBody(text string) string {
	return strings.TrimSpace(frontMatterRe.ReplaceAllString(text, ""))
}

// checksLine tells the session which commands must be green before it commits.
func checksLine(verify, test string, wantsTests bool) string {
	switch {
	case wantsTests && test != "":
		var commands []string
		for _, command := range []string{verify, test} {
			if command != "" {
				commands = append(commands, "`"+command+"`")
			}
		}
		return strings.Join(commands, " and ") +
			" must be green before you commit; the loop runs them after you."
	case verify != "":
		tail := ""
		if test != "" {
			tail = ", and `" + test + "` green when behaviour changed"
		}
		return "`" + verify + "` must be green" + tail + "; the loop runs `" + verify + "` after you."
	case test != "":
		return "`" + test + "` must be green when behaviour changed."
	}
	return "Leave the repository's own checks green before you commit."
}

type promptArgs struct {
	Skill      string
	Plan       *plan.Plan
	Unit       plan.Unit
	Context    []string
	Handover   string
	Previous   string
	Failure    string
	WantsTests bool
	Verify     string
	Test       string
}

// reading lists the documents a session reads first.
func reading(docs []string) string {
	if len(docs) == 0 {
		return "* Nothing beyond the work order."
	}
	lines := make([]string, 0, len(docs))
	for _, doc := range docs {
		lines = append(lines, "* `"+doc+"`")
	}
	return strings.Join(lines, "\n")
}

// fill puts values into a template's placeholders.
func fill(template string, values map[string]string) string {
	return placeholderRe.ReplaceAllStringFunc(template, func(match string) string {
		return values[placeholderRe.FindStringSubmatch(match)[1]]
	})
}

// renderPrompt is everything one session starts from.
func renderPrompt(args promptArgs) string {
	values := map[string]string{
		"unit":       args.Unit.ID,
		"title":      args.Unit.Title,
		"plan":       args.Plan.Path,
		"skill":      skillBody(args.Skill),
		"context":    reading(args.Context),
		"tests":      checksLine(args.Verify, args.Test, args.WantsTests),
		"handover":   args.Handover,
		"previous":   strings.TrimSpace(orDefault(args.Previous, noPrevious)),
		"failure":    strings.TrimSpace(orDefault(args.Failure, noFailure)),
		"work_order": strings.TrimSpace(args.Plan.WorkOrder(args.Unit)),
	}
	return fill(promptTemplate, values)
}

func orDefault(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
