package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Torwalt/ploopy/internal/control"
	"github.com/spf13/cobra"
)

func fakePowerCommands(t *testing.T) string {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("requires Linux")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	t.Setenv("POWER_COMMAND_LOG", log)
	for _, name := range []string{"systemd-run", "systemctl"} {
		script := "#!/bin/sh\nif [ \"$2\" = show ]; then printf '%s\\n' \"${POWER_SERVICE_STATE:-inactive}\"; exit 0; fi\nprintf '%s\\n' \"$0\" \"$@\" >> \"$POWER_COMMAND_LOG\"\nexit \"${POWER_COMMAND_EXIT:-0}\"\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

func TestPoweroffArmsATransientUserServiceOutsideACheckout(t *testing.T) {
	log := fakePowerCommands(t)
	t.Chdir(t.TempDir())
	runtimeDir := filepath.Join(t.TempDir(), "runtime with spaces")
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	root := newRoot(&env{})
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"poweroff"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimSpace(string(raw)), "\n")
	want := []string{filepath.Join(filepath.Dir(log), "systemd-run"), "--user", "--unit=" + poweroffUnit,
		"--collect", "--property=Type=exec", "--expand-environment=no", "--setenv=PATH=" + os.Getenv("PATH"),
		"--", self, "poweroff", "watch", "--runtime-dir", control.Dir(),
		"--away-file", filepath.Join(runtimeDir, "away")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("start arguments:\n%q\nwant:\n%q", got, want)
	}
	if !strings.Contains(out.String(), "armed") {
		t.Fatalf("no confirmation: %q", out.String())
	}
}

func TestPoweroffStopDoesNotStartAService(t *testing.T) {
	log := fakePowerCommands(t)
	t.Chdir(t.TempDir())
	root := newRoot(&env{})
	root.SetArgs([]string{"poweroff", "stop"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(filepath.Dir(log), "systemctl") + "\n--user\nstop\n" + poweroffUnit + "\n"
	if string(raw) != want {
		t.Fatalf("stop called %q, want %q", raw, want)
	}
}

func TestPoweroffReportsServiceStartupFailure(t *testing.T) {
	fakePowerCommands(t)
	t.Setenv("POWER_COMMAND_EXIT", "1")
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	cmd := newPoweroff()
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Execute(); err == nil {
		t.Fatal("failed systemd-run was accepted")
	}
	if strings.Contains(out.String(), "armed") {
		t.Fatal("failed start was reported as armed")
	}
}

// fakeWatcher records what the watcher did; away answers each check in turn
// and repeats its last answer.
type fakeWatcher struct {
	busy             []bool
	away             []bool
	checks, offs     int
	notes            []string
	runErr, powerErr error
}

func (f *fakeWatcher) watcher() watcher {
	next := func(answers []bool, i int) bool {
		if i < len(answers) {
			return answers[i]
		}
		return answers[len(answers)-1]
	}
	return watcher{
		running: func() (bool, error) {
			f.checks++
			if f.runErr != nil {
				return false, f.runErr
			}
			return next(f.busy, f.checks-1), nil
		},
		away:   func() bool { return next(f.away, f.checks-1) },
		notify: func(msg string) { f.notes = append(f.notes, msg) },
		poweroff: func(context.Context) error {
			f.offs++
			return f.powerErr
		},
	}
}

func ticks(n int) chan time.Time {
	c := make(chan time.Time, n)
	for range n {
		c <- time.Time{}
	}
	return c
}

func TestWatcherPowersOffAfterTwoAwayChecksOnceThePlansStop(t *testing.T) {
	f := &fakeWatcher{busy: []bool{true, false}, away: []bool{true}}
	err := watchForIdle(context.Background(), ticks(5), f.watcher())
	if err != nil || f.checks != 3 || f.offs != 1 || len(f.notes) != 1 {
		t.Fatalf("checks=%d, poweroffs=%d, notes=%q, err=%v", f.checks, f.offs, f.notes, err)
	}
}

func TestWatcherDisarmsWhenTheAuthorIsHereAsThePlansStop(t *testing.T) {
	f := &fakeWatcher{busy: []bool{true, false}, away: []bool{true, false}}
	err := watchForIdle(context.Background(), ticks(5), f.watcher())
	if err != nil || f.checks != 2 || f.offs != 0 || len(f.notes) != 1 || !strings.Contains(f.notes[0], "disarmed") {
		t.Fatalf("checks=%d, poweroffs=%d, notes=%q, err=%v", f.checks, f.offs, f.notes, err)
	}
}

func TestWatcherDisarmsWhenTheAuthorReturnsDuringTheWarning(t *testing.T) {
	f := &fakeWatcher{busy: []bool{false}, away: []bool{true, false}}
	err := watchForIdle(context.Background(), ticks(5), f.watcher())
	if err != nil || f.offs != 0 || len(f.notes) != 2 || !strings.Contains(f.notes[1], "disarmed") {
		t.Fatalf("poweroffs=%d, notes=%q, err=%v", f.offs, f.notes, err)
	}
}

func TestWatcherWarnsAgainAfterANewRunStarts(t *testing.T) {
	f := &fakeWatcher{busy: []bool{false, true, false, false}, away: []bool{true}}
	err := watchForIdle(context.Background(), ticks(5), f.watcher())
	if err != nil || f.checks != 4 || f.offs != 1 || len(f.notes) != 2 {
		t.Fatalf("checks=%d, poweroffs=%d, notes=%q, err=%v", f.checks, f.offs, f.notes, err)
	}
}

func TestWatcherWaitsForFirstTickAndCanBeCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f := &fakeWatcher{busy: []bool{false}, away: []bool{true}}
	err := watchForIdle(ctx, nil, f.watcher())
	if !errors.Is(err, context.Canceled) || f.checks != 0 || f.offs != 0 {
		t.Fatalf("checks=%d, poweroffs=%d, err=%v", f.checks, f.offs, err)
	}
}

func TestWatcherFailsClosedOnRegistryErrors(t *testing.T) {
	broken := errors.New("cannot read registry")
	f := &fakeWatcher{busy: []bool{false}, away: []bool{true}, runErr: broken}
	err := watchForIdle(context.Background(), ticks(3), f.watcher())
	if !errors.Is(err, broken) || f.offs != 0 {
		t.Fatalf("poweroffs=%d, err=%v", f.offs, err)
	}
}

func TestWatcherReportsPoweroffFailureWithoutRetrying(t *testing.T) {
	denied := errors.New("poweroff denied")
	f := &fakeWatcher{busy: []bool{false}, away: []bool{true}, powerErr: denied}
	err := watchForIdle(context.Background(), ticks(4), f.watcher())
	if !errors.Is(err, denied) || f.offs != 1 {
		t.Fatalf("poweroffs=%d, err=%v", f.offs, err)
	}
}

func TestActivityCoversTheWholeCommandAndIsReleasedOnError(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	failed := errors.New("run failed")
	cmd := &cobra.Command{Use: "test", RunE: func(cmd *cobra.Command, _ []string) error {
		if busy, err := control.AnyRunning([]string{control.Dir()}); err != nil || !busy {
			t.Fatalf("command was not marked active: busy=%v, err=%v", busy, err)
		}
		return failed
	}}
	if err := withActivity(cmd).Execute(); !errors.Is(err, failed) {
		t.Fatal(err)
	}
	if busy, err := control.AnyRunning([]string{control.Dir()}); err != nil || busy {
		t.Fatalf("command left activity behind: busy=%v, err=%v", busy, err)
	}
}

func TestPoweroffAlreadyArmedDoesNotRestartTheCountdown(t *testing.T) {
	log := fakePowerCommands(t)
	t.Setenv("POWER_SERVICE_STATE", "active")
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	cmd := newPoweroff()
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(log); !os.IsNotExist(err) {
		t.Fatalf("armed watcher was changed: %q, %v", raw, err)
	}
}

func TestPoweroffRefusesAnUnknownWatcherState(t *testing.T) {
	log := fakePowerCommands(t)
	t.Setenv("POWER_SERVICE_STATE", "deactivating")
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	if err := newPoweroff().Execute(); err == nil {
		t.Fatal("armed while the watcher was stopping")
	}
	if raw, err := os.ReadFile(log); !os.IsNotExist(err) {
		t.Fatalf("started a watcher: %q, %v", raw, err)
	}
}

func TestRunPowerControlsAreGone(t *testing.T) {
	cmd := newRun(&env{})
	for _, flag := range []string{"finish", "grace"} {
		if cmd.Flags().Lookup(flag) != nil {
			t.Fatalf("run still offers --%s", flag)
		}
	}
	if newAdjust(&env{}).Flags().Lookup("finish") != nil {
		t.Fatal("adjust still offers --finish")
	}
}
