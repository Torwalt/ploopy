package ui

import (
	"errors"
	"fmt"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
)

// PickKeyed is Pick with a letter on each choice: pressing it chooses at once,
// and the arrows with Enter still work. Flag names the option a run with
// nobody watching should have passed instead.
func PickKeyed(title, flag string, choices []Choice) (string, error) {
	if !Interactive() {
		return "", NoTerminal(flag)
	}
	if len(choices) == 0 {
		return "", errors.New("nothing to choose from")
	}
	if len(choices) == 1 {
		return choices[0].Value, nil
	}

	picker := &keyPicker{title: title, choices: choices}
	if _, err := tea.NewProgram(picker).Run(); err != nil {
		return "", err
	}
	return picker.value, picker.err
}

type keyPicker struct {
	title   string
	choices []Choice
	cursor  int
	value   string
	err     error
	done    bool
}

func (p *keyPicker) Init() tea.Cmd { return nil }

func (p *keyPicker) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		return p, p.key(key)
	}
	return p, nil
}

func (p *keyPicker) key(msg tea.KeyMsg) tea.Cmd {
	if p.done {
		return nil
	}
	switch msg.Type {
	case tea.KeyEsc, tea.KeyCtrlC:
		return p.cancel()
	case tea.KeyEnter:
		return p.choose(p.choices[p.cursor].Value)
	case tea.KeyUp:
		p.move(-1)
	case tea.KeyDown:
		p.move(1)
	case tea.KeyRunes:
		for _, r := range msg.Runes {
			if cmd := p.rune(r); cmd != nil {
				return cmd
			}
		}
	}
	return nil
}

func (p *keyPicker) rune(r rune) tea.Cmd {
	for _, choice := range p.choices {
		if choice.Key != 0 && unicode.ToLower(choice.Key) == unicode.ToLower(r) {
			return p.choose(choice.Value)
		}
	}
	if r == 'q' {
		return p.cancel()
	}
	return nil
}

func (p *keyPicker) move(delta int) {
	n := len(p.choices)
	p.cursor = (p.cursor + delta + n) % n
}

func (p *keyPicker) choose(value string) tea.Cmd {
	p.value = value
	p.done = true
	return tea.Quit
}

func (p *keyPicker) cancel() tea.Cmd {
	p.err = huh.ErrUserAborted
	p.done = true
	return tea.Quit
}

// View implements tea.Model.
func (p *keyPicker) View() string {
	st := styles()
	out := []string{st.title.Render(p.title)}
	for i, choice := range p.choices {
		prefix := " "
		if i == p.cursor {
			prefix = ">"
		}
		text := fmt.Sprintf("%s    %s", prefix, choice.Label)
		if choice.Key != 0 {
			text = fmt.Sprintf("%s %c  %s", prefix, choice.Key, choice.Label)
		}
		if i == p.cursor {
			text = st.focusedField.Render(text)
		} else {
			text = st.field.Render(text)
		}
		out = append(out, text)
	}
	out = append(out, st.help.Render("enter choose   letter choose   esc"))
	return strings.Join(out, "\n")
}
