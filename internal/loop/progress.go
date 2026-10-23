package loop

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/Torwalt/ploopy/internal/harness"
	"github.com/Torwalt/ploopy/internal/state"
)

// sessionRecord is a session that ended but was not yet judged. The next run
// judges it rather than paying for it twice.
type sessionRecord struct {
	Head    string           `json:"head"`
	Outcome harness.Outcome  `json:"outcome"`
	Stem    string           `json:"stem"`
	Tail    string           `json:"tail"`
	Extra   *json.RawMessage `json:"-"`

	// Who ran it, which need not be who would run the next session.
	Harness string      `json:"harness,omitempty"`
	Model   string      `json:"model,omitempty"`
	Effort  string      `json:"effort,omitempty"`
	Info    sessionInfo `json:"info"`
}

// progress is what survives a crash within a unit. It is local to the machine;
// everything that must outlive it lives in the state file beside the plan.
type progress struct {
	path string

	Unit      string         `json:"unit,omitempty"`
	Base      string         `json:"base,omitempty"`
	Failure   string         `json:"failure,omitempty"`
	Decisions string         `json:"decisions,omitempty"`
	Session   *sessionRecord `json:"session,omitempty"`
	LeftDirty bool           `json:"left_dirty,omitempty"`
	Spent     spent          `json:"spent"`
	Rescues   []state.Rescue `json:"rescues,omitempty"`
}

func loadProgress(path string) *progress {
	p := &progress{path: path}
	raw, err := os.ReadFile(path)
	if err != nil {
		return p
	}
	// A corrupt progress file costs one re-run, never a wrong verdict.
	_ = json.Unmarshal(raw, p)
	p.path = path
	return p
}

func (p *progress) forUnit(id string) bool { return p.Unit == id }

func (p *progress) save() error {
	if err := os.MkdirAll(filepath.Dir(p.path), 0o755); err != nil {
		return err
	}
	body, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p.path, append(body, '\n'), 0o644)
}

// reset starts a unit's record. What a unit already took carries over when the
// same unit is picked up again.
func (p *progress) reset(unit, base, failure string, session *sessionRecord) error {
	if p.Unit != unit {
		p.Spent = spent{}
		p.Decisions = ""
		p.Rescues = nil
		// The next unit starts from a clean tree or from dirt the loop did not
		// leave; either way, it is not this unit's earlier attempt.
		p.LeftDirty = false
	}
	p.Unit, p.Base, p.Failure, p.Session = unit, base, failure, session
	return p.save()
}

func (p *progress) clear() {
	*p = progress{path: p.path}
	_ = os.Remove(p.path)
}
