package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
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
		Short: "Power off when every plan has stopped (check every minute)",
		Long: "Manually arm a transient systemd service that checks every 60 seconds\n" +
			"whether any ploopy plan is running, across repositories and users.\n" +
			"The first check is after 60 seconds. If idle, it requests poweroff.\n\n" +
			"Nothing is installed or enabled at boot. Starting the system service\n" +
			"requires systemd authorization (or sudo). Cancel with `ploopy poweroff stop`.\n" +
			"Runs are found in /run/user/*/ploopy, /tmp/ploopy-<uid>, and the\n" +
			"caller's runtime directory. Other users' custom directories are not scanned.",
		Args: cobra.NoArgs,
		PersistentPreRunE: func(_ *cobra.Command, _ []string) error {
			if runtime.GOOS != "linux" {
				return fmt.Errorf("poweroff watcher requires Linux and systemd")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			self, err := os.Executable()
			if err != nil {
				return err
			}
			args := []string{"--unit=" + poweroffUnit, "--collect", "--property=Type=exec",
				"--expand-environment=no", "--setenv=PATH=" + os.Getenv("PATH"),
				"--", self, "poweroff", "watch", "--runtime-dir", control.Dir()}
			if err := poweroffCommand(cmd.Context(), "systemd-run", args...); err != nil {
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
			return poweroffCommand(cmd.Context(), "systemctl", "stop", poweroffUnit)
		},
	})
	var dir string
	worker := &cobra.Command{
		Use:    "watch",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ticker := time.NewTicker(time.Minute)
			defer ticker.Stop()
			return watchForIdle(cmd.Context(), ticker.C, func() (bool, error) {
				dirs, err := control.MachineDirs(dir)
				if err != nil {
					return false, err
				}
				return control.AnyRunning(dirs)
			}, func(ctx context.Context) error {
				return poweroffCommand(ctx, "systemctl", "--no-ask-password", "poweroff")
			})
		},
	}
	worker.Flags().StringVar(&dir, "runtime-dir", "", "caller's run registry")
	_ = worker.MarkFlagRequired("runtime-dir")
	cmd.AddCommand(worker)
	return cmd
}

func poweroffCommand(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w: %s", name, err, out)
	}
	return nil
}

func watchForIdle(ctx context.Context, ticks <-chan time.Time, running func() (bool, error), poweroff func(context.Context) error) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticks:
			// A stop racing a tick must not initiate shutdown.
			if err := ctx.Err(); err != nil {
				return err
			}
			busy, err := running()
			if err != nil {
				return err
			}
			if !busy {
				if err := ctx.Err(); err != nil {
					return err
				}
				return poweroff(ctx)
			}
		}
	}
}
