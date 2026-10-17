package main

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Torwalt/ploopy/internal/control"
	"github.com/Torwalt/ploopy/internal/harness"
	"github.com/Torwalt/ploopy/internal/history"
	"github.com/Torwalt/ploopy/internal/lease"
	"github.com/Torwalt/ploopy/internal/loop"
	"github.com/Torwalt/ploopy/internal/plan"
	"github.com/Torwalt/ploopy/internal/repo"
	"github.com/Torwalt/ploopy/internal/review"
	"github.com/Torwalt/ploopy/internal/state"
	"github.com/Torwalt/ploopy/internal/ui"
)

// settingsBoard is the run's board: the bedrock, one column per subscription,
// and the run's own settings. It keeps the leases as drafts until the author
// starts the run, so a cancel changes nothing.
type settingsBoard struct {
	ctx      context.Context
	env      *env
	plan     *plan.Plan
	branch   string
	open     int
	root     string
	adjust   bool
	given    given
	s        *runSettings
	triage   *triageDraft
	drafts   []*leaseDraft
	records  []history.Record
	canPush  bool
	leaseErr error

	secondary string
	spend     string
	push      string
	stop      string
	then      string
	lastAgent string
	board     *ui.Board
}

// triageDraft is the Triage column of a review's board. It stays editable
// until the triage has run, then the whole column is locked.
type triageDraft struct {
	agent  string
	model  string
	effort string
	budget string
	given  map[string]bool
	locked string
	last   string
}

// triageRanLock is why a review's triage cannot change.
const triageRanLock = "the triage has run"

func (d *triageDraft) lock(key string) string {
	if d.locked != "" {
		return d.locked
	}
	if d.given[key] {
		return "from a flag"
	}
	return ""
}

// leaseDraft is one subscription's lease and what it runs as on the board.
type leaseDraft struct {
	h      harness.Harness
	term   string // mine, until <clock>, nightly <span>, until I reclaim
	model  string
	effort string
	keep   string

	initial       lease.Lease
	initialTerm   string
	initialModel  string
	initialEffort string
	initialKeep   string
	initialLent   bool

	budget *harness.Budget
	seen   string
}

const (
	termMine    = "mine"
	termUntil   = "until"
	termNightly = "nightly"
	termOpen    = "open"
)

func newSettingsBoard(ctx context.Context, e *env, p *plan.Plan, g given, s *runSettings, branch string, open int) *settingsBoard {
	sb := &settingsBoard{
		ctx: ctx, env: e, plan: p, branch: branch, open: open, root: e.root, given: g, s: s,
		secondary: encodeAgent(s.Secondary), spend: spendLabel(s.SpendAll), push: yesNo(s.Push),
		then: thenValue(s.Then), lastAgent: s.Harness, canPush: repo.New(e.root, nil).CanPush(ctx, branch) == nil,
	}
	sb.records = loadHistory(ctx, sb.root)
	sb.drafts, sb.leaseErr = loadLeaseDrafts(ctx, e, sb.records, time.Now())
	sb.rebuild()
	return sb
}

// adjustLock is why a running run's bedrock cannot change.
const adjustLock = "a running run keeps its agent"

// newAdjustBoard builds the same board for a run that is going. The bedrock's
// agent, model and effort are locked; everything else is applied on Enter.
func newAdjustBoard(ctx context.Context, e *env, live control.Live) *settingsBoard {
	wanted := live.Wanted
	s := runSettings{
		Secondary:     wanted.Secondary,
		Peak:          wanted.Peak,
		Subscriptions: wanted.Subscriptions,
		SpendAll:      wanted.SpendAll != nil && *wanted.SpendAll,
		Push:          wanted.Pushes(),
		Finish:        wanted.Finish,
		Then:          wanted.Then,
	}
	if fields := strings.Fields(live.Agent); len(fields) > 0 {
		s.Harness = fields[0]
		if len(fields) > 1 {
			s.Model = fields[1]
		}
		if len(fields) > 2 {
			s.Effort = fields[2]
		}
	}
	if s.Subscriptions == "" {
		s.Subscriptions = "all"
	}
	if s.Peak == "" {
		s.Peak = "run"
	}
	if s.Finish == "" {
		s.Finish = string(ui.FinishNone)
	}
	var g given
	g.harness, g.model, g.effort = true, true, true
	g.from("harness", adjustLock)
	g.from("model", adjustLock)
	g.from("effort", adjustLock)

	p := plan.Parse("", live.Plan)
	open := 0
	if loaded, err := plan.Load(filepath.Join(live.Root, live.Plan), live.Plan); err == nil {
		p = loaded
		if st, err := state.Load(filepath.Join(live.Root, state.PathFor(live.Plan))); err == nil {
			open = openUnits(p, st)
		}
	}

	sb := &settingsBoard{
		ctx: ctx, env: e, plan: p, branch: live.Branch, open: open, root: live.Root,
		adjust: true, given: g, s: &s,
		secondary: encodeAgent(wanted.SecondaryAgent()), spend: spendLabel(s.SpendAll),
		push: yesNo(s.Push), stop: yesNo(wanted.Stops()), then: thenValue(wanted.Then), lastAgent: s.Harness,
		canPush: repo.New(live.Root, nil).CanPush(ctx, live.Branch) == nil,
	}
	sb.records = loadHistory(ctx, sb.root)
	sb.drafts, sb.leaseErr = loadLeaseDrafts(ctx, e, sb.records, time.Now())
	sb.rebuild()
	return sb
}

