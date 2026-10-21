package harness

// Preferences limits the picker without restricting explicit session settings.
type Preferences struct {
	ModelOptions  []string
	EffortOptions []string
	Fallbacks     map[string]string
}

func (p Preferences) ModelsOr(defaults ...string) []string {
	if p.ModelOptions != nil {
		return p.ModelOptions
	}
	return defaults
}

func (p Preferences) OfferedEfforts() []string {
	if p.EffortOptions != nil {
		return p.EffortOptions
	}
	return []string{"high"}
}

// Fallback is a route to use only after the selected model hits a usage limit.
func (p Preferences) Fallback(model string) string { return p.Fallbacks[model] }

// EffortChoices is what the picker offers; Efforts still names supported values.
func EffortChoices(h Harness) []string {
	if p, ok := h.(interface{ OfferedEfforts() []string }); ok {
		return p.OfferedEfforts()
	}
	return h.Efforts()
}

// FallbackModel asks for another route to the selected model, if one is configured.
func FallbackModel(h Harness, model string) string {
	if p, ok := h.(interface{ Fallback(string) string }); ok {
		return p.Fallback(model)
	}
	return ""
}

// PeakWindowsFor lets a subscription route run while its API bills peak prices.
func PeakWindowsFor(h Harness, model string) []Window {
	if p, ok := h.(interface{ PeakWindowsFor(string) []Window }); ok {
		return p.PeakWindowsFor(model)
	}
	return h.PeakWindows()
}
