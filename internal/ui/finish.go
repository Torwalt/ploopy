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

// Acts reports whether the end action does anything to the machine.
func (f Finish) Acts() bool { return f == FinishSuspend || f == FinishPoweroff }

// Countdown waits out the grace period before the end action and reports
// whether the author cancelled it. It runs whether the run finished or
// blocked: an overnight run that stops at three in the morning should still
// leave the machine off.
func (f Finish) Countdown(out io.Writer, grace time.Duration) bool {
	verb := map[Finish]string{FinishSuspend: "Suspending", FinishPoweroff: "Powering off"}[f]
	return countdown(out, verb, grace)
}

// Execute carries out the end action.
func (f Finish) Execute(out io.Writer) error {
	if !f.Acts() {
		return nil
	}
	command := map[Finish]string{FinishSuspend: "suspend", FinishPoweroff: "poweroff"}[f]
	cmd := exec.Command("systemctl", command)
	cmd.Stdout, cmd.Stderr = out, out
	return cmd.Run()
}

// countdown reports whether the author cancelled. Only `c` or Ctrl-C does:
// whatever was typed into the terminal during the run is thrown away first,
// and the sequences a terminal sends on its own are skipped.
func countdown(out io.Writer, verb string, grace time.Duration) bool {
	if grace <= 0 {
		return false
	}
	fd := int(os.Stdin.Fd())
	if !isatty.IsTerminal(os.Stdin.Fd()) {
		fmt.Fprintf(out, "\n%s in %s.\n", verb, grace.Round(time.Second))
		time.Sleep(grace)
		return false
	}

	state, err := term.MakeRaw(fd)
	if err != nil {
		time.Sleep(grace)
		return false
	}
	defer term.Restore(fd, state)
	_ = flushInput(fd)

	pressed := make(chan struct{})
	go func() {
		var keys cancelKeys
		buf := make([]byte, 64)
		for {
			n, err := os.Stdin.Read(buf)
			for _, b := range buf[:n] {
				if keys.feed(b) {
					close(pressed)
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	deadline := time.Now().Add(grace)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		left := time.Until(deadline).Round(time.Second)
		fmt.Fprintf(out, "\r%s in %3ds — press c to cancel. ", verb, int(left.Seconds()))
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

// cancelKeys reads raw terminal input for the keys that cancel a countdown.
// Escape sequences are consumed whole: a focus report, a mouse event or a
// reply to a query can carry a `c` without anyone pressing it.
type cancelKeys struct {
	state int
}

const (
	keyPlain = iota
	keyEscape
	keyCSI       // ESC [ … final byte
	keyString    // OSC, DCS, APC, PM, SOS: until BEL or ST
	keyStringEsc // ESC seen inside a string, maybe the start of ST
	keySS3       // ESC O and one more byte
)

func (k *cancelKeys) feed(b byte) bool {
	switch k.state {
	case keyPlain:
		switch b {
		case 0x1b:
			k.state = keyEscape
		case 'c', 'C', 0x03:
			return true
		}
	case keyEscape:
		switch b {
		case '[':
			k.state = keyCSI
		case ']', 'P', '_', '^', 'X':
			k.state = keyString
		case 'O':
			k.state = keySS3
		case 0x1b:
		default:
			k.state = keyPlain
		}
	case keyCSI:
		if b >= 0x40 && b <= 0x7e {
			k.state = keyPlain
		}
	case keyString:
		switch b {
		case 0x07:
			k.state = keyPlain
		case 0x1b:
			k.state = keyStringEsc
		}
	case keyStringEsc:
		if b == '\\' {
			k.state = keyPlain
		} else {
			k.state = keyString
		}
	case keySS3:
		k.state = keyPlain
	}
	return false
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