// newReviewBoard is the run's board with the review's Triage column before
// the Run column.
func newReviewBoard(ctx context.Context, e *env, p *plan.Plan, g given, s *runSettings, branch string, open int, t review.Triage, givens map[string]bool, locked string) *settingsBoard {
	sb := newSettingsBoard(ctx, e, p, g, s, branch, open)
	sb.triage = &triageDraft{
		agent: t.Agent, model: t.Model, effort: t.Effort,
		budget: strconv.FormatFloat(t.Budget, 'f', -1, 64),
		given:  givens, locked: locked, last: t.Agent,
	}
	sb.normalize()
	sb.rebuild()
	return sb
}

func (sb *settingsBoard) rebuild() {
	sb.board = ui.NewBoard(sb.title(), sb.columns(), sb.footer)
	if sb.adjust {
		sb.board.Action = "apply"
	}
	sb.board.OnChange = func() {
		sb.normalize()
		sb.board.SetColumns(sb.columns())
	}
}

func (sb *settingsBoard) title() string {
	return fmt.Sprintf("ploopy · %s on %s · %d open units", sb.plan.Name(), sb.branch, sb.open)
}

func (sb *settingsBoard) normalize() {
	sb.normalizeTriage()
	sb.s.SpendAll = sb.spend == "all"
	sb.s.Secondary = decodeAgent(sb.secondary)
	h := sb.env.harnesses.Get(sb.s.Harness)
	if h == nil {
		return
	}
	if sb.lastAgent != "" && sb.lastAgent != sb.s.Harness {
		sb.s.Model = h.Models()[0]
		sb.s.Effort = preferredEffort(h)
	}
	if !contains(h.Efforts(), sb.s.Effort) {
		sb.s.Effort = preferredEffort(h)
	}
	if agent := decodeAgent(sb.secondary); agent != nil {
		if agent.Harness == sb.s.Harness || sb.env.harnesses.Get(agent.Harness) == nil {
			sb.secondary = ""
		}
	}
	sb.lastAgent = sb.s.Harness
}

func (sb *settingsBoard) normalizeTriage() {
	d := sb.triage
	if d == nil {
		return
	}
	h := sb.env.harnesses.Get(d.agent)
	if h == nil {
		d.agent = sb.env.harnesses[0].Name()
		h = sb.env.harnesses.Get(d.agent)
	}
	if d.last != d.agent {
		d.model = triageModels(sb.env, d.agent)[0]
		d.effort = preferredEffort(h)
	}
	if !contains(h.Efforts(), d.effort) {
		d.effort = preferredEffort(h)
	}
	if d.budget == "" {
		d.budget = "0"
	}
	d.last = d.agent
}

func (sb *settingsBoard) columns() []ui.Column {
	var columns []ui.Column
	columns = append(columns, sb.bedrockColumn())
	for _, d := range sb.drafts {
		if d.h.Name() == sb.s.Harness {
			continue
		}
		columns = append(columns, sb.subscriptionColumn(d))
	}
	if sb.triage != nil {
		columns = append(columns, sb.triageColumn())
	}
	columns = append(columns, sb.runColumn())
	return columns
}

