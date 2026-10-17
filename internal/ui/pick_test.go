package ui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
)

func newKeyPicker() *keyPicker {
	return &keyPicker{title: "What now?", choices: []Choice{
		{Key: 'r', Label: "run a plan", Value: "run"},
		{Key: 'v', Label: "review a plan", Value: "review"},
		{Key: 's', Label: "status", Value: "status"},
	}}
}

func pressKey(p *keyPicker, msg tea.Msg) {
	p.Update(msg)
}

func TestKeyedPickLettersAndEnter(t *testing.T) {
	t.Run("a letter picks its command", func(t *testing.T) {
		p := newKeyPicker()
		pressKey(p, letter('v'))
		if p.err != nil {
			t.Fatalf("v returned %v", p.err)
		}
		if p.value != "review" {
			t.Fatalf("v picked %q, want review", p.value)
		}
	})

	t.Run("an unknown letter does nothing", func(t *testing.T) {
		p := newKeyPicker()
		pressKey(p, letter('z'))
		if p.done {
			t.Fatal("an unknown letter closed the picker")
		}
		if p.value != "" {
			t.Fatalf("an unknown letter picked %q", p.value)
		}
		if p.cursor != 0 {
			t.Fatalf("an unknown letter moved the cursor to %d", p.cursor)
		}
	})

	t.Run("enter picks the highlighted command", func(t *testing.T) {
		p := newKeyPicker()
		pressKey(p, plain(tea.KeyDown))
		pressKey(p, plain(tea.KeyEnter))
		if p.err != nil {
			t.Fatalf("enter returned %v", p.err)
		}
		if p.value != "review" {
			t.Fatalf("enter picked %q, want review", p.value)
		}
	})

	t.Run("esc cancels without a value", func(t *testing.T) {
		p := newKeyPicker()
		pressKey(p, plain(tea.KeyEsc))
		if !p.done {
			t.Fatal("esc left the picker open")
		}
		if !errors.Is(p.err, huh.ErrUserAborted) {
			t.Fatalf("esc returned %v, want huh.ErrUserAborted", p.err)
		}
		if p.value != "" {
			t.Fatalf("esc picked %q", p.value)
		}
	})

	t.Run("ctrl-c cancels without a value", func(t *testing.T) {
		p := newKeyPicker()
		pressKey(p, plain(tea.KeyCtrlC))
		if !p.done {
			t.Fatal("ctrl-c left the picker open")
		}
		if !errors.Is(p.err, huh.ErrUserAborted) {
			t.Fatalf("ctrl-c returned %v, want huh.ErrUserAborted", p.err)
		}
		if p.value != "" {
			t.Fatalf("ctrl-c picked %q", p.value)
		}
	})

	t.Run("the view shows each letter", func(t *testing.T) {
		p := newKeyPicker()
		for _, want := range []string{"r  run a plan", "v  review a plan", "s  status"} {
			if !strings.Contains(p.View(), want) {
				t.Fatalf("the view does not show %q:\n%s", want, p.View())
			}
		}
	})
}
