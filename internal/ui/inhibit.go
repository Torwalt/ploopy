package ui

import (
	"context"
	"os/exec"
)

// Inhibit holds off idle suspend for as long as the returned function is
// uncalled, so a lid or an idle timer cannot end an overnight run early.
func Inhibit(ctx context.Context, why string) func() {
	cmd := exec.CommandContext(ctx, "systemd-inhibit",
		"--what=idle:sleep", "--who=ploopy", "--why="+why, "--mode=block",
		"sleep", "infinity")
	if err := cmd.Start(); err != nil {
		return func() {}
	}
	return func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}
}
