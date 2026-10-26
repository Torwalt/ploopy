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
		script := "#!/bin/sh\nprintf '%s\\n' \"$0\" \"$@\" >> \"$POWER_COMMAND_LOG\"\nexit \"${POWER_COMMAND_EXIT:-0}\"\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

func TestPoweroffArmsOnlyAManualTransientServiceOutsideACheckout(t *testing.T) {
	log := fakePowerCommands(t)
	t.Chdir(t.TempDir())
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(t.TempDir(), "runtime with spaces"))
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
	want := []string{filepath.Join(filepath.Dir(log), "systemd-run"), "--unit=" + poweroffUnit,
		"--collect", "--property=Type=exec", "--expand-environment=no", "--setenv=PATH=" + os.Getenv("PATH"),
		"--", self, "poweroff", "watch", "--runtime-dir", control.Dir()}
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
	want := filepath.Join(filepath.Dir(log), "systemctl") + "\nstop\n" + poweroffUnit + "\n"
	if string(raw) != want {
		t.Fatalf("stop called %q, want %q", raw, want)
	}
}

func TestPoweroffReportsServiceStartupFailure(t *testing.T) {
	fakePowerCommands(t)
	t.Setenv("POWER_COMMAND_EXIT", "1")
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

func TestWatcherWaitsThroughActivePlansThenPowersOffOnce(t *testing.T) {
	ticks := make(chan time.Time, 3)
	for range 3 {
		ticks <- time.Time{}
	}
	checks, shutdowns := 0, 0
	err := watchForIdle(context.Background(), ticks, func() (bool, error) {
		checks++
		return checks < 2, nil
	}, func(context.Context) error {
		shutdowns++
		return nil
	})
	if err != nil || checks != 2 || shutdowns != 1 {
		t.Fatalf("checks=%d, shutdowns=%d, err=%v", checks, shutdowns, err)
	}
}

func TestWatcherWaitsForFirstTickAndCanBeCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := watchForIdle(ctx, nil, func() (bool, error) {
		t.Fatal("checked before the first tick")
		return false, nil
	}, func(context.Context) error {
		t.Fatal("powered off after cancellation")
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestWatcherFailsClosedOnRegistryErrors(t *testing.T) {
	ticks := make(chan time.Time, 1)
	ticks <- time.Time{}
	broken := errors.New("cannot read registry")
	err := watchForIdle(context.Background(), ticks, func() (bool, error) {
		return false, broken
	}, func(context.Context) error {
		t.Fatal("powered off after a registry error")
		return nil
	})
	if !errors.Is(err, broken) {
		t.Fatal(err)
	}
}

func TestWatcherReportsPoweroffFailureWithoutRetrying(t *testing.T) {
	ticks := make(chan time.Time, 2)
	ticks <- time.Time{}
	ticks <- time.Time{}
	denied := errors.New("poweroff denied")
	calls := 0
	err := watchForIdle(context.Background(), ticks, func() (bool, error) {
		return false, nil
	}, func(context.Context) error {
		calls++
		return denied
	})
	if !errors.Is(err, denied) || calls != 1 {
		t.Fatalf("poweroff calls=%d, err=%v", calls, err)
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
