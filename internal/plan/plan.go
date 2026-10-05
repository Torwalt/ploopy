// Package plan parses and lints the task document the loop runs.
//
// A plan is optional `key: value` front matter, a preamble, then `## Stage N`
// sections holding `### N.M Title` units. One unit's work order is the preamble
// plus that unit, and nothing from the units after it.
package plan

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var (
	unitRe    = regexp.MustCompile(`^### (\d+\.\d+)\s+(.+?)\s*$`)
	stageRe   = regexp.MustCompile(`^## Stage\s+(\d+)\b\s*[—–:-]?\s*(.*?)\s*$`)
	fenceRe   = regexp.MustCompile("^\\s*(```|~~~)")
	fieldRe   = regexp.MustCompile(`^([A-Z][A-Za-z-]*):\s*(.*?)\s*$`)
	settingRe = regexp.MustCompile(`^([a-z][a-z-]*):\s*(.*?)\s*$`)
	goalRe    = regexp.MustCompile(`(?m)^(#### Goal\b|\*\*Goal\b)`)
	doneRe    = regexp.MustCompile(`(?m)^(#### Completion criteria\b|\*\*Done when\b|#### Done when\b)`)
)

// UnitFields may appear directly under a unit heading, before its first prose.
var UnitFields = []string{"Test", "Context"}

// Settings are the front-matter keys a plan may set.
var Settings = []string{"authority", "context", "test", "agent", "effort", "model"}

const workOrderNote = "The unit below is the only work authorized by this order. Later\n" +
	"units of this stage and every later stage are deliberately absent."

// Unit is one `### N.M Title` section.
type Unit struct {
	ID         string
	Title      string
	Stage      string
	StageTitle string
	Start      int // index of the heading line
	End        int // exclusive
	Fields     map[string]string
}

// Key orders units numerically rather than lexically.
func (u Unit) Key() []int {
	parts := strings.Split(u.ID, ".")
	key := make([]int, 0, len(parts))
	for _, part := range parts {
		n, _ := strconv.Atoi(part)
		key = append(key, n)
	}
	return key
}

func less(a, b []int) bool {
	for i := range a {
		if i >= len(b) {
			return false
		}
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}

// Plan is a parsed task document. Path is relative to the repository root.
type Plan struct {
	Path        string
	Lines       []string
	BodyStart   int
	PreambleEnd int
	Units       []Unit
	Settings    map[string]string
	Errors      []string
}

// Name is the plan's file stem, which also names its state file.
func (p *Plan) Name() string {
	return strings.TrimSuffix(filepath.Base(p.Path), filepath.Ext(p.Path))
}

// Unit finds a unit by id, or nil.
func (p *Plan) Unit(id string) *Unit {
	for i := range p.Units {
		if p.Units[i].ID == id {
			return &p.Units[i]
		}
	}
	return nil
}

// Index is a unit's position in the plan, or -1.
func (p *Plan) Index(id string) int {
	for i := range p.Units {
		if p.Units[i].ID == id {
			return i
		}
	}
	return -1
}

// Previous is the unit before this one, or nil.
func (p *Plan) Previous(u Unit) *Unit {
	if i := p.Index(u.ID); i > 0 {
		return &p.Units[i-1]
	}
	return nil
}

// Body is a unit's text, with trailing blanks and rules dropped.
func (p *Plan) Body(u Unit) string {
	chunk := append([]string(nil), p.Lines[u.Start:min(u.End, len(p.Lines))]...)
	for len(chunk) > 0 {
		last := strings.TrimSpace(chunk[len(chunk)-1])
		if last != "" && last != "---" {
			break
		}
		chunk = chunk[:len(chunk)-1]
	}
	return strings.Join(chunk, "\n")
}

// Preamble is everything before the first stage. Every session sees it.
func (p *Plan) Preamble() string {
	return strings.Trim(strings.Join(p.Lines[p.BodyStart:min(p.PreambleEnd, len(p.Lines))], "\n"), "\n")
}

// WorkOrder is the preamble, the unit's stage heading, and the unit itself.
func (p *Plan) WorkOrder(u Unit) string {
	stage := "## Stage " + u.Stage
	if u.StageTitle != "" {
		stage += " — " + u.StageTitle
	}
	return strings.Join([]string{p.Preamble(), "", stage, "", workOrderNote, "", p.Body(u), ""}, "\n")
}

// Authority is the document whose decisions this plan implements, relative to
// the repository root. It is named in front matter, or found beside a plan
// whose name ends in _EXECUTION.
func (p *Plan) Authority(root string) string {
	return p.AuthorityAmong(func(path string) bool { return isFile(filepath.Join(root, path)) })
}

// AuthorityAmong is Authority for a tree that is not on disk: exists says
// whether a repository path is a file in it.
func (p *Plan) AuthorityAmong(exists func(path string) bool) string {
	if named := p.Settings["authority"]; named != "" {
		return named
	}
	name := p.Name()
	if !strings.HasSuffix(name, "_EXECUTION") {
		return ""
	}
	sibling := filepath.Join(filepath.Dir(p.Path), strings.TrimSuffix(name, "_EXECUTION")+".md")
	if exists(sibling) {
		return sibling
	}
	return ""
}

// Context is what a session reads before its work order, in order, without
// duplicates.
func (p *Plan) Context(root string, u *Unit, base []string) []string {
	docs := append([]string(nil), base...)
	if authority := p.Authority(root); authority != "" {
		docs = append(docs, authority)
	}
	docs = append(docs, strings.Fields(p.Settings["context"])...)
	if u != nil {
		docs = append(docs, strings.Fields(u.Fields["Context"])...)
	}
	seen := map[string]bool{}
	out := docs[:0]
	for _, doc := range docs {
		if !seen[doc] {
			seen[doc] = true
			out = append(out, doc)
		}
	}
	return out
}

// WantsTests reports whether the test command runs after this unit.
func (p *Plan) WantsTests(u Unit) bool {
	if value, ok := u.Fields["Test"]; ok {
		return strings.EqualFold(strings.TrimSpace(value), "yes")
	}
	return strings.EqualFold(strings.TrimSpace(p.Settings["test"]), "yes")
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func yesNo(value string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "yes":
		return true, true
	case "no":
		return false, true
	}
	return false, false
}

func frontMatter(lines []string) (map[string]string, []string, int) {
	settings := map[string]string{}
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return settings, nil, 0
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return settings, []string{"front matter opened with `---` is never closed"}, 0
	}

	var errs []string
	known := map[string]bool{}
	for _, key := range Settings {
		known[key] = true
	}
	for i := 1; i < end; i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "" {
			continue
		}
		match := settingRe.FindStringSubmatch(line)
		switch {
		case match == nil:
			errs = append(errs, fmt.Sprintf("front matter line %d is not `key: value`: %q", i+1, line))
		case !known[match[1]]:
			errs = append(errs, fmt.Sprintf("unknown front matter key `%s`; known: %s",
				match[1], strings.Join(Settings, ", ")))
		default:
			settings[match[1]] = match[2]
		}
	}
	return settings, errs, end + 1
}

