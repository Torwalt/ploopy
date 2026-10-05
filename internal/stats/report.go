package stats

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/Torwalt/ploopy/internal/harness"
	"github.com/Torwalt/ploopy/internal/plan"
	"github.com/Torwalt/ploopy/internal/state"
)

// Heading opens every report, so `git log --grep` finds the stamped ones.
const Heading = "ploopy report: "

// Report renders what a plan took, for a person and for a commit message. Each
// unit's account comes from the state file; the runs, waits, every check and
// every tool call come from the log, when there is one. current is a run still
// going on, not yet in the log.
func Report(p *plan.Plan, s *state.State, records []Record, current *Run) string {
	r := report{plan: p, state: s, records: records, current: current}
	r.line("%s%s", Heading, p.Name())
	r.line("")
	r.outcome()
	r.stages()
	r.slowestUnits()
	r.checks()
	r.commands()
	r.trouble()
	if r.listPrice {
		r.line("")
		r.line("≈ Claude's cost is the API-price equivalent, not what a subscription charges.")
	}
	if len(records) == 0 {
		r.line("")
		r.line("No stats log: runs, waits and tool calls are gone with the checkout's .ploopy/.")
	}
	return r.text.String()
}

type report struct {
	plan      *plan.Plan
	state     *state.State
	records   []Record
	current   *Run
	text      strings.Builder
	listPrice bool
}

func (r *report) line(format string, args ...any) {
	fmt.Fprintf(&r.text, format+"\n", args...)
}

// entries are the units the loop recorded, in plan order. Units marked by hand
// took nothing worth counting.
func (r *report) entries() []unitEntry {
	var out []unitEntry
	for _, u := range r.plan.Units {
		if e := r.state.Entry(u.ID); e != nil && e.Outcome != "marked" {
			out = append(out, unitEntry{unit: u, entry: e})
		}
	}
	return out
}

type unitEntry struct {
	unit  plan.Unit
	entry *state.Entry
}

func (r *report) cost(usd float64, listPrice bool) string {
	if usd <= 0 {
		return ""
	}
	r.listPrice = r.listPrice || listPrice
	text := fmt.Sprintf("$%.2f", usd)
	if listPrice {
		text = "≈" + text
	}
	return text
}

