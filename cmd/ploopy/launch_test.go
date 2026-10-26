package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Torwalt/ploopy/internal/loop"
	"github.com/Torwalt/ploopy/internal/plan"
	"github.com/Torwalt/ploopy/internal/ui"
)

const reviewPlanText = "---\nreviews: docs/plans/PASS.md\n---\n\n# Review\n\n## Stage 1 — Only\n\n### 1.1 One\n\n**Goal:** x\n\n**Done when:** y\n"

func chosenForLaunch(e *env) chosenRun {
	claude := e.harnesses.Get("claude")
	return chosenRun{
		agent:         loop.Agent{Harness: claude, Model: "opus", Effort: "high"},
		secondary:     &loop.Agent{Harness: e.harnesses.Get("codex"), Effort: "medium"},
		rescue:        &loop.Agent{Harness: e.harnesses.Get("opencode"), Model: "deepseek/deepseek-flash", Effort: "max"},
		subscriptions: "claude codex",
		push:          true,
		finish:        ui.FinishSuspend,
		followUp:      true,
	}
}

func TestALaunchedRunStartsWithTheSettingsChosenForIt(t *testing.T) {
	e := mainRepo(t)
	parent := newReview(e)
	if err := parent.ParseFlags([]string{"--units", "3", "--timeout", "1h", "--since", "1.2", "--agent", "opencode"}); err != nil {
		t.Fatal(err)
	}
	var pf reviewFlags
	p := plan.Parse(reviewPlanText, "docs/plans/PASS_REVIEW.md")
	args := launchArgs(parent, &pf.run, p, chosenForLaunch(e))
	if args[0] != "run" {
		t.Fatalf("a launch runs %v, want `run` first", args)
	}

	child := newRun(e)
	if err := child.ParseFlags(args[1:]); err != nil {
		t.Fatalf("`ploopy %s` does not parse: %v", strings.Join(args, " "), err)
	}
	var f runFlags
	f.planName, _ = child.Flags().GetString("plan")
	f.agent, _ = child.Flags().GetString("agent")
	f.model, _ = child.Flags().GetString("model")
	f.effort, _ = child.Flags().GetString("effort")
	f.secondary, _ = child.Flags().GetString("secondary")
	f.secondaryEff, _ = child.Flags().GetString("secondary-effort")
	f.rescue, _ = child.Flags().GetString("rescue")
	f.rescueMod, _ = child.Flags().GetString("rescue-model")
	f.rescueEff, _ = child.Flags().GetString("rescue-effort")
	f.subscriptions, _ = child.Flags().GetString("subscriptions")
	f.push, _ = child.Flags().GetBool("push")
	f.finish, _ = child.Flags().GetString("finish")
	f.followUp, _ = child.Flags().GetBool("follow-up")
	s, _, err := givenSettings(child, e, p, &f)
	if err != nil {
		t.Fatal(err)
	}
	if s.Harness != "claude" || s.Model != "opus" || s.Effort != "high" {
		t.Errorf("the launched run drives %s %s %s, want claude opus high", s.Harness, s.Model, s.Effort)
	}
	if s.Secondary == nil || s.Secondary.Harness != "codex" || s.Secondary.Effort != "medium" {
		t.Errorf("the launched run falls back to %+v, want codex at medium", s.Secondary)
	}
	if s.Rescue == nil || s.Rescue.Harness != "opencode" || s.Rescue.Model != "deepseek/deepseek-flash" ||
		s.Rescue.Effort != "max" {
		t.Errorf("the launched run rescues with %+v, want opencode deepseek/deepseek-flash max", s.Rescue)
	}
	if s.Subscriptions != "claude codex" || !s.Push || s.Finish != "suspend" || !s.FollowUp {
		t.Errorf("the launched run has %+v", s)
	}
	if f.planName != p.Path {
		t.Errorf("the launched run runs %s, want %s", f.planName, p.Path)
	}
	if units, _ := child.Flags().GetInt("units"); units != 3 {
		t.Errorf("the author's --units 3 reached the launched run as %d", units)
	}
	if launched, _ := child.Flags().GetBool(launchedFlag); !launched {
		t.Error("the launched run would launch again")
	}
	if child.Flags().Lookup("since") != nil {
		t.Error("run has grown a --since; the test no longer shows review's own flags are left out")
	}
}

func TestAPlansReviewsAndFixPlansShareItsWindow(t *testing.T) {
	for _, path := range []string{
		"docs/plans/PASS.md", "docs/plans/PASS_REVIEW.md", "docs/plans/PASS_FIXES.md",
		"docs/plans/PASS_FIXES_REVIEW.md",
	} {
		if got := windowName(planAt(t, path)); got != "ploopy-PASS" {
			t.Errorf("%s runs in window %s, want ploopy-PASS", path, got)
		}
	}
}

func TestALaunchedCommandLineSurvivesTheShell(t *testing.T) {
	args := []string{"/nix/store/x-ploopy/bin/ploopy", "--subscriptions", "claude codex", "--context", "it's.md", ""}
	out, err := exec.Command("sh", "-c", "printf '%s\\n' "+shellLine(args[1:])).Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n"); strings.Join(got, "|") != strings.Join(args[1:], "|") {
		t.Errorf("the shell read %q, want %q", got, args[1:])
	}
}

