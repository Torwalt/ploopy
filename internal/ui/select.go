package ui

import (
	"errors"
	"fmt"
	"os"

	"github.com/charmbracelet/huh"
	"github.com/mattn/go-isatty"
)

// NoTerminal is what a run with nobody watching is told: name the choice as a
// flag instead.
func NoTerminal(flag string) error {
	return fmt.Errorf("nothing to answer with: pass --%s", flag)
}

// Choice is one option offered by the selection form. Key is the letter that
// picks it on a keyed pick; zero shows no letter.
type Choice struct {
	Key   rune
	Label string
	Value string
}

// Interactive reports whether the author is at the keyboard.
func Interactive() bool {
	return isatty.IsTerminal(os.Stdin.Fd()) && isatty.IsTerminal(os.Stderr.Fd())
}

// Pick asks the author to choose one of a list. Flag names the option a run
// with nobody watching should have passed instead.
func Pick(title, flag string, choices []Choice) (string, error) {
	if !Interactive() {
		return "", NoTerminal(flag)
	}
	if len(choices) == 0 {
		return "", errors.New("nothing to choose from")
	}
	if len(choices) == 1 {
		return choices[0].Value, nil
	}

	options := make([]huh.Option[string], 0, len(choices))
	for _, choice := range choices {
		options = append(options, huh.NewOption(choice.Label, choice.Value))
	}

	var picked string
	err := huh.NewSelect[string]().
		Title(title).
		Options(options...).
		Value(&picked).
		WithTheme(huh.ThemeBase16()).
		Run()
	if err != nil {
		return "", err
	}
	return picked, nil
}

// PickMany asks the author to choose any of a list, every one chosen to
// begin with. The choices come back in the order offered. Flag names the
// option a run with nobody watching should have passed instead.
func PickMany(title, flag string, choices []Choice) ([]string, error) {
	if !Interactive() {
		return nil, NoTerminal(flag)
	}
	if len(choices) == 0 {
		return nil, errors.New("nothing to choose from")
	}
	options := make([]huh.Option[string], 0, len(choices))
	for _, choice := range choices {
		options = append(options, huh.NewOption(choice.Label, choice.Value).Selected(true))
	}
	var picked []string
	err := huh.NewMultiSelect[string]().
		Title(title).
		Options(options...).
		Value(&picked).
		WithTheme(huh.ThemeBase16()).
		Run()
	if err != nil {
		return nil, err
	}
	if len(picked) == 0 {
		return nil, errors.New("no branch picked")
	}
	chosen := map[string]bool{}
	for _, value := range picked {
		chosen[value] = true
	}
	var ordered []string
	for _, choice := range choices {
		if chosen[choice.Value] {
			ordered = append(ordered, choice.Value)
		}
	}
	return ordered, nil
}
