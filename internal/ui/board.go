package ui

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
)

// Column is one column on the board.
type Column struct {
	Title  string
	Fields []BoardField
	Lines  []string // read-only lines under the fields, such as window status
	Dim    bool     // not part of this run
}

// BoardField is one setting in a column. Choices are read again on every
// render, as Field does with Binding. A value that holds a clock or a span
// also answers +/-, and [/], without needing a choice list.
type BoardField struct {
	Key     rune
	Label   string
	Value   *string
	Choices func() []Choice
	Locked  string // why the field cannot change; empty when editable
}

// Board is the keyboard board. Enter returns the values in column and field
// order; Esc, q and ctrl-c return huh.ErrUserAborted.
type Board struct {
	Title    string
	Columns  []Column
	Footer   func() []string
	OnChange func() // runs before the footer after focus, cursor or a value changes

	width  int
	focus  int
	cursor []int
	footer []string
	notice string
	values []string
	err    error
	done   bool
}

// NewBoard builds a board focused on its first column.
func NewBoard(title string, columns []Column, footer func() []string) *Board {
	b := &Board{
		Title:   title,
		Columns: columns,
		Footer:  footer,
		cursor:  make([]int, len(columns)),
	}
	b.refreshFooter()
	return b
}

// SetColumns replaces the columns, keeping the focus and cursor where they
// still point. A caller whose columns depend on a value rebuilds them here.
func (b *Board) SetColumns(columns []Column) {
	b.Columns = columns
	if len(b.cursor) < len(columns) {
		b.cursor = append(b.cursor, make([]int, len(columns)-len(b.cursor))...)
	} else {
		b.cursor = b.cursor[:len(columns)]
	}
	for i := range b.cursor {
		fields := len(columns[i].Fields)
		if b.cursor[i] >= fields {
			b.cursor[i] = fields - 1
		}
		if b.cursor[i] < 0 {
			b.cursor[i] = 0
		}
	}
	if b.focus >= len(columns) {
		b.focus = len(columns) - 1
	}
	if b.focus < 0 {
		b.focus = 0
	}
}

// Init implements tea.Model.
func (b *Board) Init() tea.Cmd { return nil }

// Update implements tea.Model.
func (b *Board) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		b.width = msg.Width
		return b, nil
	case tea.KeyMsg:
		return b, b.key(msg)
	}
	return b, nil
}

// Result is what the board returned when it closed.
func (b *Board) Result() ([]string, error) {
	return append([]string(nil), b.values...), b.err
}

// Run shows the board and waits for the author to start or cancel.
func (b *Board) Run() ([]string, error) {
	if _, err := tea.NewProgram(b).Run(); err != nil {
		return nil, err
	}
	return b.Result()
}

func (b *Board) key(msg tea.KeyMsg) tea.Cmd {
	if b.done {
		return nil
	}
	switch msg.Type {
	case tea.KeyEsc, tea.KeyCtrlC:
		return b.cancel()
	case tea.KeyEnter:
		b.values = b.snapshot()
		b.done = true
		return tea.Quit
	case tea.KeyTab:
		b.moveFocus(1)
	case tea.KeyShiftTab:
		b.moveFocus(-1)
	case tea.KeyUp:
		b.moveCursor(-1)
	case tea.KeyDown:
		b.moveCursor(1)
	case tea.KeyLeft:
		b.cycleCursor(-1)
	case tea.KeyRight:
		b.cycleCursor(1)
	case tea.KeyRunes:
		for _, r := range msg.Runes {
			if cmd := b.rune(r); cmd != nil {
				return cmd
			}
		}
	}
	return nil
}

func (b *Board) cancel() tea.Cmd {
	b.err = huh.ErrUserAborted
	b.done = true
	return tea.Quit
}

func (b *Board) rune(r rune) tea.Cmd {
	switch r {
	case 'q':
		return b.cancel()
	case '+':
		b.adjustTime(1, false)
		return nil
	case '-':
		b.adjustTime(-1, false)
		return nil
	case ']':
		b.adjustTime(1, true)
		return nil
	case '[':
		b.adjustTime(-1, true)
		return nil
	}
	if r >= '1' && r <= '9' {
		b.setFocus(int(r - '1'))
		return nil
	}
	if unicode.IsLetter(r) {
		direction := 1
		if unicode.IsUpper(r) {
			direction = -1
		}
		b.cycleLetter(r, direction)
	}
	return nil
}

