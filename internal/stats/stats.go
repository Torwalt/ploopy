// Package stats is the record of what a plan's runs took: the log a run
// appends to as it goes, and the report read from that log and the state file.
//
// The log is `.ploopy/<plan>/stats.jsonl`, local to its checkout and gone with
// it. The report is what outlives it, stamped into a commit.
package stats

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Torwalt/ploopy/internal/harness"
)

// Run is what one run took.
type Run struct {
	Started  time.Time
	Ended    time.Time
	Sessions int
	Session  time.Duration // agents at work
	Checks   time.Duration // setup, preflight, verify and test
	Waited   time.Duration // peak hours, usage limits and backoff
	CostUSD  float64
	Tokens   harness.Tokens
}

// Record is one line of the log: a session, a check, a wait, a tool call or a
// whole run.
type Record struct {
	Kind    string          `json:"kind"` // session, check, wait, tool or run
	Run     string          `json:"run"`
	Unit    string          `json:"unit,omitempty"`
	Stem    string          `json:"stem,omitempty"`
	Agent   string          `json:"agent,omitempty"`
	Started time.Time       `json:"started"`
	Seconds float64         `json:"seconds"`
	Verify  float64         `json:"verify_s,omitempty"`
	Test    float64         `json:"test_s,omitempty"`
	Tokens  *harness.Tokens `json:"tokens,omitempty"`
	CostUSD float64         `json:"cost_usd,omitempty"`
	Turns   int             `json:"turns,omitempty"`
	Tools   int             `json:"tools,omitempty"`
	Verdict string          `json:"verdict,omitempty"`
	Reason  string          `json:"reason,omitempty"`
	Tool    string          `json:"tool,omitempty"`
	Command string          `json:"command,omitempty"`
}

// Dir is where a checkout keeps a plan's own files.
func Dir(root, planName string) string {
	return filepath.Join(root, ".ploopy", strings.ToLower(planName))
}

func logPath(dir string) string { return filepath.Join(dir, "stats.jsonl") }

// Append adds a record to the log in dir.
func Append(dir string, r Record) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(logPath(dir), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
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

// Load reads the log in dir. A missing log is no records; a line that does not
// parse is skipped, so a run killed mid-write costs one record.
func Load(dir string) ([]Record, error) {
	file, err := os.Open(logPath(dir))
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
