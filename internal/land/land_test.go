package land

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Torwalt/ploopy/internal/harness"
	"github.com/Torwalt/ploopy/internal/harness/fake"
)

type silent struct{}

func (silent) Say(string, ...any)  {}
func (silent) Fail(string, ...any) {}
func (silent) Event(harness.Event) {}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, dir, path, text string) {
	t.Helper()
	full := filepath.Join(dir, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, dir, path string) string {
	t.Helper()
	text, err := os.ReadFile(filepath.Join(dir, path))
	if err != nil {
		t.Fatal(err)
	}
	return string(text)
}

// fixture is a main checkout on master and a worktree of branch pass, each
// with one commit since they parted. Both change shared.txt as asked.
type fixture struct {
	main, tree string
	before     string
}

func setup(t *testing.T, onMaster, onBranch string) *fixture {
	t.Helper()
	base := t.TempDir()
	main := filepath.Join(base, "repo")
	if err := os.MkdirAll(main, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, main, "init", "-q", "-b", "master")
	git(t, main, "config", "user.name", "Test")
	git(t, main, "config", "user.email", "test@example.com")
	write(t, main, "shared.txt", "start\n")
	git(t, main, "add", "-A")
	git(t, main, "commit", "-q", "-m", "first")

	tree := filepath.Join(base, "repo.worktrees", "pass")
	git(t, main, "worktree", "add", "-q", "-b", "pass", tree)
	write(t, tree, "shared.txt", onBranch)
	write(t, tree, "branch.txt", "branch\n")
	git(t, tree, "add", "-A")
	git(t, tree, "commit", "-q", "-m", "branch work")

	write(t, main, "shared.txt", onMaster)
	write(t, main, "master.txt", "master\n")
	git(t, main, "add", "-A")
	git(t, main, "commit", "-q", "-m", "master work")

	return &fixture{main: main, tree: tree, before: git(t, tree, "rev-parse", "HEAD")}
}

func (f *fixture) options(t *testing.T, agent harness.Harness) Options {
	o := Options{
		Root:   f.tree,
		Branch: "pass",
		Onto:   "master",
		Logs:   filepath.Join(t.TempDir(), "logs"),
		Report: silent{},
	}
	if agent != nil {
		o.Agent = &Agent{Harness: agent}
	}
	return o
}

func (f *fixture) master(t *testing.T) string { return git(t, f.main, "rev-parse", "master") }

func done() harness.Outcome { return harness.Outcome{Marker: harness.Done} }

func TestACleanRebaseFastForwardsMasterInItsCheckout(t *testing.T) {
	f := setup(t, "start\n", "start\n")
	agent := fake.New()
	if _, err := Run(context.Background(), f.options(t, agent)); err != nil {
		t.Fatal(err)
	}
	if got := f.master(t); got != git(t, f.tree, "rev-parse", "HEAD") {
		t.Fatalf("master is at %s, not at the rebased branch", got)
	}
	if read(t, f.main, "branch.txt") != "branch\n" {
		t.Fatal("the main checkout should hold the branch's work after the fast-forward")
	}
	if agent.Started() != 0 {
		t.Fatalf("no session is needed for a clean landing; %d started", agent.Started())
	}
}

func TestASessionResolvesAConflictAndTheRebaseGoesOn(t *testing.T) {
	f := setup(t, "master\n", "branch\n")
	agent := fake.New(fake.Step{
		Do: func(dir string) error {
			if err := os.WriteFile(filepath.Join(dir, "shared.txt"), []byte("both\n"), 0o644); err != nil {
				return err
			}
			return exec.Command("git", "-C", dir, "add", "shared.txt").Run()
		},
		Outcome: done(),
	})
	result, err := Run(context.Background(), f.options(t, agent))
	if err != nil {
		t.Fatal(err)
	}
	if result.Sessions != 1 {
		t.Fatalf("one session should resolve the one conflict, not %d", result.Sessions)
	}
	if got := git(t, f.main, "show", "master:shared.txt"); got != "both" {
		t.Fatalf("master holds %q, not the resolution", got)
	}
	if !strings.Contains(agent.Prompts[0], "shared.txt") || !strings.Contains(agent.Prompts[0], "master work") {
		t.Fatal("the prompt should name the conflicting path and what master gained")
	}
}

type routedHarness struct {
	*fake.Harness
	harness.Preferences
}

func TestARepairContinuesThroughTheAPIAfterASubscriptionLimit(t *testing.T) {
	f := setup(t, "master\n", "branch\n")
	fakeAgent := fake.New(
		fake.Step{Outcome: harness.Outcome{RateLimit: &harness.RateLimit{Reason: "usage limit"}}},
		fake.Step{Do: func(dir string) error {
			if err := os.WriteFile(filepath.Join(dir, "shared.txt"), []byte("both\n"), 0o644); err != nil {
				return err
			}
			return exec.Command("git", "-C", dir, "add", "shared.txt").Run()
		}, Outcome: done()},
	)
	agent := &routedHarness{Harness: fakeAgent, Preferences: harness.Preferences{
		Fallbacks: map[string]string{"subscription": "api"},
	}}
	o := f.options(t, agent)
	o.Agent.Model, o.Attempts = "subscription", 1
	result, err := Run(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if result.Sessions != 2 || fakeAgent.Specs[0].Model != "subscription" || fakeAgent.Specs[1].Model != "api" {
		t.Fatalf("sessions %d, specs %+v", result.Sessions, fakeAgent.Specs)
	}
	if fakeAgent.Specs[0].LogPath == fakeAgent.Specs[1].LogPath {
		t.Fatal("the API session overwrote the limited session's log")
	}
	if got := git(t, f.main, "show", "master:shared.txt"); got != "both" {
		t.Fatalf("master holds %q", got)
	}
}

func TestAConflictLeftWithMarkersRestoresTheBranch(t *testing.T) {
	f := setup(t, "master\n", "branch\n")
	masterBefore := f.master(t)
	agent := fake.New(fake.Step{
		Do: func(dir string) error {
			return exec.Command("git", "-C", dir, "add", "shared.txt").Run()
		},
		Outcome: done(),
	})
	o := f.options(t, agent)
	o.Attempts = 2
	if _, err := Run(context.Background(), o); err == nil {
		t.Fatal("markers left in the file must not land")
	}
	if agent.Started() != 2 {
		t.Fatalf("each attempt should be a session; %d started", agent.Started())
	}
	if !strings.Contains(agent.Prompts[1], "conflict markers left in shared.txt") {
		t.Fatal("the second attempt should be told why the first failed")
	}
	if got := git(t, f.tree, "rev-parse", "HEAD"); got != f.before {
		t.Fatalf("the branch should be back at %s, not %s", f.before, got)
	}
	if f.master(t) != masterBefore {
		t.Fatal("master must not move when the landing fails")
	}
}

func TestACommitDuringAConflictIsRefused(t *testing.T) {
	f := setup(t, "master\n", "branch\n")
	agent := fake.New(fake.Step{
		Do: func(dir string) error {
			if err := os.WriteFile(filepath.Join(dir, "shared.txt"), []byte("both\n"), 0o644); err != nil {
				return err
			}
			return exec.Command("git", "-C", dir, "commit", "-q", "-am", "resolved").Run()
		},
		Outcome: done(),
	})
	o := f.options(t, agent)
	o.Attempts = 1
	if _, err := Run(context.Background(), o); err == nil {
		t.Fatal("a session that commits during the rebase must not land")
	}
	if git(t, f.tree, "rev-parse", "HEAD") != f.before {
		t.Fatal("the branch should be back where it was")
	}
}

func TestABlockedSessionStopsWithoutRetrying(t *testing.T) {
	f := setup(t, "master\n", "branch\n")
	agent := fake.New(fake.Step{Outcome: harness.Outcome{Marker: harness.Blocked, Reason: "contradictory"}})
	if _, err := Run(context.Background(), f.options(t, agent)); err == nil || !strings.Contains(err.Error(), "contradictory") {
		t.Fatalf("a block should stop the landing with its reason, got %v", err)
	}
	if agent.Started() != 1 {
		t.Fatalf("a block is not retried; %d sessions", agent.Started())
	}
}

// clash is a check that fails until both sides' files agree.
const clash = `test "$(cat master.txt)" = "$(cat branch.txt)"`

func TestASessionRepairsACheckThatFailsAfterTheRebase(t *testing.T) {
	f := setup(t, "start\n", "start\n")
	agent := fake.New(fake.Step{
		Do: func(dir string) error {
			if err := os.WriteFile(filepath.Join(dir, "branch.txt"), []byte("master\n"), 0o644); err != nil {
				return err
			}
			return exec.Command("git", "-C", dir, "commit", "-q", "-am", "agree with master").Run()
		},
		Outcome: done(),
	})
	o := f.options(t, agent)
	o.Verify = clash
	if _, err := Run(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	if got := git(t, f.main, "log", "-1", "--format=%s", "master"); got != "agree with master" {
		t.Fatalf("master should end at the repair, not %q", got)
	}
	if !strings.Contains(agent.Prompts[0], "failed twice") {
		t.Fatal("the prompt should carry the failure")
	}
}

func TestAFailingCheckWithoutAnAgentLeavesMasterAlone(t *testing.T) {
	f := setup(t, "start\n", "start\n")
	masterBefore := f.master(t)
	o := f.options(t, nil)
	o.Verify = clash
	if _, err := Run(context.Background(), o); err == nil {
		t.Fatal("a failing check must not land")
	}
	if f.master(t) != masterBefore {
		t.Fatal("master must not move")
	}
	if !strings.Contains(git(t, f.tree, "log", "--format=%s"), "master work") {
		t.Fatal("the branch stays rebased so the failure can be looked at")
	}
}

func TestARepairThatCommitsNothingIsRetried(t *testing.T) {
	f := setup(t, "start\n", "start\n")
	agent := fake.New(fake.Step{Outcome: done()})
	o := f.options(t, agent)
	o.Verify = clash
	o.Attempts = 2
	if _, err := Run(context.Background(), o); err == nil {
		t.Fatal("nothing repaired, nothing landed")
	}
	if agent.Started() != 2 || !strings.Contains(agent.Prompts[1], "committed nothing") {
		t.Fatalf("the second attempt should hear that the first committed nothing; %d sessions", agent.Started())
	}
}

func TestMasterMovingDuringTheChecksRebasesAgain(t *testing.T) {
	f := setup(t, "start\n", "start\n")
	flag := filepath.Join(t.TempDir(), "moved")
	o := f.options(t, nil)
	o.Verify = `if [ ! -f ` + flag + ` ]; then touch ` + flag + ` && echo late > ` + f.main + `/late.txt && ` +
		`git -C ` + f.main + ` add late.txt && git -C ` + f.main + ` commit -q -m late; fi`
	if _, err := Run(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	if got := git(t, f.main, "log", "--format=%s", "-3", "master"); got != "branch work\nlate\nmaster work" {
		t.Fatalf("the branch should land on top of the late commit:\n%s", got)
	}
}
