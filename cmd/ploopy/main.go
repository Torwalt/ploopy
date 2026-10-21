// Command ploopy drives a task document one unit per fresh agent session.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	ploopy "github.com/Torwalt/ploopy"
	"github.com/Torwalt/ploopy/internal/config"
	"github.com/Torwalt/ploopy/internal/control"
	"github.com/Torwalt/ploopy/internal/harness"
	"github.com/Torwalt/ploopy/internal/harness/claude"
	"github.com/Torwalt/ploopy/internal/harness/codex"
	"github.com/Torwalt/ploopy/internal/harness/opencode"
	"github.com/Torwalt/ploopy/internal/plan"
	"github.com/Torwalt/ploopy/internal/repo"
	"github.com/Torwalt/ploopy/internal/state"
	"github.com/Torwalt/ploopy/internal/ui"
)

// version is stamped at build time.
var version = "dev"

// env is what every command works against.
type env struct {
	root      string
	cfg       config.Config
	harnesses harness.Set
	extra     []string                                     // arguments after --, passed to the harness
	popStash  func(ctx context.Context, root string) error // tests make a pop fail
}

func main() {
	argv, extra := splitExtra(os.Args[1:])
	if len(argv) > 0 && argv[0] == "guard" {
		os.Exit(runGuard())
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()

	// `ploopy` alone asks what to do; each command then asks for what it lacks.
	if len(argv) == 0 && len(extra) == 0 && ui.Interactive() {
		picked, err := chooseCommand(ctx)
		if err != nil {
			fmt.Fprintln(os.Stderr, "ploopy:", err)
			os.Exit(2)
		}
		argv = []string{picked}
	}

	e := &env{extra: extra}
	root := newRoot(e)
	root.SetArgs(withDefaultCommand(root, argv))

	if err := root.ExecuteContext(ctx); err != nil {
		if !errors.Is(err, context.Canceled) {
			fmt.Fprintln(os.Stderr, "ploopy:", err)
		}
		os.Exit(2)
	}
}

// chooseCommand offers what ploopy can do. A run going on comes first: it is
// most likely what brought the author here.
func chooseCommand(ctx context.Context) (string, error) {
	var lives []control.Live
	if cwd, err := os.Getwd(); err == nil {
		if root, err := repo.Root(ctx, cwd); err == nil {
			lives, _ = livesIn(ctx, root)
		}
	}
	return ui.PickKeyed("What now?", "help", commandChoices(lives))
}

func commandChoices(lives []control.Live) []ui.Choice {
	var choices []ui.Choice
	if len(lives) > 0 {
		label := "change a running run"
		if len(lives) == 1 {
			label += ": " + runLabel(lives[0])
		} else {
			label += fmt.Sprintf(" (%d going on)", len(lives))
		}
		choices = append(choices, ui.Choice{Key: 'a', Label: label, Value: "adjust"})
	}
	choices = append(choices,
		ui.Choice{Key: 'r', Label: "run a plan", Value: "run"},
		ui.Choice{Key: 'v', Label: "review a plan's work: findings, a triage, a fix plan", Value: "review"},
		ui.Choice{Key: 's', Label: "status of every plan", Value: "status"},
		ui.Choice{Key: 'p', Label: "report on a plan: time, checks, cost", Value: "report"},
		ui.Choice{Key: 'h', Label: "history: what a unit took each agent here, and the usage windows", Value: "history"},
		ui.Choice{Key: 'n', Label: "lend a subscription to every run, or list what is lent", Value: "lend"},
		ui.Choice{Key: 'c', Label: "close a plan without landing it, or a review or fix plan", Value: "close"},
		ui.Choice{Key: 'l', Label: "land a finished branch: rebase, check, close its plans, fast-forward", Value: "land"},
		ui.Choice{Key: 'w', Label: "show a unit's work order", Value: "show"},
		ui.Choice{Key: 'y', Label: "replay a unit's session", Value: "replay"},
		ui.Choice{Key: 'm', Label: "mark a unit by hand", Value: "mark"},
		ui.Choice{Key: 't', Label: "lint the plans", Value: "lint"},
	)
	return choices
}

// splitExtra takes everything after `--` for the harness.
func splitExtra(argv []string) ([]string, []string) {
	for i, arg := range argv {
		if arg == "--" {
			return argv[:i], argv[i+1:]
		}
	}
	return argv, nil
}

// withDefaultCommand makes `ploopy` and `ploopy --plan X` mean `ploopy run`.
func withDefaultCommand(root *cobra.Command, argv []string) []string {
	if len(argv) == 0 {
		return []string{"run"}
	}
	switch argv[0] {
	case "-h", "--help", "-v", "--version", "help", "completion":
		return argv
	}
	for _, command := range root.Commands() {
		if command.Name() == argv[0] {
			return argv
		}
	}
	return append([]string{"run"}, argv...)
}

func newRoot(e *env) *cobra.Command {
	root := &cobra.Command{
		Use:           "ploopy",
		Short:         "Drive a task document one unit per fresh agent session",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			if e.root, err = repo.Root(cmd.Context(), cwd); err != nil {
				return err
			}
			if e.cfg, err = config.Load(e.root); err != nil {
				return err
			}
			e.harnesses = allHarnesses()
			return nil
		},
	}
	root.AddCommand(
		newRun(e),
		newReview(e),
		newStatus(e),
		newShow(e),
		newLint(e),
		newMark(e),
		newReplay(e),
		newReport(e),
		newHistory(e),
		newLend(e),
		newReclaim(e),
		newClose(e),
		newLand(e),
		newAdjust(e),
		newSkills(e),
		guardCommand(),
	)
	return root
}