func (sb *settingsBoard) triageColumn() ui.Column {
	d := sb.triage
	return ui.Column{Title: "Triage", Fields: []ui.BoardField{
		{Key: 'a', Label: "agent", Value: &d.agent, Choices: func() []ui.Choice {
			return labelled(sb.env.harnesses.Names())
		}, Locked: d.lock("agent")},
		{Key: 'm', Label: "model", Value: &d.model, Choices: func() []ui.Choice {
			return labelled(triageModelChoices(sb.env, d.agent, d.model))
		}, Locked: d.lock("model")},
		{Key: 'e', Label: "effort", Value: &d.effort, Choices: func() []ui.Choice {
			return labelled(effortChoices(sb.env.harnesses.Get(d.agent), d.effort))
		}, Locked: d.lock("effort")},
		{Key: 'b', Label: "cap", Value: &d.budget, Choices: func() []ui.Choice {
			return budgetOptions(d.budget)
		}, Locked: d.lock("budget")},
	}}
}

func triageModelChoices(e *env, agent, current string) []string {
	models := triageModels(e, agent)
	if current != "" && !contains(models, current) {
		models = append([]string{current}, models...)
	}
	return models
}

func (sb *settingsBoard) bedrockColumn() ui.Column {
	s := sb.s
	h := sb.env.harnesses.Get(s.Harness)
	fields := []ui.BoardField{
		{Key: 'a', Label: "agent", Value: &s.Harness, Choices: func() []ui.Choice {
			return labelled(sb.env.harnesses.Names())
		}, Locked: sb.given.why("harness")},
		{Key: 'm', Label: "model", Value: &s.Model, Choices: func() []ui.Choice {
			return labelled(modelChoices(h, s.Model))
		}, Locked: sb.given.why("model")},
		{Key: 'e', Label: "effort", Value: &s.Effort, Choices: func() []ui.Choice {
			return labelled(effortChoices(h, s.Effort))
		}, Locked: sb.given.why("effort")},
		{Key: 'l', Label: "limit", Value: &sb.secondary, Choices: func() []ui.Choice {
			return secondaryChoices(sb.env.harnesses, s.Harness)
		}, Locked: sb.given.why("secondary")},
	}
	if peaked := peakedHarness(sb.env.harnesses); peaked != nil {
		fields = append(fields, ui.BoardField{Key: 'o', Label: "peak", Value: &s.Peak,
			Choices: func() []ui.Choice { return peakChoices }, Locked: sb.given.why("peak")})
	}
	return ui.Column{Title: "Bedrock", Fields: fields}
}

func (sb *settingsBoard) subscriptionColumn(d *leaseDraft) ui.Column {
	fields := []ui.BoardField{
		{Key: 'u', Label: "lent", Value: &d.term, Choices: func() []ui.Choice {
			return leaseTermChoices(d, time.Now())
		}},
		{Key: 'm', Label: "model", Value: &d.model, Choices: func() []ui.Choice {
			return labelled(modelChoices(d.h, d.model))
		}},
		{Key: 'e', Label: "effort", Value: &d.effort, Choices: func() []ui.Choice {
			return labelled(effortChoices(d.h, d.effort))
		}},
		{Key: 'k', Label: "keep", Value: &d.keep, Choices: func() []ui.Choice {
			return keepChoices(d, time.Now())
		}},
	}
	now := time.Now()
	title, dim := d.h.Name(), !subscriptionInSet(sb.s.Subscriptions, d.h.Name())
	if dim {
		title += " · not this run"
	}
	return ui.Column{
		Title:  title,
		Fields: fields,
		Lines:  []string{d.budget.Describe(now) + d.seen},
		Dim:    dim,
	}
}

func (sb *settingsBoard) runColumn() ui.Column {
	s := sb.s
	fields := []ui.BoardField{
		{Key: 's', Label: "subs", Value: &s.Subscriptions, Choices: func() []ui.Choice {
			return subscriptionChoices(sb.env.harnesses, s.Harness)
		}, Locked: sb.given.why("subscriptions")},
		{Key: 'w', Label: "week", Value: &sb.spend, Choices: func() []ui.Choice {
			return spendChoices
		}, Locked: sb.given.why("spendAll")},
	}
	if sb.canPush {
		fields = append(fields, ui.BoardField{Key: 'p', Label: "push", Value: &sb.push,
			Choices: func() []ui.Choice { return yesNoChoices(true) }, Locked: sb.given.why("push")})
	}
	fields = append(fields, ui.BoardField{Key: 'f', Label: "end", Value: &s.Finish,
		Choices: func() []ui.Choice { return ui.Finishes }, Locked: sb.given.why("finish")})
	if sb.triage != nil || sb.adjust {
		fields = append(fields, ui.BoardField{Key: 'n', Label: "then", Value: &sb.then,
			Choices: func() []ui.Choice { return thenChoices }, Locked: sb.given.why("then")})
	}
	if sb.adjust {
		fields = append(fields, ui.BoardField{Key: 'x', Label: "stop", Value: &sb.stop,
			Choices: func() []ui.Choice { return yesNoChoices(sb.stop == "yes") }})
	}
	return ui.Column{Title: "Run", Fields: fields}
}

