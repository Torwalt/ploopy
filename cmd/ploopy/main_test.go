package main

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/Torwalt/ploopy/internal/control"
)

type menuEntry struct {
	key   rune
	label string
}

func readmeMenu(t *testing.T) []menuEntry {
	t.Helper()
	data, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	var entries []menuEntry
	inMenu := false
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if !inMenu {
			if trimmed == "What now?" {
				inMenu = true
			}
			continue
		}
		if trimmed == "" {
			break
		}
		fields := strings.Fields(strings.TrimPrefix(trimmed, ">"))
		if len(fields) < 2 || len(fields[0]) != 1 {
			t.Fatalf("cannot read menu line %q", line)
		}
		entries = append(entries, menuEntry{
			key:   rune(fields[0][0]),
			label: strings.Join(fields[1:], " "),
		})
	}
	if len(entries) == 0 {
		t.Fatal("the README's Start menu was not found")
	}
	return entries
}

func TestTheMenuMatchesTheREADMEsStartList(t *testing.T) {
	want := readmeMenu(t)
	choices := commandChoices(nil)
	if len(choices) != len(want) {
		t.Fatalf("the menu has %d choices, the README lists %d", len(choices), len(want))
	}
	seen := map[rune]bool{}
	for i, choice := range choices {
		if seen[choice.Key] {
			t.Errorf("two choices use the letter %c", choice.Key)
		}
		seen[choice.Key] = true
		if choice.Key != want[i].key || choice.Label != want[i].label {
			t.Errorf("choice %d is %c %q, the README lists %c %q",
				i, choice.Key, choice.Label, want[i].key, want[i].label)
		}
		if choice.Value == "adjust" {
			t.Error("the menu offers adjust with no run going on")
		}
	}
}

func TestTheMenuOffersAdjustExactlyWhileARunGoesOn(t *testing.T) {
	static := commandChoices(nil)
	withRun := commandChoices([]control.Live{{}})
	if len(withRun) != len(static)+1 {
		t.Fatalf("with a run the menu has %d choices, without it %d", len(withRun), len(static))
	}
	if withRun[0].Value != "adjust" || withRun[0].Key != 'a' {
		t.Fatalf("with a run the first choice is %c %q, want a adjust", withRun[0].Key, withRun[0].Value)
	}
	for i, choice := range static {
		if withRun[i+1].Value != choice.Value || withRun[i+1].Key != choice.Key {
			t.Errorf("choice %d changed with a run: %c %q, was %c %q",
				i+1, withRun[i+1].Key, withRun[i+1].Value, choice.Key, choice.Value)
		}
	}
}

func TestOnlyThisRepositorysRunsAreListed(t *testing.T) {
	e, tree := worktreeRepo(t, nil)
	other := t.TempDir()
	if _, err := control.Register(control.Status{Root: other, Plan: "docs/plans/OTHER.md"}); err != nil {
		t.Fatal(err)
	}
	if lives, err := livesIn(context.Background(), e.root); err != nil || len(lives) != 0 {
		t.Fatalf("another repository's run is listed here: %v, %v", lives, err)
	}
	if _, err := control.Register(control.Status{Root: tree, Plan: "docs/plans/PASS.md"}); err != nil {
		t.Fatal(err)
	}
	lives, err := livesIn(context.Background(), e.root)
	if err != nil || len(lives) != 1 || lives[0].Plan != "docs/plans/PASS.md" {
		t.Fatalf("the run in this repository's worktree: %v, %v", lives, err)
	}
}
