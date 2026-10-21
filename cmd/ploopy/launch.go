package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/Torwalt/ploopy/internal/config"
	"github.com/Torwalt/ploopy/internal/plan"
	"github.com/Torwalt/ploopy/internal/review"
)

// launchedFlag marks the run a launch started, which runs where it is and
// asks nothing more.
const launchedFlag = "launched"

// detached reports whether this run goes to a tmux window instead of the
// terminal it was started in.
func detached(e *env, f *runFlags) bool {
	if f.launched || f.dryRun || f.here {
		return false
	}
	return f.detach || e.cfg.Launch == config.LaunchTmux
}

// windowName is the tmux window a plan's family runs in: a plan, its reviews
// and their fix plans share one branch, one worktree and one window.
func windowName(p *plan.Plan) string {
	family := review.Family(p.Path)
	return "ploopy-" + strings.TrimSuffix(filepath.Base(family), filepath.Ext(family))
}

// launch starts the run in a tmux window of the current session, with the
// settings already chosen. The window's shell runs it, so the run belongs to
// the tmux server and not to this process, and the shell's hooks (direnv) load
// as they would for the author. A window the family has already runs it in its
// idle shell, below the earlier runs' output. The window is never closed: it
// stays until the author closes it.
func launch(ctx context.Context, cmd *cobra.Command, e *env, f *runFlags, p *plan.Plan, pick chosenRun) error {
	if os.Getenv("TMUX") == "" {
		return errors.New("a detached run needs tmux, and this terminal is not in a tmux session; " +
			"start ploopy inside tmux, or pass --here")
	}
	main, err := e.mainRoot(ctx)
	if err != nil {
		return err
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	// A window kept from an earlier run may have its shell anywhere.
	line := shellLine([]string{"cd", main}) + " && " + shellLine(append([]string{self}, launchArgs(cmd, f, p, pick)...))
	name := windowName(p)

	target, err := freeWindow(ctx, name)
	if err != nil {
		return err
	}
	if target == "" {
		args := append([]string{"new-window", "-d", "-P", "-F", "#{window_id}", "-n", name, "-c", main}, carried()...)
		out, err := tmux(ctx, args...)
		if err != nil {
			return err
		}
		target = strings.TrimSpace(out)
	}
	if _, err := tmux(ctx, "send-keys", "-t", target, "-l", line); err != nil {
		return err
	}
	if _, err := tmux(ctx, "send-keys", "-t", target, "Enter"); err != nil {
		return err
	}
	session, _ := tmux(ctx, "display-message", "-p", "-t", target, "#S")
	fmt.Printf("%s runs in tmux window %s of session %s\n", p.Path, name, strings.TrimSpace(session))
	fmt.Printf("follow it there, or with `ploopy status`; change it with `ploopy adjust`\n")
	return nil
}

// freeWindow is the window named name in the current session when its shell
// waits for a command, or empty when there is none. A window running
// something else is the author's, or a run's, and is refused.
func freeWindow(ctx context.Context, name string) (string, error) {
	out, err := tmux(ctx, "list-windows", "-F", "#{window_id}\t#{window_name}\t#{pane_current_command}")
	if err != nil {
		return "", err
	}
	for _, row := range strings.Split(strings.TrimSpace(out), "\n") {
		fields := strings.Split(row, "\t")
		if len(fields) != 3 || fields[1] != name {
			continue
		}
		if !isShell(fields[2]) {
			return "", fmt.Errorf("tmux window %s is busy running %s; let it finish or close it", name, fields[2])
		}
		return fields[0], nil
	}
	return "", nil
}

// carried is the environment a launched run keeps from the terminal that
// launched it: which tools it finds, and where ploopy keeps the state `ploopy
// adjust` and `ploopy status` read. The rest is the window's own; an agent's
// environment would reach the run's sessions otherwise.
func carried() []string {
	var args []string
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if key == "PATH" || key == "XDG_STATE_HOME" || key == "XDG_RUNTIME_DIR" || strings.HasPrefix(key, "PLOOPY_") {
			args = append(args, "-e", entry)
		}
	}
	return args
}

func isShell(command string) bool {
	switch command {
	case "zsh", "bash", "sh", "fish", "dash", "ksh", "nu", filepath.Base(os.Getenv("SHELL")):
		return true
	}
	return false
}

func tmux(ctx context.Context, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, "tmux", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("tmux %s: %v: %s", args[0], err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// chosenFlags are the run flags launchArgs writes from the chosen settings,
// never copies from the command line.
var chosenFlags = map[string]bool{
	"plan": true, "all": true, "agent": true, "model": true, "effort": true,
	"secondary": true, "secondary-model": true, "secondary-effort": true,
	"peak": true, "subscriptions": true, "spend-all": true, "push": true,
	"finish": true, "then": true, "detach": true, "here": true, launchedFlag: true,
}

// launchArgs is the `ploopy run` that carries the chosen settings: every
// choice made on the board or by the plan as a flag, and every other run flag
// the author passed as they passed it.
func launchArgs(cmd *cobra.Command, f *runFlags, p *plan.Plan, pick chosenRun) []string {
	args := []string{"run", "--" + launchedFlag, "--plan", p.Path,
		"--agent", pick.agent.Harness.Name(), "--effort", pick.agent.Effort}
	if pick.agent.Model != "" {
		args = append(args, "--model", pick.agent.Model)
	}
	if s := pick.secondary; s != nil {
		args = append(args, "--secondary", s.Harness.Name(), "--secondary-effort", s.Effort)
		if s.Model != "" {
			args = append(args, "--secondary-model", s.Model)
		}
	} else {
		args = append(args, "--secondary", "none")
	}
	args = append(args, "--peak", peakName(pick.waitOffPeak),
		"--subscriptions", pick.subscriptions,
		"--spend-all="+strconv.FormatBool(pick.spendAll),
		"--push="+strconv.FormatBool(pick.push),
		"--finish", string(pick.finish))
	if p.Reviews() != "" {
		args = append(args, "--then", pick.then)
	}

	runOnly := runFlagNames()
	cmd.Flags().Visit(func(flag *pflag.Flag) {
		if chosenFlags[flag.Name] || !runOnly[flag.Name] || flag.Deprecated != "" {
			return
		}
		args = append(args, "--"+flag.Name+"="+flag.Value.String())
	})
	return args
}

// runFlagNames is every flag `ploopy run` takes, so a command with flags of
// its own (review) hands on only the run's.
func runFlagNames() map[string]bool {
	probe := &cobra.Command{}
	addRunFlags(probe, &runFlags{})
	names := map[string]bool{}
	probe.Flags().VisitAll(func(flag *pflag.Flag) { names[flag.Name] = true })
	return names
}

// shellLine quotes args for the POSIX shells and zsh a tmux window runs.
func shellLine(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		if arg != "" && strings.Trim(arg, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_./=,:+@") == "" {
			quoted[i] = arg
			continue
		}
		quoted[i] = "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
	}
	return strings.Join(quoted, " ")
}