func (sb *settingsBoard) footer() []string {
	now := time.Now()
	var parts []forecastPart
	var lines []string
	for _, d := range sb.drafts {
		if d.h.Name() == sb.s.Harness {
			continue
		}
		if !subscriptionInSet(sb.s.Subscriptions, d.h.Name()) {
			parts = append(parts, forecastPart{
				Name: d.h.Name(), Budget: d.budget,
				Estimate: history.For(sb.records, d.agentLabel()),
			})
			continue
		}
		l, lent, err := d.loopLease(now)
		if err != nil {
			lines = append(lines, d.h.Name()+": "+err.Error())
			lent = false
		}
		var lease *loop.Lease
		if lent {
			lease = &l
		}
		parts = append(parts, forecastPart{
			Name: d.h.Name(), Lease: lease, Budget: d.budget,
			Estimate: history.For(sb.records, d.agentLabel()),
		})
	}
	h := sb.env.harnesses.Get(sb.s.Harness)
	bedrock := loop.Agent{Harness: h, Model: sb.s.Model, Effort: sb.s.Effort}.Label()
	result := forecastRun(parts, sb.s.Harness, history.For(sb.records, bedrock), sb.open, now, sb.s.SpendAll)
	lines = append([]string{result.String()}, lines...)
	if sb.leaseErr != nil {
		lines = append(lines, "the lease file cannot be read: "+sb.leaseErr.Error())
	}
	return lines
}

func (sb *settingsBoard) run() error {
	if _, err := sb.board.Run(); err != nil {
		return err
	}
	sb.read()
	return nil
}

// finish resolves the board's settings, writes the leases it changed and
// remembers the run and the triage.
func (sb *settingsBoard) finish(now time.Time) (chosenRun, error) {
	chosen, err := resolveSettings(sb.env, *sb.s)
	if err != nil {
		return chosenRun{}, err
	}
	if err := sb.saveLeases(now); err != nil {
		return chosenRun{}, err
	}
	saveLast(sb.ctx, sb.env.root, *sb.s)
	if sb.triage != nil {
		t, err := sb.triageValue()
		if err != nil {
			return chosenRun{}, err
		}
		saveLastTriage(sb.ctx, sb.env.root, t)
	}
	return chosen, nil
}

func (sb *settingsBoard) triageValue() (review.Triage, error) {
	d := sb.triage
	if d == nil {
		return review.Triage{}, nil
	}
	budget, err := strconv.ParseFloat(d.budget, 64)
	if err != nil {
		return review.Triage{}, fmt.Errorf("the cap %q is not dollars", d.budget)
	}
	t := review.Triage{Agent: d.agent, Model: d.model, Effort: d.effort, Budget: budget}
	if err := validateTriage(sb.env, t); err != nil {
		return review.Triage{}, err
	}
	return t, nil
}

// read takes the board's values into the run settings.
func (sb *settingsBoard) read() {
	sb.s.Secondary = decodeAgent(sb.secondary)
	sb.s.SpendAll = sb.spend == "all"
	sb.s.Push = sb.push == "yes"
	sb.s.Then = thenValue(sb.then)
}

// adjustChange is everything the adjust board shows, as a change. Every field
// is sent, so the board's state is what the run gets, not a partial overlay.
func (sb *settingsBoard) adjustChange() control.Settings {
	secondary := sb.s.Secondary
	if secondary == nil {
		secondary = &control.Agent{}
	}
	stop := sb.stop == "yes"
	return control.Settings{
		Finish:        sb.s.Finish,
		Peak:          sb.s.Peak,
		Push:          &sb.s.Push,
		Stop:          &stop,
		Secondary:     secondary,
		Subscriptions: sb.s.Subscriptions,
		SpendAll:      &sb.s.SpendAll,
		Then:          thenValue(sb.then),
	}
}

var thenChoices = []ui.Choice{
	{Label: "stop", Value: "stop"},
	{Label: "run the fix plan", Value: "fixes"},
}

