package ui

import "github.com/charmbracelet/huh"

// Field is one choice on a one-page form.
type Field struct {
	Title   string
	Value   *string         // already set to what the author can keep
	Choices func() []Choice // asked again whenever Binding changes
	Binding any             // what the choices depend on, or nil
}

// Form asks for every field on one page. Left and right change a field, enter
// moves to the next, and on the last one it starts.
func Form(title string, fields []Field) error {
	if !Interactive() {
		return NoTerminal("help")
	}
	inputs := make([]huh.Field, 0, len(fields))
	for _, field := range fields {
		input := huh.NewSelect[string]().Title(field.Title).Value(field.Value)
		if field.Binding != nil {
			input = input.OptionsFunc(func() []huh.Option[string] { return options(field.Choices()) }, field.Binding)
		} else {
			input = input.Options(options(field.Choices())...)
		}
		inputs = append(inputs, input.Inline(true))
	}
	return huh.NewForm(huh.NewGroup(inputs...).Title(title)).WithTheme(huh.ThemeBase16()).Run()
}

func options(choices []Choice) []huh.Option[string] {
	out := make([]huh.Option[string], 0, len(choices))
	for _, choice := range choices {
		out = append(out, huh.NewOption(choice.Label, choice.Value))
	}
	return out
}