func (r *report) outcome() {
	counts := map[string]int{}
	for _, u := range r.plan.Units {
		counts[r.state.Status(u.ID)]++
	}
	parts := []string{fmt.Sprintf("%d of %d landed", counts[state.Landed], len(r.plan.Units))}
	for _, status := range []string{state.Blocked, state.Skipped, state.Pending} {
		if counts[status] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[status], status))
		}
	}

	runs, wall := 0, 0.0
	var waits = map[string]float64{}
	sessions, verdicts := 0, map[string]int{}
	for _, rec := range r.records {
		switch rec.Kind {
		case "run":
			runs++
			wall += rec.Seconds
		case "wait":
			waits[rec.Reason] += rec.Seconds
		case "session":
			sessions++
			verdicts[rec.Verdict]++
		}
	}
	if r.current != nil {
		runs++
		end := r.current.Ended
		if end.IsZero() {
			end = time.Now()
		}
		wall += end.Sub(r.current.Started).Seconds()
	}
	if runs > 0 {
		parts = append(parts, fmt.Sprintf("%d run(s)", runs), Duration(int(wall))+" wall clock")
	}

	var spent, checks, total float64
	var tokens harness.Tokens
	claude := false
	for _, ue := range r.entries() {
		e := ue.entry
		spent += float64(e.SessionS)
		checks += float64(e.VerifyS + e.TestS)
		total += e.CostUSD
		claude = claude || (e.Harness == "claude" && e.CostUSD > 0)
		if e.Tokens != nil {
			tokens = tokens.Add(*e.Tokens)
		}
	}
	// The log counts every session, a unit re-run after a reset included.
	if logged, loggedClaude, ok := r.loggedCost(); ok {
		total, claude = logged, loggedClaude
	}
	if cost := r.cost(total, claude); cost != "" {
		parts = append(parts, cost)
	}
	r.line("%s", strings.Join(parts, " · "))
	r.line("")

	waited := 0.0
	for _, seconds := range waits {
		waited += seconds
	}
	var timeLine []string
	if spent > 0 {
		timeLine = append(timeLine, Duration(int(spent))+" in sessions")
	}
	if checks > 0 {
		timeLine = append(timeLine, Duration(int(checks))+" in checks")
	}
	if len(r.records) > 0 {
		timeLine = append(timeLine, Clock(time.Duration(waited)*time.Second)+" waiting")
	}
	if len(timeLine) > 0 {
		r.line("%-9s %s", "Time", strings.Join(timeLine, " · "))
	}

	if sessions > 0 {
		line := []string{fmt.Sprintf("%d", sessions)}
		for _, verdict := range []string{"landed", "failed", "blocked", "limit", "transient", "exhausted"} {
			if verdicts[verdict] > 0 {
				line = append(line, fmt.Sprintf("%d %s", verdicts[verdict], verdict))
			}
		}
		r.line("%-9s %s", "Sessions", strings.Join(line, " · "))
	}
	r.agents()
	if tokens != (harness.Tokens{}) {
		line := Count(tokens.Prompt()) + " read"
		if prompt := tokens.Prompt(); prompt > 0 && tokens.CacheRead > 0 {
			line += fmt.Sprintf(", %d%% from cache", tokens.CacheRead*100/prompt)
		}
		r.line("%-9s %s · %s written", "Tokens", line, Count(tokens.Generated()))
	}
	if waited > 0 {
		var parts []string
		for _, why := range sortedKeys(waits) {
			parts = append(parts, why+" "+Duration(int(waits[why])))
		}
		r.line("%-9s %s", "Waits", strings.Join(parts, " · "))
	}
}

// loggedCost is what every session in the log cost, and whether any of it
// is Claude's list price.
func (r *report) loggedCost() (float64, bool, bool) {
	total, claude, any := 0.0, false, false
	for _, rec := range r.records {
		if rec.Kind == "session" {
			any = true
			total += rec.CostUSD
			claude = claude || (strings.HasPrefix(rec.Agent, "claude") && rec.CostUSD > 0)
		}
	}
	return total, claude, any
}

// agents is who did the work: from the log per session, else from the state
// file per unit.
func (r *report) agents() {
	type tally struct {
		count int
		cost  float64
	}
	byAgent := map[string]*tally{}
	var order []string
	add := func(agent string, cost float64) {
		if agent == "" {
			return
		}
		t, ok := byAgent[agent]
		if !ok {
			t = &tally{}
			byAgent[agent] = t
			order = append(order, agent)
		}
		t.count++
		t.cost += cost
	}
	what := "session(s)"
	for _, rec := range r.records {
		if rec.Kind == "session" {
			add(rec.Agent, rec.CostUSD)
		}
	}
	if len(order) == 0 {
		what = "unit(s)"
		for _, ue := range r.entries() {
			add(ue.entry.Agent, ue.entry.CostUSD)
		}
	}
	for i, agent := range order {
		title := ""
		if i == 0 {
			title = "Agents"
		}
		t := byAgent[agent]
		line := fmt.Sprintf("%s  %d %s", agent, t.count, what)
		if cost := r.cost(t.cost, strings.HasPrefix(agent, "claude")); cost != "" {
			line += "  " + cost
		}
		r.line("%-9s %s", title, line)
	}
}

