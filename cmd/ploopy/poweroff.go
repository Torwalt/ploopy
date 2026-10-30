package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/Torwalt/ploopy/internal/control"
	"github.com/Torwalt/ploopy/internal/ui"
)

const poweroffUnit = "ploopy-poweroff.service"

func newPoweroff() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "poweroff",
		Short: "Power off once every plan has stopped and you are away",
		Long: "Arm a transient systemd user service that checks every 60 seconds\n" +
			"whether any ploopy plan is running. Once none is, it powers off only\n" +
			"if $XDG_RUNTIME_DIR/away exists, which an idle daemon such as hypridle\n" +
			"creates after a stretch without input and removes on return.\n\n" +
			"Away when the plans finish: a notification, one more minute, then\n" +
			"poweroff unless you came back. Present: it disarms itself instead.\n" +
			"Nothing is installed or enabled at boot. Cancel with `ploopy poweroff stop`.",
		Args: cobra.NoArgs,
		PersistentPreRunE: func(_ *cobra.Command, _ []string) error {
			if runtime.GOOS != "linux" {
				return fmt.Errorf("poweroff watcher requires Linux and systemd")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
			if runtimeDir == "" {
				return fmt.Errorf("poweroff watcher needs XDG_RUNTIME_DIR to find the away marker")
			}
			armed, err := poweroffArmed(cmd.Context())
			if err != nil {
				return err
			}
			if armed {
				ui.PoweroffArmed(cmd.OutOrStdout())
				return nil
			}
			self, err := os.Executable()
			if err != nil {
				return err
			}
			args := []string{"--user", "--unit=" + poweroffUnit, "--collect", "--property=Type=exec",
				"--expand-environment=no", "--setenv=PATH=" + os.Getenv("PATH"),
				"--", self, "poweroff", "watch", "--runtime-dir", control.Dir(),
				"--away-file", filepath.Join(runtimeDir, "away")}
			if err := poweroffCommand(cmd.Context(), "systemd-run", args...); err != nil {
				// A second arming may have raced this one.
				if armed, _ := poweroffArmed(cmd.Context()); armed {
					ui.PoweroffArmed(cmd.OutOrStdout())
					return nil
				}
				return err
			}
			ui.PoweroffArmed(cmd.OutOrStdout())
			return nil
		},
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "stop",
		Short: "Cancel the shutdown watcher",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return poweroffCommand(cmd.Context(), "systemctl", "--user", "stop", poweroffUnit)
		},
	})
	var dir, awayFile string
	worker := &cobra.Command{
		Use:    "watch",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ticker := time.NewTicker(time.Minute)
			defer ticker.Stop()
			return watchForIdle(cmd.Context(), ticker.C, watcher{
				running: func() (bool, error) {
					dirs, err := control.MachineDirs(dir)
					if err != nil {
						return false, err
					}
					return control.AnyRunning(dirs)
				},
				away: func() bool {
					_, err := os.Stat(awayFile)
					return err == nil
				},
				notify: func(msg string) {
					fmt.Fprintln(cmd.OutOrStdout(), msg)
					// Best effort: the journal keeps the message either way.
					_ = poweroffCommand(cmd.Context(), "notify-send", "--app-name=ploopy", "ploopy", msg)
				},
				poweroff: func(ctx context.Context) error {
					return poweroffCommand(ctx, "systemctl", "--no-ask-password", "poweroff")
				},
			})
		},
	}
	worker.Flags().StringVar(&dir, "runtime-dir", "", "caller's run registry")
	worker.Flags().StringVar(&awayFile, "away-file", "", "marker an idle daemon keeps while the author is away")
	_ = worker.MarkFlagRequired("runtime-dir")
	_ = worker.MarkFlagRequired("away-file")
	cmd.AddCommand(worker)
	return cmd
}

func poweroffCommand(ctx context.Context, name string, args ...string) error {
	_, err := poweroffOutput(ctx, name, args...)
	return err
}

func poweroffArmed(ctx context.Context) (bool, error) {
	out, err := poweroffOutput(ctx, "systemctl", "--user", "show", "--property=ActiveState", "--value", poweroffUnit)
	if err != nil {
		return false, err
	}
	switch strings.TrimSpace(string(out)) {
	case "active", "activating", "reloading":
		return true, nil
	case "inactive", "failed":
		return false, nil
	default:
		return false, fmt.Errorf("cannot arm poweroff watcher in state %q", strings.TrimSpace(string(out)))
	}
}

func poweroffOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("%s: %w: %s", name, err, out)
	}
	return out, nil
}

// watcher is what the shutdown watcher asks of the machine.
type watcher struct {
	running  func() (bool, error)
	away     func() bool
	notify   func(string)
	poweroff func(context.Context) error
}

// watchForIdle powers off only once no plan runs and the author has been away
// for two checks in a row. Finding the author present after the plans finished
// disarms it: a shutdown armed overnight must not fire into the next workday.
func watchForIdle(ctx context.Context, ticks <-chan time.Time, w watcher) error {
	warned := false
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticks:
			// A stop racing a tick must not initiate shutdown.
			if err := ctx.Err(); err != nil {
				return err
			}
			busy, err := w.running()
			if err != nil {
				return err
			}
			switch {
			case busy:
				warned = false
			case !w.away():
				w.notify("Plans finished while you are here; the shutdown watcher is disarmed.")
				return nil
			case !warned:
				w.notify("Plans finished. Powering off in a minute unless you come back.")
				warned = true
			default:
				if err := ctx.Err(); err != nil {
					return err
				}
				return w.poweroff(ctx)
			}
		}
	}
}