// fakeTmux puts a tmux on PATH that logs its arguments and lists windows as
// told.
func fakeTmux(t *testing.T, windows string) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "log")
	script := "#!/bin/sh\necho \"$*\" >> " + log + "\ncase \"$1\" in\n" +
		"list-windows) printf '" + windows + "' ;;\n" +
		"new-window) echo @9 ;;\n" +
		"display-message) echo work ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX", "/tmp/tmux-test,1,0")
	return log
}

func launched(t *testing.T, e *env, log string) ([]string, error) {
	t.Helper()
	err := launch(context.Background(), newRun(e), e, &runFlags{}, planAt(t, "docs/plans/PASS_REVIEW.md"), chosenForLaunch(e))
	raw, _ := os.ReadFile(log)
	return strings.Split(strings.TrimSpace(string(raw)), "\n"), err
}

func TestARunStartsInANewWindowOfItsOwn(t *testing.T) {
	e := mainRepo(t)
	log := fakeTmux(t, `@1\tzsh\tzsh\n`)
	t.Setenv("PLOOPY_SANDBOX", "here")
	calls, err := launched(t, e, log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(calls[1], "new-window -d -P -F #{window_id} -n ploopy-PASS -c ") || !strings.Contains(calls[1], " -e PLOOPY_SANDBOX=here") {
		t.Errorf("tmux was asked %q, want a detached window named ploopy-PASS with the run's environment", calls[1])
	}
	if !strings.HasPrefix(calls[2], "send-keys -t @9 -l ") || !strings.Contains(calls[2], " run --launched --plan docs/plans/PASS_REVIEW.md ") {
		t.Errorf("tmux was asked %q, want the run typed into the new window", calls[2])
	}
	if calls[3] != "send-keys -t @9 Enter" {
		t.Errorf("tmux was asked %q, want the run started", calls[3])
	}
}

func TestARunGoesToItsFamilysWindowWhileThatWaitsForACommand(t *testing.T) {
	e := mainRepo(t)
	log := fakeTmux(t, `@1\tzsh\tzsh\n@4\tploopy-PASS\tzsh\n`)
	calls, err := launched(t, e, log)
	if err != nil {
		t.Fatal(err)
	}
	for _, call := range calls {
		if strings.HasPrefix(call, "new-window") {
			t.Errorf("a second window was made beside the plan's own: %q", call)
		}
	}
	if !strings.HasPrefix(calls[1], "send-keys -t @4 -l cd "+e.root+" && ") {
		t.Errorf("tmux was asked %q, want the run typed into ploopy-PASS", calls[2])
	}
}

func TestARunUsesAnUnusedNameWhenItsWindowIsBusy(t *testing.T) {
	for _, tc := range []struct {
		name    string
		windows string
		want    string
	}{
		{"busy", `@4\tploopy-PASS\tploopy\n`, "ploopy-PASS_2"},
		{"occupied suffix", `@4\tploopy-PASS\tploopy\n@5\tploopy-PASS_2\tzsh\n`, "ploopy-PASS_3"},
		{"gap", `@4\tploopy-PASS\tploopy\n@6\tploopy-PASS_3\tploopy\n`, "ploopy-PASS_2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := mainRepo(t)
			log := fakeTmux(t, tc.windows)
			calls, err := launched(t, e, log)
			if err != nil {
				t.Fatal(err)
			}
			if len(calls) != 5 {
				t.Fatalf("tmux calls: %q, want list, create, send, enter and display", calls)
			}
			if !strings.HasPrefix(calls[1], "new-window -d -P -F #{window_id} -n "+tc.want+" -c ") {
				t.Errorf("tmux was asked %q, want a new window named %s", calls[1], tc.want)
			}
			if !strings.HasPrefix(calls[2], "send-keys -t @9 -l ") || calls[3] != "send-keys -t @9 Enter" {
				t.Errorf("run was not sent to the new window: %q", calls)
			}
		})
	}
}

func TestADetachedRunNeedsTmux(t *testing.T) {
	e := mainRepo(t)
	fakeTmux(t, "")
	t.Setenv("TMUX", "")
	err := launch(context.Background(), newRun(e), e, &runFlags{}, planAt(t, "docs/plans/PASS.md"), chosenForLaunch(e))
	if err == nil || !strings.Contains(err.Error(), "--here") {
		t.Errorf("a launch outside tmux said %v", err)
	}
}

func TestDetachedLaunchPreservesTheReviewModeChosenOnTheBoard(t *testing.T) {
	e := mainRepo(t)
	parent := newRun(e)
	if err := parent.ParseFlags([]string{"--review-mode", "collected"}); err != nil {
		t.Fatal(err)
	}
	pick := chosenForLaunch(e)
	pick.reviewMode = "direct"
	args := launchArgs(parent, &runFlags{}, plan.Parse("# Pass", "docs/plans/PASS.md"), pick)
	child := newRun(e)
	if err := child.ParseFlags(args[1:]); err != nil {
		t.Fatal(err)
	}
	if mode, _ := child.Flags().GetString("review-mode"); mode != "direct" {
		t.Fatalf("launched mode: %s", mode)
	}
}