func (r *report) stages() {
	type stage struct {
		id, title       string
		elapsed, checks int
		cost            float64
		claude          bool
		units           int
	}
	var stages []*stage
	for _, ue := range r.entries() {
		if len(stages) == 0 || stages[len(stages)-1].id != ue.unit.Stage {
			stages = append(stages, &stage{id: ue.unit.Stage, title: ue.unit.StageTitle})
		}
		st := stages[len(stages)-1]
		st.units++
		st.elapsed += ue.entry.ElapsedS
		st.checks += ue.entry.VerifyS + ue.entry.TestS
		st.cost += ue.entry.CostUSD
		st.claude = st.claude || ue.entry.Harness == "claude"
	}
	if len(stages) < 2 {
		return
	}
	r.line("")
	r.line("%-36s %8s %8s %8s", "Stage", "time", "checks", "cost")
	for _, st := range stages {
		r.line("%-36s %8s %8s %8s", cut(st.id+" "+st.title, 36), Duration(st.elapsed), Duration(st.checks),
			r.cost(st.cost, st.claude))
	}
}

func (r *report) slowestUnits() {
	entries := r.entries()
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].entry.ElapsedS > entries[j].entry.ElapsedS })
	if len(entries) == 0 || entries[0].entry.ElapsedS == 0 {
		return
	}
	if len(entries) > 3 {
		entries = entries[:3]
	}
	r.line("")
	r.line("%-36s %8s %8s %8s", "Slowest units", "time", "tries", "cost")
	for _, ue := range entries {
		e := ue.entry
		r.line("%-36s %8s %8d %8s", cut(ue.unit.ID+" "+ue.unit.Title, 36), Duration(e.ElapsedS), e.Attempts,
			r.cost(e.CostUSD, e.Harness == "claude"))
	}
}

// checks is whether verify and test got slower as the plan went on: every run
// of each from the log, else each unit's time from the state file.
func (r *report) checks() {
	series := map[string][]float64{}
	for _, rec := range r.records {
		if rec.Kind == "check" && (rec.Reason == "verify" || rec.Reason == "test") {
			series[rec.Reason] = append(series[rec.Reason], rec.Seconds)
		}
	}
	if len(series) == 0 {
		for _, ue := range r.entries() {
			tries := float64(max(ue.entry.Attempts, 1))
			if ue.entry.VerifyS > 0 {
				series["verify"] = append(series["verify"], float64(ue.entry.VerifyS)/tries)
			}
			if ue.entry.TestS > 0 {
				series["test"] = append(series["test"], float64(ue.entry.TestS)/tries)
			}
		}
	}
	var flaky []string
	for _, ue := range r.entries() {
		for _, note := range ue.entry.Notes {
			if strings.Contains(note, "passed on a rerun") {
				flaky = append(flaky, ue.unit.ID)
				break
			}
		}
	}
	if len(series) == 0 && len(flaky) == 0 {
		return
	}

	r.line("")
	r.line("%-9s %5s %8s %17s  %s", "Checks", "runs", "median", "first → last", "trend")
	for _, name := range []string{"verify", "test"} {
		values := series[name]
		if len(values) == 0 {
			continue
		}
		span := Duration(int(values[0])) + " → " + Duration(int(values[len(values)-1]))
		r.line("%s", strings.TrimRight(fmt.Sprintf("%-9s %5d %8s %17s  %s %s", name, len(values),
			Duration(int(median(values))), span, sparkline(values), trend(values)), " "))
	}
	if len(flaky) > 0 {
		r.line("%-9s failed once and passed on a rerun in %s", "", strings.Join(flaky, ", "))
	}
}

// commands are the slowest things the sessions ran.
func (r *report) commands() {
	byCommand := map[string][]float64{}
	for _, rec := range r.records {
		if rec.Kind != "tool" {
			continue
		}
		name := rec.Command
		if name == "" {
			name = rec.Tool
		}
		byCommand[name] = append(byCommand[name], rec.Seconds)
	}
	type command struct {
		name     string
		runs     int
		med, top float64
	}
	var commands []command
	for name, values := range byCommand {
		top := 0.0
		for _, v := range values {
			top = math.Max(top, v)
		}
		if top >= 1 {
			commands = append(commands, command{name: name, runs: len(values), med: median(values), top: top})
		}
	}
	if len(commands) == 0 {
		return
	}
	sort.Slice(commands, func(i, j int) bool {
		if commands[i].top != commands[j].top {
			return commands[i].top > commands[j].top
		}
		return commands[i].name < commands[j].name
	})
	if len(commands) > 5 {
		commands = commands[:5]
	}
	r.line("")
	r.line("%-40s %5s %8s %8s", "Slowest commands in sessions", "runs", "median", "max")
	for _, c := range commands {
		r.line("%-40s %5d %8s %8s", cut(c.name, 40), c.runs, Duration(int(c.med)), Duration(int(c.top)))
	}
}

