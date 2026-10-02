package ui

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/mattn/go-isatty"
	"golang.org/x/term"
)

// Finish is what happens to the machine when a run ends.
type Finish string

// The end actions a run can take.
const (
	FinishNone     Finish = "none"
	FinishSuspend  Finish = "suspend"
	FinishPoweroff Finish = "poweroff"
)

// Finishes are the choices offered by the selection form.
var Finishes = []Choice{
	{Label: "nothing — leave the machine as it is", Value: string(FinishNone)},
	{Label: "suspend the machine", Value: string(FinishSuspend)},
	{Label: "power the machine off", Value: string(FinishPoweroff)},
}

// Valid reports whether a string names an end action.
func (f Finish) Valid() bool {
	switch f {
	case FinishNone, FinishSuspend, FinishPoweroff:
		return true
	}
	return false
}

// Run carries out the end action, after a countdown any keypress cancels.
// It fires whether the run finished or blocked: an overnight run that stops at
// three in the morning should still leave the machine off.
func (f Finish) Run(out io.Writer, grace time.Duration) error {
	if f == FinishNone || f == "" {
		return nil
	}
	verb := map[Finish]string{FinishSuspend: "Suspending", FinishPoweroff: "Powering off"}[f]

	if countdown(out, verb, grace) {
		fmt.Fprintln(out, "cancelled; the machine stays up.")
		return nil
	}

	command := map[Finish]string{FinishSuspend: "suspend", FinishPoweroff: "poweroff"}[f]
	cmd := exec.Command("systemctl", command)
	cmd.Stdout, cmd.Stderr = out, out
	return cmd.Run()
}

// countdown reports whether the author cancelled.
func countdown(out io.Writer, verb string, grace time.Duration) bool {
	if grace <= 0 {
		return false
	}
	if !isatty.IsTerminal(os.Stdin.Fd()) {
		fmt.Fprintf(out, "\n%s in %s.\n", verb, grace.Round(time.Second))
		time.Sleep(grace)
		return false
	}

	state, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		time.Sleep(grace)
		return false
	}
	defer term.Restore(int(os.Stdin.Fd()), state)

	pressed := make(chan struct{})
	go func() {
		buf := make([]byte, 1)
		if _, err := os.Stdin.Read(buf); err == nil {
			close(pressed)
		}
	}()

	deadline := time.Now().Add(grace)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		left := time.Until(deadline).Round(time.Second)
		fmt.Fprintf(out, "\r%s in %3ds — press any key to cancel. ", verb, int(left.Seconds()))
		select {
		case <-pressed:
			fmt.Fprint(out, "\r\n")
			return true
		case <-ticker.C:
			if time.Now().After(deadline) {
				fmt.Fprint(out, "\r\n")
				return false
			}
		}
	}
}

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
