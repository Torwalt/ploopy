package ui

import "testing"

func cancels(input string) bool {
	var keys cancelKeys
	for i := 0; i < len(input); i++ {
		if keys.feed(input[i]) {
			return true
		}
	}
	return false
}

func TestOnlyCOrCtrlCCancelsTheCountdown(t *testing.T) {
	for _, input := range []string{"c", "C", "\x03", "xyc"} {
		if !cancels(input) {
			t.Fatalf("%q should cancel", input)
		}
	}
	for _, input := range []string{"", "x", "\r", "\n", " ", "q"} {
		if cancels(input) {
			t.Fatalf("%q should not cancel", input)
		}
	}
}

// A terminal answers queries and reports focus on its own; none of it is the
// author at the keyboard.
func TestSequencesATerminalSendsDoNotCancel(t *testing.T) {
	for name, input := range map[string]string{
		"focus in":             "\x1b[I",
		"focus out":            "\x1b[O",
		"device attributes":    "\x1b[?62;22c",
		"background colour":    "\x1b]11;rgb:1c1c/1c1c/1c1c\x07",
		"colour with ST":       "\x1b]11;rgb:cccc/cccc/cccc\x1b\\",
		"mouse":                "\x1b[<0;12;5M",
		"application cursor":   "\x1bOC",
		"alt-c":                "\x1bc",
		"device control reply": "\x1bP1$r0m\x1b\\",
	} {
		if cancels(input) {
			t.Fatalf("%s %q cancelled the countdown", name, input)
		}
	}
	if !cancels("\x1b[I" + "c") {
		t.Fatal("a key pressed after a focus report should still cancel")
	}
}
