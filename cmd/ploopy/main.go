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
	"github.com/Torwalt/ploopy/internal/harness"
	"github.com/Torwalt/ploopy/internal/harness/claude"
	"github.com/Torwalt/ploopy/internal/harness/opencode"
	"github.com/Torwalt/ploopy/internal/plan"
	"github.com/Torwalt/ploopy/internal/repo"
	"github.com/Torwalt/ploopy/internal/state"
)

// version is stamped at build time.
var version = "dev"

// env is what every command works against.
type env struct {
	root      string
	cfg       config.Config
	harnesses harness.Set
	extra     []string // arguments after --, passed to the harness
}

func main() {
	argv, extra := splitExtra(os.Args[1:])
	if len(argv) > 0 && argv[0] == "guard" {
		os.Exit(runGuard())
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()

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
		newStatus(e),
		newShow(e),
		newLint(e),
		newMark(e),
		newReplay(e),
		newReport(e),
		newAdjust(e),
		guardCommand(),
	)
	return root
}

// allHarnesses is every harness ploopy can drive, in offer order.
func allHarnesses() harness.Set { return harness.Set{claude.New(), opencode.New()} }

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

// skillAt is the plan-unit skill every session is given: the one the settings
// name, else the checkout's own, else the one the binary ships with.
func skillAt(root string, cfg config.Config) (string, error) {
	path := ""
	switch {
	case cfg.Skill != "":
		path = filepath.Join(root, cfg.Skill)
	default:
		own := filepath.Join(root, config.RepoSkill)
		if info, err := os.Stat(own); err == nil && info.Mode().IsRegular() {
			path = own
		}
	}
	if path == "" {
		return ploopy.PlanUnitSkill, nil
	}
	text, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("no plan-unit skill at %s", path)
	}
	return string(text), nil
}
