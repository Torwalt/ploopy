package loop

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/Torwalt/ploopy/internal/harness"
)

// sessionRecord is a session that ended but was not yet judged. The next run
// judges it rather than paying for it twice.
type sessionRecord struct {
	Head    string           `json:"head"`
	Outcome harness.Outcome  `json:"outcome"`
	Stem    string           `json:"stem"`
	Tail    string           `json:"tail"`
	Extra   *json.RawMessage `json:"-"`
}

// progress is what survives a crash within a unit. It is local to the machine;
// everything that must outlive it lives in the state file beside the plan.
type progress struct {
	path string

	Unit      string         `json:"unit,omitempty"`
	Base      string         `json:"base,omitempty"`
	Failure   string         `json:"failure,omitempty"`
	Session   *sessionRecord `json:"session,omitempty"`
	LeftDirty bool           `json:"left_dirty,omitempty"`
	Spent     spent          `json:"spent"`
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
	}
	p.Unit, p.Base, p.Failure, p.Session, p.LeftDirty = unit, base, failure, session, false
	return p.save()
}

func (p *progress) clear() {
	*p = progress{path: p.path}
	_ = os.Remove(p.path)
}