// applyAdjust writes the changed leases and lays the board's settings over
// the running run.
func (sb *settingsBoard) applyAdjust(live control.Live, now time.Time) error {
	if err := sb.saveLeases(now); err != nil {
		return err
	}
	updated, err := control.Adjust(live.PID, sb.adjustChange())
	if err != nil {
		return err
	}
	fmt.Printf("%s: %s\n", updated.Name(), updated.Wanted.Describe())
	return nil
}

// saveLeases writes only the leases the author changed. A subscription
// reclaimed on the board is removed from the file.
func (sb *settingsBoard) saveLeases(now time.Time) error {
	changed := false
	for _, d := range sb.drafts {
		changed = changed || d.changed()
	}
	if !changed {
		return nil
	}
	file, err := lease.Load()
	if err != nil {
		return err
	}
	for _, d := range sb.drafts {
		if !d.changed() {
			continue
		}
		if termKind(d.term) == termMine {
			file = file.Reclaim(d.h.Name())
			continue
		}
		l, _, err := d.loopLease(now)
		if err != nil {
			return err
		}
		file = file.Set(fileLease(l))
	}
	return lease.Save(file)
}

func loadLeaseDrafts(ctx context.Context, e *env, records []history.Record, now time.Time) ([]*leaseDraft, error) {
	file, err := lease.Load()
	active := map[string]lease.Lease{}
	if err == nil {
		for _, l := range file.Active(now) {
			active[l.Harness] = l
		}
	}
	var drafts []*leaseDraft
	for _, h := range e.harnesses {
		if _, metered := h.(harness.Metered); !metered {
			continue
		}
		d := &leaseDraft{h: h, term: termMine, model: h.Models()[0], effort: preferredEffort(h), keep: "20%"}
		if l, ok := active[h.Name()]; ok {
			d.model, d.effort, d.keep = l.Model, l.Effort, fmt.Sprintf("%d%%", l.Keep)
			if d.model == "" {
				d.model = h.Models()[0]
			}
			if d.effort == "" {
				d.effort = preferredEffort(h)
			}
			d.term, d.initial, d.initialLent = leaseTerm(l, now), l, true
		}
		d.initialTerm, d.initialModel, d.initialEffort, d.initialKeep = d.term, d.model, d.effort, d.keep
		d.budget, d.seen = windowsNow(ctx, h, records)
		drafts = append(drafts, d)
	}
	return drafts, err
}

func (d *leaseDraft) agentLabel() string {
	return loop.Agent{Harness: d.h, Model: d.model, Effort: d.effort}.Label()
}

func (d *leaseDraft) changed() bool {
	if d.term == d.initialTerm && d.model == d.initialModel &&
		d.effort == d.initialEffort && d.keep == d.initialKeep {
		return false
	}
	// Editing what a subscription would run as while it stays mine lends
	// nothing and must not create a lease.
	return !(d.term == termMine && d.initialTerm == termMine)
}

func (d *leaseDraft) loopLease(now time.Time) (loop.Lease, bool, error) {
	keep, err := strconv.Atoi(strings.TrimSuffix(d.keep, "%"))
	if err != nil || keep < 0 || keep > 100 {
		return loop.Lease{}, false, fmt.Errorf("keep must be a percentage, not %q", d.keep)
	}
	l := loop.Lease{
		Agent: loop.Agent{Harness: d.h, Model: d.model, Effort: d.effort},
		Keep:  float64(keep) / 100,
	}
	kind := termKind(d.term)
	if kind == termMine {
		return l, false, nil
	}
	if d.term == d.initialTerm && d.initialLent {
		l.Kind, l.Until = loop.LeaseKind(d.initial.Kind), d.initial.Until
		if d.initial.Kind == lease.Nightly {
			span, err := loop.ParseAway(d.initial.Span)
			if err != nil {
				return loop.Lease{}, false, err
			}
			l.Span = span
		}
		return l, true, nil
	}
	switch kind {
	case termUntil:
		at, err := untilAt(strings.TrimSpace(strings.TrimPrefix(d.term, "until ")), now)
		if err != nil {
			return loop.Lease{}, false, err
		}
		l.Kind, l.Until = loop.LeaseUntil, at
	case termNightly:
		span, err := loop.ParseAway(strings.TrimSpace(strings.TrimPrefix(d.term, "nightly ")))
		if err != nil {
			return loop.Lease{}, false, err
		}
		l.Kind, l.Span = loop.LeaseNightly, span
	case termOpen:
		l.Kind = loop.LeaseOpen
	default:
		return loop.Lease{}, false, fmt.Errorf("unknown lease term %q", d.term)
	}
	return l, true, nil
}