// allHarnesses is every harness ploopy can drive, in offer order.
func allHarnesses() harness.Set { return harness.Set{claude.New(), opencode.New(), codex.New()} }

// resolve finds a plan by path, by name, or by stem under the plans directory.
func (e *env) resolve(name string) (*plan.Plan, error) {
	directory := filepath.Join(e.root, e.cfg.Plans)
	candidates := []string{
		name,
		filepath.Join(e.root, name),
		filepath.Join(directory, name),
		filepath.Join(directory, name+".md"),
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			return plan.Load(candidate, plan.Relative(candidate, e.root))
		}
	}

	wanted := strings.ToLower(strings.TrimSuffix(name, ".md"))
	plans, err := e.discover()
	if err != nil {
		return nil, err
	}
	for _, p := range plans {
		if strings.ToLower(p.Name()) == wanted {
			return p, nil
		}
	}
	return nil, fmt.Errorf("no plan %s", name)
}

func (e *env) discover() ([]*plan.Plan, error) {
	return plan.Discover(e.root, e.cfg.Plans)
}

func (e *env) state(p *plan.Plan) (*state.State, error) {
	return state.Load(filepath.Join(e.root, state.PathFor(p.Path)))
}

// repo is the checkout ploopy was started in.
func (e *env) repo() *repo.Repo { return repo.New(e.root, e.cfg.AuthorPaths) }

// skills is what sessions are given besides their work order.
type skills struct {
	unit, review, triage, checklist string
}

// skillsAt reads every skill from the checkout when it keeps its own, else
// from the binary, each with the checkout's own rules for it, and the
// checkout's review checklist when it keeps one.
func skillsAt(root string, cfg config.Config) (skills, error) {
	var out skills
	var err error
	if out.unit, err = skillAt(root, cfg); err != nil {
		return out, err
	}
	out.review = ownOr(root, config.RepoReviewSkill, ploopy.PlanReviewSkill)
	out.triage = ownOr(root, config.RepoTriageSkill, ploopy.ReviewTriageSkill)
	if path := cfg.ChecklistPath(root); path != "" {
		text, err := os.ReadFile(path)
		if err != nil {
			return out, fmt.Errorf("no review checklist at %s", path)
		}
		out.checklist = string(text)
	}
	return out, nil
}

// ownOr is the checkout's own copy of a skill, else the bundled one, with the
// checkout's rules for it. A copy `ploopy skills` wrote is not the checkout's
// own: the binary's is as new or newer.
func ownOr(root, own, bundled string) string {
	name := filepath.Base(filepath.Dir(own))
	if text, err := os.ReadFile(filepath.Join(root, own)); err == nil && !generated(string(text)) {
		return withLocalRules(root, name, string(text))
	}
	return withLocalRules(root, name, bundled)
}

// skillAt is the plan-unit skill every session is given: the one the settings
// name, else the checkout's own, else the one the binary ships with.
func skillAt(root string, cfg config.Config) (string, error) {
	if cfg.Skill == "" {
		return ownOr(root, config.RepoSkill, ploopy.PlanUnitSkill), nil
	}
	path := filepath.Join(root, cfg.Skill)
	text, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("no plan-unit skill at %s", path)
	}
	return withLocalRules(root, "plan-unit", string(text)), nil
}