func unitFields(lines []string, start, end int) map[string]string {
	known := map[string]bool{}
	for _, name := range UnitFields {
		known[name] = true
	}
	fields := map[string]string{}
	for i := start + 1; i < min(end, len(lines)); i++ {
		if strings.TrimSpace(lines[i]) == "" {
			continue
		}
		match := fieldRe.FindStringSubmatch(lines[i])
		if match == nil || !known[match[1]] {
			break
		}
		fields[match[1]] = match[2]
	}
	return fields
}

// Parse reads a task document. It never fails: what it cannot use becomes a
// lint error.
func Parse(text, path string) *Plan {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	settings, errs, bodyStart := frontMatter(lines)

	var units []Unit
	var stage, stageTitle string
	haveStage := false
	preambleEnd := -1
	fenced := false

	closeAt := func(index int) {
		if n := len(units); n > 0 && units[n-1].End > index {
			units[n-1].End = index
		}
	}

	for i := bodyStart; i < len(lines); i++ {
		line := lines[i]
		if fenceRe.MatchString(line) {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		if strings.HasPrefix(line, "## ") {
			closeAt(i)
			match := stageRe.FindStringSubmatch(line)
			haveStage = match != nil
			if match != nil {
				stage, stageTitle = match[1], match[2]
				if preambleEnd < 0 {
					preambleEnd = i
				}
			}
			continue
		}
		if match := unitRe.FindStringSubmatch(line); match != nil && haveStage {
			closeAt(i)
			units = append(units, Unit{
				ID: match[1], Title: match[2],
				Stage: stage, StageTitle: stageTitle,
				Start: i, End: len(lines),
			})
		}
	}

	for i := range units {
		units[i].Fields = unitFields(lines, units[i].Start, units[i].End)
	}
	if preambleEnd < 0 {
		preambleEnd = len(lines)
	}

	return &Plan{
		Path: path, Lines: lines,
		BodyStart: bodyStart, PreambleEnd: preambleEnd,
		Units: units, Settings: settings, Errors: errs,
	}
}

// Load reads a plan from disk. Path is stored as given.
func Load(path, storeAs string) (*Plan, error) {
	text, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(string(text), storeAs), nil
}

// Relative expresses path relative to root where it can.
func Relative(path, root string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return path
	}
	rel, err := filepath.Rel(absRoot, abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return path
	}
	return rel
}