// trouble is every attempt that did not land, and every block.
func (r *report) trouble() {
	var lines []string
	for _, rec := range r.records {
		if rec.Kind == "session" && rec.Verdict == "failed" {
			lines = append(lines, cut(rec.Unit+" failed: "+rec.Reason, 72))
		}
	}
	for _, ue := range r.entries() {
		if ue.entry.Status == state.Blocked {
			lines = append(lines, cut(ue.unit.ID+" blocked: "+ue.entry.Reason, 72))
		}
	}
	if len(lines) == 0 {
		return
	}
	r.line("")
	r.line("What went wrong")
	const shown = 8
	for i, line := range lines {
		if i == shown {
			r.line("  and %d more", len(lines)-shown)
			break
		}
		r.line("  %s", line)
	}
}

const ticks = "▁▂▃▄▅▆▇█"

// sparkline draws values low to high, at most 40 wide.
func sparkline(values []float64) string {
	if len(values) > 40 {
		values = buckets(values, 40)
	}
	low, high := values[0], values[0]
	for _, v := range values {
		low, high = math.Min(low, v), math.Max(high, v)
	}
	levels := []rune(ticks)
	var b strings.Builder
	for _, v := range values {
		level := 3
		if high > low {
			level = int(math.Round((v - low) / (high - low) * float64(len(levels)-1)))
		}
		b.WriteRune(levels[level])
	}
	return b.String()
}

func buckets(values []float64, n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		from, to := i*len(values)/n, (i+1)*len(values)/n
		sum := 0.0
		for _, v := range values[from:to] {
			sum += v
		}
		out[i] = sum / float64(to-from)
	}
	return out
}

// trend compares the first third of the runs with the last.
func trend(values []float64) string {
	if len(values) < 4 {
		return ""
	}
	third := len(values) / 3
	first, last := mean(values[:third]), mean(values[len(values)-third:])
	if first <= 0 {
		return ""
	}
	ratio := last / first
	switch {
	case ratio >= 1.25:
		return fmt.Sprintf("slowed down %d%%", int(math.Round((ratio-1)*100)))
	case ratio <= 0.8:
		return fmt.Sprintf("sped up %d%%", int(math.Round((1-ratio)*100)))
	}
	return "steady"
}

func mean(values []float64) float64 {
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}

func median(values []float64) float64 {
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return (sorted[mid-1] + sorted[mid]) / 2
}

// Duration renders seconds the way a person reads a stopwatch, and nothing for
// none.
func Duration(seconds int) string {
	d := time.Duration(seconds) * time.Second
	switch {
	case seconds <= 0:
		return ""
	case d < time.Minute:
		return fmt.Sprintf("%ds", seconds)
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", seconds/60, seconds%60)
	default:
		return fmt.Sprintf("%dh%02dm", seconds/3600, seconds%3600/60)
	}
}

// Clock is Duration, with zero shown as zero.
func Clock(d time.Duration) string {
	if text := Duration(int(d.Seconds())); text != "" {
		return text
	}
	return "0s"
}

// Count renders a token count in thousands or millions.
func Count(n int) string {
	switch {
	case n >= 1_000_000:
		return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(n)/1e6), ".0") + "M"
	case n >= 1000:
		return fmt.Sprintf("%dk", (n+500)/1000)
	default:
		return fmt.Sprint(n)
	}
}

func cut(text string, limit int) string {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\n", " "))
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit-1]) + "…"
}

func sortedKeys(m map[string]float64) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