func (b *Board) setFocus(column int) {
	if column < 0 || column >= len(b.Columns) {
		return
	}
	b.focus = column
	b.notice = ""
	b.changed()
}

func (b *Board) moveFocus(delta int) {
	n := len(b.Columns)
	if n == 0 {
		return
	}
	b.setFocus((b.focus + delta + n) % n)
}

func (b *Board) moveCursor(delta int) {
	fields := b.focused().Fields
	if len(fields) == 0 {
		return
	}
	cursor := b.cursor[b.focus] + delta
	if cursor < 0 {
		cursor = 0
	}
	if cursor >= len(fields) {
		cursor = len(fields) - 1
	}
	b.cursor[b.focus] = cursor
	b.notice = ""
	b.changed()
}

func (b *Board) cycleCursor(direction int) {
	fields := b.focused().Fields
	if len(fields) == 0 {
		return
	}
	b.cycle(&fields[b.cursor[b.focus]], direction)
}

func (b *Board) cycleLetter(r rune, direction int) {
	for i := range b.focused().Fields {
		field := &b.focused().Fields[i]
		if unicode.ToLower(field.Key) == unicode.ToLower(r) {
			b.cycle(field, direction)
			return
		}
	}
}

func (b *Board) cycle(field *BoardField, direction int) {
	if field.Locked != "" {
		b.notice = field.Locked
		return
	}
	if field.Choices == nil || field.Value == nil {
		return
	}
	choices := field.Choices()
	if len(choices) == 0 {
		return
	}
	current := -1
	for i, choice := range choices {
		if choice.Value == *field.Value {
			current = i
			break
		}
	}
	next := 0
	if direction < 0 {
		next = len(choices) - 1
	}
	if current >= 0 {
		next = (current + direction + len(choices)) % len(choices)
	}
	*field.Value = choices[next].Value
	b.notice = ""
	b.changed()
}

func (b *Board) adjustTime(direction int, start bool) {
	field := b.timeField()
	if field == nil || field.Value == nil {
		return
	}
	if field.Locked != "" {
		b.notice = field.Locked
		return
	}
	value, ok := adjustClock(*field.Value, start, direction)
	if !ok {
		return
	}
	*field.Value = value
	b.notice = ""
	b.changed()
}

func (b *Board) timeField() *BoardField {
	fields := b.focused().Fields
	if len(fields) == 0 {
		return nil
	}
	cursor := b.cursor[b.focus]
	if hasClock(fields[cursor].Value) {
		return &fields[cursor]
	}
	for i := range fields {
		if hasClock(fields[i].Value) {
			return &fields[i]
		}
	}
	return nil
}

func (b *Board) focused() *Column {
	if len(b.Columns) == 0 {
		return &Column{}
	}
	return &b.Columns[b.focus]
}

func (b *Board) snapshot() []string {
	var values []string
	for _, column := range b.Columns {
		for _, field := range column.Fields {
			value := ""
			if field.Value != nil {
				value = *field.Value
			}
			values = append(values, value)
		}
	}
	return values
}

func (b *Board) changed() {
	if b.OnChange != nil {
		b.OnChange()
	}
	b.refreshFooter()
}

func (b *Board) refreshFooter() {
	if b.Footer == nil {
		return
	}
	b.footer = append(b.footer[:0], b.Footer()...)
}

var clockPattern = regexp.MustCompile(`([01][0-9]|2[0-3]):[0-5][0-9]`)

func hasClock(value *string) bool {
	return value != nil && clockPattern.MatchString(*value)
}

// adjustClock moves the end clock of a value by an hour, or the start clock
// when start is asked for and the value holds a span.
func adjustClock(value string, start bool, direction int) (string, bool) {
	matches := clockPattern.FindAllStringIndex(value, -1)
	if len(matches) == 0 {
		return "", false
	}
	at := len(matches) - 1
	if start {
		if len(matches) < 2 {
			return "", false
		}
		at = 0
	}
	loc := matches[at]
	hour, err := strconv.Atoi(value[loc[0] : loc[0]+2])
	if err != nil {
		return "", false
	}
	hour = (hour + direction + 24) % 24
	return value[:loc[0]] + fmt.Sprintf("%02d", hour) + value[loc[0]+2:], true
}