// Discover lists the runnable plans under directory: documents with units that
// are not another plan's authority.
func Discover(root, directory string) ([]*Plan, error) {
	matches, err := filepath.Glob(filepath.Join(root, directory, "*.md"))
	if err != nil {
		return nil, err
	}
	sortStrings(matches)

	var plans []*Plan
	for _, path := range matches {
		p, err := Load(path, Relative(path, root))
		if err != nil {
			return nil, err
		}
		plans = append(plans, p)
	}
	return Runnable(plans, func(path string) bool { return isFile(filepath.Join(root, path)) }), nil
}

// Runnable keeps the plans that have units and that no other plan names as
// its authority. exists says whether a repository path is a file.
func Runnable(plans []*Plan, exists func(path string) bool) []*Plan {
	authorities := map[string]bool{}
	for _, p := range plans {
		if authority := p.AuthorityAmong(exists); authority != "" {
			authorities[authority] = true
		}
	}
	var out []*Plan
	for _, p := range plans {
		if len(p.Units) > 0 && !authorities[p.Path] {
			out = append(out, p)
		}
	}
	return out
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

// Harnesses is what lint needs to know about the harnesses a plan may name.
type Harnesses interface {
	Known(name string) bool
	Efforts(name string) []string
	Names() []string
}

// Lint reports what would stop the loop running this plan, and what merely
// looks wrong. Units in settled are exempt from context-document checks: a
// landed unit may have deleted the document it named.
func Lint(p *Plan, root string, harnesses Harnesses, settled map[string]bool) (errs, warnings []string) {
	errs = append(errs, p.Errors...)
	if len(p.Units) == 0 {
		errs = append(errs, "no `### N.M` units under a `## Stage N` heading")
	}

	seen := map[string]bool{}
	var previous *Unit
	for i := range p.Units {
		u := p.Units[i]
		where := "unit " + u.ID
		if seen[u.ID] {
			errs = append(errs, where+" appears twice; unit ids must be unique")
		}
		seen[u.ID] = true
		if previous != nil && !less(previous.Key(), u.Key()) {
			warnings = append(warnings, fmt.Sprintf("%s comes after %s; ids should ascend", where, previous.ID))
		}
		previous = &p.Units[i]
		if strings.SplitN(u.ID, ".", 2)[0] != u.Stage {
			warnings = append(warnings, fmt.Sprintf("%s sits under Stage %s", where, u.Stage))
		}

		text := strings.Join(p.Lines[u.Start+1:min(u.End, len(p.Lines))], "\n")
		if strings.TrimSpace(text) == "" {
			errs = append(errs, where+" has no body")
			continue
		}
		if !goalRe.MatchString(text) {
			errs = append(errs, where+" states no goal (`**Goal:**` or `#### Goal`)")
		}
		if !doneRe.MatchString(text) {
			warnings = append(warnings, where+
				" does not say when it is done (`**Done when:**` or `#### Completion criteria`)")
		}
		if value, ok := u.Fields["Test"]; ok {
			if _, valid := yesNo(value); !valid {
				errs = append(errs, fmt.Sprintf("%s: `Test:` must be yes or no, not %q", where, value))
			}
		}
		for _, doc := range strings.Fields(u.Fields["Context"]) {
			if !settled[u.ID] && !isFile(filepath.Join(root, doc)) {
				warnings = append(warnings, fmt.Sprintf("%s: context document %s does not exist", where, doc))
			}
		}
	}

	if value, ok := p.Settings["test"]; ok {
		if _, valid := yesNo(value); !valid {
			errs = append(errs, fmt.Sprintf("front matter `test:` must be yes or no, not %q", value))
		}
	}
	if name := p.Settings["agent"]; name != "" && harnesses != nil {
		if !harnesses.Known(name) {
			errs = append(errs, "front matter `agent:` must be one of "+strings.Join(harnesses.Names(), ", "))
		} else if effort, ok := p.Settings["effort"]; ok {
			efforts := harnesses.Efforts(name)
			if !contains(efforts, effort) {
				errs = append(errs, fmt.Sprintf("front matter `effort:` for %s must be one of %s",
					name, strings.Join(efforts, ", ")))
			}
		}
	}
	if authority := p.Authority(root); authority != "" && !isFile(filepath.Join(root, authority)) {
		errs = append(errs, "authority "+authority+" does not exist")
	}
	for _, doc := range strings.Fields(p.Settings["context"]) {
		if !isFile(filepath.Join(root, doc)) {
			errs = append(errs, "context document "+doc+" does not exist")
		}
	}
	return errs, warnings
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
