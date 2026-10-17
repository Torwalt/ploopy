package ui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func press(b *Board, msg tea.Msg) {
	b.Update(msg)
}

func letter(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
}

func plain(kind tea.KeyType) tea.KeyMsg {
	return tea.KeyMsg{Type: kind}
}

func choiceField(key rune, label string, value *string, values ...string) BoardField {
	choices := make([]Choice, 0, len(values))
	for _, v := range values {
		choices = append(choices, Choice{Label: v, Value: v})
	}
	return BoardField{
		Key:     key,
		Label:   label,
		Value:   value,
		Choices: func() []Choice { return choices },
	}
}

func TestColumnsTakeFocusByNumberAndTab(t *testing.T) {
	board := NewBoard("board", []Column{{Title: "one"}, {Title: "two"}, {Title: "three"}}, nil)

	press(board, letter('3'))
	if board.focus != 2 {
		t.Fatalf("number 3 focused column %d", board.focus)
	}
	press(board, plain(tea.KeyTab))
	if board.focus != 0 {
		t.Fatalf("Tab from the last column focused %d", board.focus)
	}
	press(board, plain(tea.KeyShiftTab))
	if board.focus != 2 {
		t.Fatalf("Shift+Tab from the first column focused %d", board.focus)
	}
	press(board, letter('1'))
	if board.focus != 0 {
		t.Fatalf("number 1 focused column %d", board.focus)
	}
}

func TestALetterCyclesForwardAndShiftBack(t *testing.T) {
	value := "a"
	board := NewBoard("board", []Column{{Title: "one", Fields: []BoardField{
		choiceField('m', "model", &value, "a", "b", "c"),
	}}}, nil)

	press(board, letter('m'))
	if value != "b" {
		t.Fatalf("m cycled to %q, want b", value)
	}
	press(board, letter('M'))
	if value != "a" {
		t.Fatalf("M cycled back to %q, want a", value)
	}
	press(board, letter('M'))
	if value != "c" {
		t.Fatalf("M at the first choice cycled to %q, want c", value)
	}
}

func TestARepeatedLetterActsOnlyInTheFocusedColumn(t *testing.T) {
	first, second := "a", "a"
	board := NewBoard("board", []Column{
		{Title: "one", Fields: []BoardField{choiceField('m', "model", &first, "a", "b")}},
		{Title: "two", Fields: []BoardField{choiceField('m', "model", &second, "a", "b")}},
	}, nil)

	press(board, letter('m'))
	if first != "b" || second != "a" {
		t.Fatalf("first column m gave %q and %q", first, second)
	}
	press(board, letter('2'))
	press(board, letter('m'))
	if first != "b" || second != "b" {
		t.Fatalf("second column m gave %q and %q", first, second)
	}
}

func TestArrowsMoveTheCursorAndCycleTheFieldUnderIt(t *testing.T) {
	model, effort := "a", "low"
	board := NewBoard("board", []Column{{Title: "one", Fields: []BoardField{
		choiceField('m', "model", &model, "a", "b"),
		choiceField('e', "effort", &effort, "low", "high"),
	}}}, nil)

	press(board, plain(tea.KeyDown))
	if board.cursor[0] != 1 {
		t.Fatalf("down left the cursor at %d", board.cursor[0])
	}
	press(board, plain(tea.KeyRight))
	if effort != "high" {
		t.Fatalf("right cycled effort to %q", effort)
	}
	press(board, plain(tea.KeyLeft))
	if effort != "low" {
		t.Fatalf("left cycled effort to %q", effort)
	}
	press(board, plain(tea.KeyUp))
	if board.cursor[0] != 0 {
		t.Fatalf("up left the cursor at %d", board.cursor[0])
	}
	press(board, plain(tea.KeyRight))
	if model != "b" {
		t.Fatalf("right cycled model to %q", model)
	}
}

func TestALockedFieldKeepsItsValueAndSaysWhy(t *testing.T) {
	value := "high"
	field := choiceField('e', "effort", &value, "high", "low")
	field.Locked = "from a flag"
	board := NewBoard("board", []Column{{Title: "one", Fields: []BoardField{field}}}, nil)

	press(board, letter('e'))
	if value != "high" {
		t.Fatalf("a locked field changed to %q", value)
	}
	if !strings.Contains(board.View(), "from a flag") {
		t.Fatalf("the view does not give the lock reason:\n%s", board.View())
	}

	board.notice = ""
	press(board, plain(tea.KeyRight))
	if value != "high" {
		t.Fatalf("an arrow changed a locked field to %q", value)
	}
	if !strings.Contains(board.View(), "from a flag") {
		t.Fatalf("an arrow did not give the lock reason:\n%s", board.View())
	}
}

func TestFocusOutranksDimAndLock(t *testing.T) {
	t.Cleanup(func() { lipgloss.SetColorProfile(termenv.Ascii) })
	lipgloss.SetColorProfile(termenv.ANSI)

	lent := "mine"
	field := choiceField('u', "lent", &lent, "mine", "until 09:00")
	field.Locked = "from a flag"
	board := NewBoard("board", []Column{
		{Title: "Bedrock"},
		{Title: "claude", Fields: []BoardField{field}, Dim: true},
	}, nil)
	press(board, letter('2'))

	st := styles()
	view := board.View()
	if want := st.focusedColumn.Render("2 claude"); !strings.Contains(view, want) {
		t.Fatalf("the focused dim column's title is not in the focused style:\n%q\n%s", want, view)
	}
	if want := st.focusedField.Render(" u lent  mine"); !strings.Contains(view, want) {
		t.Fatalf("the cursor's locked field is not in the focused style:\n%q\n%s", want, view)
	}
}