type boardStyles struct {
	title         lipgloss.Style
	column        lipgloss.Style
	focusedColumn lipgloss.Style
	field         lipgloss.Style
	focusedField  lipgloss.Style
	locked        lipgloss.Style
	dim           lipgloss.Style
	notice        lipgloss.Style
	help          lipgloss.Style
	separator     lipgloss.Style
}

func styles() boardStyles {
	theme := huh.ThemeBase16()
	return boardStyles{
		title:         theme.Focused.Title.Bold(true),
		column:        theme.Blurred.Title,
		focusedColumn: theme.Focused.Title.Bold(true).Underline(true),
		field:         theme.Blurred.Option,
		focusedField:  theme.Focused.SelectedOption,
		locked:        theme.Blurred.Description,
		dim:           theme.Blurred.Description,
		notice:        theme.Focused.ErrorMessage,
		help:          theme.Blurred.Description,
		separator:     theme.Blurred.Description,
	}
}

// View implements tea.Model.
func (b *Board) View() string {
	st := styles()
	width := b.width
	if width <= 0 {
		width = 80
	}
	var out []string
	if b.Title != "" {
		out = append(out, st.title.Render(b.Title))
	}
	out = append(out, st.separator.Render(strings.Repeat("─", width)))
	out = append(out, b.renderColumns(st, width)...)
	out = append(out, st.separator.Render(strings.Repeat("─", width)))
	for _, line := range b.footer {
		out = append(out, st.dim.Render(line))
	}
	if b.notice != "" {
		out = append(out, st.notice.Render(b.notice))
	}
	out = append(out, st.help.Render("enter start   1–9 column   letter next   shift+letter back   +/- hour   esc"))
	return strings.Join(out, "\n")
}

func (b *Board) renderColumns(st boardStyles, width int) []string {
	blocks := make([][]string, len(b.Columns))
	widths := make([]int, len(b.Columns))
	total := 0
	for i := range b.Columns {
		blocks[i] = b.columnLines(i, st)
		for _, line := range blocks[i] {
			if w := lipgloss.Width(line); w > widths[i] {
				widths[i] = w
			}
		}
		total += widths[i]
	}
	if len(blocks) > 1 {
		total += 2 * (len(blocks) - 1)
	}

	if total > width {
		var out []string
		for i, block := range blocks {
			if i > 0 {
				out = append(out, "")
			}
			out = append(out, block...)
		}
		return out
	}

	height := 0
	for _, block := range blocks {
		if len(block) > height {
			height = len(block)
		}
	}
	var out []string
	pad := lipgloss.NewStyle()
	for row := 0; row < height; row++ {
		parts := make([]string, 0, len(blocks))
		for i, block := range blocks {
			line := ""
			if row < len(block) {
				line = block[row]
			}
			parts = append(parts, pad.Width(widths[i]).Render(line))
		}
		out = append(out, strings.Join(parts, "  "))
	}
	return out
}

func (b *Board) columnLines(index int, st boardStyles) []string {
	column := b.Columns[index]
	focused := index == b.focus
	title := fmt.Sprintf("%d %s", index+1, column.Title)
	switch {
	case column.Dim:
		title = st.dim.Render(title)
	case focused:
		title = st.focusedColumn.Render(title)
	default:
		title = st.column.Render(title)
	}
	lines := []string{title}
	for i, field := range column.Fields {
		text := fmt.Sprintf(" %c %s  %s", field.Key, field.Label, fieldLabel(field))
		switch {
		case field.Locked != "":
			text = st.locked.Render(text)
		case column.Dim:
			text = st.dim.Render(text)
		case focused && i == b.cursor[index]:
			text = st.focusedField.Render(text)
		default:
			text = st.field.Render(text)
		}
		lines = append(lines, text)
	}
	for _, line := range column.Lines {
		lines = append(lines, st.dim.Render("  "+line))
	}
	return lines
}

func fieldLabel(field BoardField) string {
	value := ""
	if field.Value != nil {
		value = *field.Value
	}
	if field.Choices != nil {
		for _, choice := range field.Choices() {
			if choice.Value == value {
				return choice.Label
			}
		}
	}
	return value
}
