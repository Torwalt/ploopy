// Package history is what every session of every plan took, kept for the
// repository across plans, branches and checkouts, so a run can be estimated
// before it starts.
//
// The log is `<git-common-dir>/ploopy/history.jsonl`: every worktree shares
// it, removing a worktree leaves it, and nothing commits it.
package history

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/Torwalt/ploopy/internal/harness"
)

// Record is one session.
type Record struct {
	ReviewMode string          `json:"review_mode,omitempty"`
	ReviewBase string          `json:"review_base,omitempty"`
	ReviewHead string          `json:"review_head,omitempty"`
	Plan       string          `json:"plan"`
	Role       string          `json:"role,omitempty"`
	Branch     string          `json:"branch,omitempty"`
	Run        string          `json:"run"`
	Unit       string          `json:"unit"`
	Stem       string          `json:"stem"`
	Agent      string          `json:"agent"`
	Harness    string          `json:"harness"`
	Started    time.Time       `json:"started"`
	Seconds    float64         `json:"seconds"`
	Verdict    string          `json:"verdict"`
	Tokens     *harness.Tokens `json:"tokens,omitempty"`
	CostUSD    float64         `json:"cost_usd,omitempty"`
	Before     *harness.Budget `json:"before,omitempty"`
	After      *harness.Budget `json:"after,omitempty"`
	Away       bool            `json:"away,omitempty"` // ran on a lent subscription
	WorkOrder  int             `json:"work_order,omitempty"`
	Degraded   []string        `json:"degraded,omitempty"`
}

// Path is the repository's log, given its git common directory.
func Path(commonDir string) string { return filepath.Join(commonDir, "ploopy", "history.jsonl") }

// Append adds a record to the log.
func Append(path string, r Record) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	line, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = file.Write(append(line, '\n'))
	return err
}

// Load reads the log. A missing log is no records; a line that does not parse
// is skipped, so a run killed mid-write costs one record.
func Load(path string) ([]Record, error) {
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var records []Record
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		var r Record
		if json.Unmarshal(scanner.Bytes(), &r) == nil {
			records = append(records, r)
		}
	}
	return records, scanner.Err()
}

// Latest is the newest usage windows a harness reported, or nil.
func Latest(records []Record, harnessName string) *harness.Budget {
	var latest *harness.Budget
	for _, r := range records {
		if r.Harness != harnessName {
			continue
		}
		for _, b := range []*harness.Budget{r.Before, r.After} {
			if b != nil && (latest == nil || b.At.After(latest.At)) {
				latest = b
			}
		}
	}
	return latest
}
