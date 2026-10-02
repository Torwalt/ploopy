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

// renderPrompt is everything one session starts from.
func renderPrompt(args promptArgs) string {
	reading := "* Nothing beyond the work order."
	if len(args.Context) > 0 {
		lines := make([]string, 0, len(args.Context))
		for _, doc := range args.Context {
			lines = append(lines, "* `"+doc+"`")
		}
		reading = strings.Join(lines, "\n")
	}

	values := map[string]string{
		"unit":       args.Unit.ID,
		"title":      args.Unit.Title,
		"plan":       args.Plan.Path,
		"skill":      skillBody(args.Skill),
		"context":    reading,
		"tests":      checksLine(args.Verify, args.Test, args.WantsTests),
		"handover":   args.Handover,
		"previous":   strings.TrimSpace(orDefault(args.Previous, noPrevious)),
		"failure":    strings.TrimSpace(orDefault(args.Failure, noFailure)),
		"work_order": strings.TrimSpace(args.Plan.WorkOrder(args.Unit)),
	}
	return placeholderRe.ReplaceAllStringFunc(promptTemplate, func(match string) string {
		return values[placeholderRe.FindStringSubmatch(match)[1]]
	})
}

func orDefault(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