func TestTimeFieldsMoveTheLeaseHours(t *testing.T) {
	until := "until 09:00"
	board := NewBoard("board", []Column{{Title: "claude", Fields: []BoardField{
		{Key: 'u', Label: "lent", Value: &until},
	}}}, nil)

	press(board, letter('+'))
	if until != "until 10:00" {
		t.Fatalf("+ gave %q", until)
	}
	press(board, letter('-'))
	if until != "until 09:00" {
		t.Fatalf("- gave %q", until)
	}
	until = "until 00:00"
	press(board, letter('-'))
	if until != "until 23:00" {
		t.Fatalf("- at midnight gave %q", until)
	}

	span := "nightly 21:00-09:00"
	board = NewBoard("board", []Column{{Title: "claude", Fields: []BoardField{
		{Key: 'u', Label: "lent", Value: &span},
	}}}, nil)

	press(board, letter('+'))
	if span != "nightly 21:00-10:00" {
		t.Fatalf("+ gave %q", span)
	}
	press(board, letter('-'))
	if span != "nightly 21:00-09:00" {
		t.Fatalf("- gave %q", span)
	}
	press(board, letter(']'))
	if span != "nightly 22:00-09:00" {
		t.Fatalf("] gave %q", span)
	}
	press(board, letter('['))
	if span != "nightly 21:00-09:00" {
		t.Fatalf("[ gave %q", span)
	}

	press(board, letter(']'))
	press(board, letter(']'))
	press(board, letter(']'))
	if span != "nightly 00:00-09:00" {
		t.Fatalf("] at midnight gave %q", span)
	}
}

func TestTheFooterIsRecomputedAfterAChange(t *testing.T) {
	value := "a"
	calls := 0
	board := NewBoard("board", []Column{{Title: "one", Fields: []BoardField{
		choiceField('m', "model", &value, "a", "b"),
	}}}, func() []string {
		calls++
		return []string{"footer " + value}
	})

	if !strings.Contains(board.View(), "footer a") {
		t.Fatalf("the first view lacks the footer:\n%s", board.View())
	}
	before := calls
	press(board, letter('m'))
	if calls <= before {
		t.Fatal("the footer was not recomputed after a change")
	}
	if !strings.Contains(board.View(), "footer b") {
		t.Fatalf("the view has a stale footer:\n%s", board.View())
	}
}

func TestEnterReturnsTheValues(t *testing.T) {
	first, second := "a", "x"
	board := NewBoard("board", []Column{
		{Title: "one", Fields: []BoardField{choiceField('m', "model", &first, "a", "b")}},
		{Title: "two", Fields: []BoardField{choiceField('e', "effort", &second, "x", "y")}},
	}, nil)

	press(board, letter('m'))
	press(board, plain(tea.KeyEnter))
	values, err := board.Result()
	if err != nil {
		t.Fatalf("enter returned %v", err)
	}
	if len(values) != 2 || values[0] != "b" || values[1] != "x" {
		t.Fatalf("enter returned %q", values)
	}
}

func TestEscReturnsUserAborted(t *testing.T) {
	for name, msg := range map[string]tea.KeyMsg{
		"esc":    plain(tea.KeyEsc),
		"q":      letter('q'),
		"ctrl-c": plain(tea.KeyCtrlC),
	} {
		board := NewBoard("board", []Column{{Title: "one"}}, nil)
		press(board, msg)
		if _, err := board.Result(); !errors.Is(err, huh.ErrUserAborted) {
			t.Fatalf("%s returned %v", name, err)
		}
	}
}

func TestColumnsSitBesideEachOtherWhenWideAndStackWhenNarrow(t *testing.T) {
	agent, lent, subs := "opencode", "until 09:00", "use lent"
	board := NewBoard("ploopy · PASS", []Column{
		{Title: "Bedrock", Fields: []BoardField{
			choiceField('a', "agent", &agent, "opencode", "claude"),
			choiceField('m', "model", newString("deepseek/deepseek-flash"), "deepseek/deepseek-flash"),
		}},
		{Title: "claude", Fields: []BoardField{
			{Key: 'u', Label: "lent", Value: &lent},
		}},
		{Title: "Run", Fields: []BoardField{
			choiceField('s', "subs", &subs, "use lent", "bedrock only"),
		}},
	}, nil)

	press(board, tea.WindowSizeMsg{Width: 120})
	wide := board.View()
	if !sameLine(wide, "Bedrock", "claude") || !sameLine(wide, "claude", "Run") {
		t.Fatalf("at width 120 the columns are not side by side:\n%s", wide)
	}

	press(board, tea.WindowSizeMsg{Width: 60})
	narrow := board.View()
	if sameLine(narrow, "Bedrock", "claude") {
		t.Fatalf("at width 60 the columns are not stacked:\n%s", narrow)
	}
	if strings.Index(narrow, "Bedrock") > strings.Index(narrow, "claude") {
		t.Fatalf("at width 60 the columns are out of order:\n%s", narrow)
	}
}

func newString(value string) *string { return &value }

func sameLine(view string, left, right string) bool {
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, left) && strings.Contains(line, right) {
			return true
		}
	}
	return false
}