func fileLease(l loop.Lease) lease.Lease {
	out := lease.Lease{
		Harness: l.Harness.Name(),
		Model:   l.Model,
		Effort:  l.Effort,
		Kind:    lease.Kind(l.Kind),
		Until:   l.Until,
		Keep:    int(math.Round(l.Keep * 100)),
	}
	if l.Kind == loop.LeaseNightly {
		out.Span = l.Span.String()
	}
	return out
}

func leaseTerm(l lease.Lease, now time.Time) string {
	switch l.Kind {
	case lease.Until:
		return "until " + leaseClock(l.Until, now)
	case lease.Nightly:
		return "nightly " + pretty(l.Span)
	default:
		return "until I reclaim"
	}
}

func termKind(term string) string {
	switch {
	case term == termMine:
		return termMine
	case strings.HasPrefix(term, "until I reclaim"):
		return termOpen
	case strings.HasPrefix(term, "until "):
		return termUntil
	case strings.HasPrefix(term, "nightly "):
		return termNightly
	}
	return termMine
}

func leaseTermChoices(d *leaseDraft, now time.Time) []ui.Choice {
	until := "until " + leaseClock(nextClockOrNow("09:00", now), now)
	nightly := "nightly " + pretty("21:00-09:00")
	switch termKind(d.term) {
	case termUntil:
		until = d.term
	case termNightly:
		nightly = d.term
	}
	return []ui.Choice{
		{Label: "mine", Value: termMine},
		{Label: until, Value: until},
		{Label: nightly, Value: nightly},
		{Label: "until I reclaim", Value: "until I reclaim"},
	}
}

func nextClockOrNow(text string, now time.Time) time.Time {
	at, err := nextClock(text, now)
	if err != nil {
		return now
	}
	return at
}

func untilAt(text string, now time.Time) (time.Time, error) {
	if at, err := time.ParseInLocation("15:04", text, now.Location()); err == nil {
		candidate := time.Date(now.Year(), now.Month(), now.Day(), at.Hour(), at.Minute(), 0, 0, now.Location())
		if !candidate.After(now) {
			candidate = candidate.AddDate(0, 0, 1)
		}
		return candidate, nil
	}
	parts := strings.Fields(text)
	if len(parts) == 2 {
		if at, err := time.ParseInLocation("15:04", parts[1], now.Location()); err == nil {
			if day, ok := weekday(parts[0]); ok {
				for ahead := 0; ahead < 8; ahead++ {
					candidate := time.Date(now.Year(), now.Month(), now.Day()+ahead,
						at.Hour(), at.Minute(), 0, 0, now.Location())
					if candidate.Weekday() == day && candidate.After(now) {
						return candidate, nil
					}
				}
			}
		}
	}
	return time.Time{}, fmt.Errorf("until must look like 09:00, not %q", text)
}

func weekday(name string) (time.Weekday, bool) {
	for day := time.Sunday; day <= time.Saturday; day++ {
		if strings.EqualFold(name, day.String()[:3]) {
			return day, true
		}
	}
	return 0, false
}

func keepChoices(d *leaseDraft, now time.Time) []ui.Choice {
	values := []string{"0%", "10%", "20%", "30%", "40%", "50%", "100%"}
	if !contains(values, d.keep) {
		values = append([]string{d.keep}, values...)
	}
	choices := make([]ui.Choice, 0, len(values))
	for _, value := range values {
		label := value
		if value == d.keep {
			if keep, err := strconv.Atoi(strings.TrimSuffix(value, "%")); err == nil {
				label = keepNote(keep, d.budget, now)
			}
		}
		choices = append(choices, ui.Choice{Label: label, Value: value})
	}
	return choices
}

func modelChoices(h harness.Harness, current string) []string {
	models := h.Models()
	if current != "" && !contains(models, current) {
		models = append([]string{current}, models...)
	}
	return models
}

func effortChoices(h harness.Harness, current string) []string {
	efforts := h.Efforts()
	if current != "" && !contains(efforts, current) {
		efforts = append([]string{current}, efforts...)
	}
	return efforts
}

func subscriptionInSet(set, name string) bool {
	switch set {
	case "", "all":
		return true
	case "none":
		return false
	}
	return contains(strings.Fields(set), name)
}
