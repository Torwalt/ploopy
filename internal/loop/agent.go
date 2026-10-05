package loop

import (
	"strings"

	"github.com/Torwalt/ploopy/internal/harness"
)

// Agent is a harness and the model and effort it runs at.
type Agent struct {
	Harness harness.Harness
	Model   string
	Effort  string
}

// Label names the agent the way the state file records it.
func (a Agent) Label() string {
	if a.Harness == nil {
		return ""
	}
	parts := []string{a.Harness.Name()}
	if a.Model != "" {
		parts = append(parts, a.Model)
	}
	if a.Effort != "" {
		parts = append(parts, a.Effort)
	}
	return strings.Join(parts, " ")
}
