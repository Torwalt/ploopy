package main

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Torwalt/ploopy/internal/control"
	"github.com/Torwalt/ploopy/internal/harness"
	"github.com/Torwalt/ploopy/internal/loop"
	"github.com/Torwalt/ploopy/internal/ui"
)

type adjustFlags struct {
	run          string
	finish       string
	peak         string
	push         bool
	stop         bool
	secondary    string
	secondaryMod string
	secondaryEff string
}

func newAdjust(e *env) *cobra.Command {
	var f adjustFlags
	cmd := &cobra.Command{
		Use:   "adjust",
		Short: "Change what a running run does next",
		Long: "Change what a running run does next: its end action, whether it\n" +
			"pushes, whether it stops after the current unit, the agent that takes\n" +
			"over on a usage limit, and what a unit due in peak hours does.\n\n" +
			"With nothing passed, ploopy asks. It works from any directory.",
		Args: cobra.NoArgs,
		// A run is adjusted from anywhere, not only from inside its repository.
		PersistentPreRunE: func(*cobra.Command, []string) error {
			e.harnesses = allHarnesses()
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error { return adjust(cmd, e, &f) },
	}
	flags := cmd.Flags()
	flags.StringVar(&f.run, "run", "", "the run to change, by pid or plan name; asked for when several go on")
	flags.StringVar(&f.finish, "finish", "", "when the run ends: none, suspend or poweroff")
	flags.StringVar(&f.peak, "peak", "", "a unit due in the harness's peak hours: wait or run")
	flags.BoolVar(&f.push, "push", false, "push the branch when the run ends; --push=false to stop it")
	flags.BoolVar(&f.stop, "stop", false, "stop once the current unit is done; --stop=false to go on")
	flags.StringVar(&f.secondary, "secondary", "", "harness that takes over on a usage limit, or none")
	flags.StringVar(&f.secondaryMod, "secondary-model", "", "model for the secondary harness")
	flags.StringVar(&f.secondaryEff, "secondary-effort", "", "effort for the secondary harness")
	return cmd
}

func adjust(cmd *cobra.Command, e *env, f *adjustFlags) error {
	lives, err := control.List()
	if err != nil {
		return err
	}
	if len(lives) == 0 {
		return fmt.Errorf("no run is going on")
	}

	change, err := changeFromFlags(cmd, e, f)
	if err != nil {
		return err
	}
	if change == (control.Settings{}) && !ui.Interactive() {
		for _, live := range lives {
			fmt.Printf("%-8d %-36s %-6s %s\n", live.PID, live.Name(), live.Unit, live.Wanted.Describe())
		}
		return nil
	}

	live, err := pickRun(lives, f.run)
	if err != nil {
		return err
	}
	if change == (control.Settings{}) {
		return adjustInteractively(e, live)
	}
	return apply(live, change)
}

func apply(live control.Live, change control.Settings) error {
	updated, err := control.Adjust(live.PID, change)
	if err != nil {
		return err
	}
	fmt.Printf("%s: %s\n", updated.Name(), updated.Wanted.Describe())
	return nil
}

// pickRun finds the run to change: the one named, the only one, or the one the
// author picks.
func pickRun(lives []control.Live, which string) (control.Live, error) {
	if which != "" {
		pid, _ := strconv.Atoi(which)
		for _, live := range lives {
			stem := strings.TrimSuffix(filepath.Base(live.Plan), filepath.Ext(live.Plan))
			if live.PID == pid || strings.EqualFold(stem, which) || live.Plan == which {
				return live, nil
			}
		}
		return control.Live{}, fmt.Errorf("no run %s is going on", which)
	}
	if len(lives) == 1 {
		return lives[0], nil
	}
	choices := make([]ui.Choice, 0, len(lives))
	for _, live := range lives {
		choices = append(choices, ui.Choice{Label: runLabel(live), Value: strconv.Itoa(live.PID)})
	}
	picked, err := ui.Pick("Which run?", "run", choices)
	if err != nil {
		return control.Live{}, err
	}
	pid, _ := strconv.Atoi(picked)
	for _, live := range lives {
		if live.PID == pid {
			return live, nil
		}
	}
	return control.Live{}, fmt.Errorf("no run with pid %d", pid)
}

func runLabel(live control.Live) string {
	label := live.Name()
	if live.Unit != "" {
		label += ", unit " + live.Unit
	}
	return label + " · " + live.Wanted.Describe()
}

func changeFromFlags(cmd *cobra.Command, e *env, f *adjustFlags) (control.Settings, error) {
	var change control.Settings
	flags := cmd.Flags()
	if flags.Changed("finish") {
		if !ui.Finish(f.finish).Valid() {
			return change, fmt.Errorf("--finish must be none, suspend or poweroff, not %s", f.finish)
		}
		change.Finish = f.finish
	}
	if flags.Changed("peak") {
		if f.peak != "wait" && f.peak != "run" {
			return change, fmt.Errorf("--peak must be wait or run, not %s", f.peak)
		}
		change.Peak = f.peak
	}
	if flags.Changed("push") {
		change.Push = &f.push
	}
	if flags.Changed("stop") {
		change.Stop = &f.stop
	}
	if flags.Changed("secondary") {
		if f.secondary == "none" {
			change.Secondary = &control.Agent{}
		} else {
			agent, err := resolveAgent(e.harnesses, "--secondary", f.secondary, f.secondaryMod, f.secondaryEff, "")
			if err != nil {
				return change, err
			}
			change.Secondary = toControlAgent(agent)
		}
	}
	return change, nil
}

func adjustInteractively(e *env, live control.Live) error {
	wanted := live.Wanted
	setting, err := ui.Pick(live.Name()+": change what?", "finish", []ui.Choice{
		{Label: "when the run ends: " + finishLabel(wanted.Finish), Value: "finish"},
		{Label: "push when the run ends: " + yesNo(wanted.Pushes()), Value: "push"},
		{Label: "stop after the current unit: " + yesNo(wanted.Stops()), Value: "stop"},
		{Label: "on a usage limit: " + secondaryLabel(wanted.SecondaryAgent()), Value: "secondary"},
		{Label: "a unit due in peak hours: " + peakLabel(wanted.Peak), Value: "peak"},
	})
	if err != nil {
		return err
	}

	var change control.Settings
	switch setting {
	case "finish":
		if change.Finish, err = ui.Pick("When the run ends?", "finish", ui.Finishes); err != nil {
			return err
		}
	case "push":
		picked, err := ui.Pick("Push the branch when the run ends?", "push", yesNoChoices(wanted.Pushes()))
		if err != nil {
			return err
		}
		push := picked == "yes"
		change.Push = &push
	case "stop":
		picked, err := ui.Pick("Stop once the current unit is done?", "stop", yesNoChoices(wanted.Stops()))
		if err != nil {
			return err
		}
		stop := picked == "yes"
		change.Stop = &stop
	case "secondary":
		primary := strings.Fields(live.Agent)
		except := ""
		if len(primary) > 0 {
			except = primary[0]
		}
		picked, err := ui.Pick("When "+except+" hits its usage limit?", "secondary",
			secondaryChoices(e.harnesses, except))
		if err != nil {
			return err
		}
		agent := decodeAgent(picked)
		if agent == nil {
			agent = &control.Agent{}
		}
		change.Secondary = agent
	case "peak":
		if change.Peak, err = ui.Pick("A unit due to start in peak hours?", "peak", peakChoices); err != nil {
			return err
		}
	}
	return apply(live, change)
}

var peakChoices = []ui.Choice{
	{Label: "wait for off-peak", Value: "wait"},
	{Label: "run anyway", Value: "run"},
}

// secondaryChoices offers every agent of the other harnesses, after waiting
// the limit out.
func secondaryChoices(harnesses harness.Set, except string) []ui.Choice {
	choices := []ui.Choice{{Label: "wait it out", Value: ""}}
	for _, h := range harnesses {
		if h.Name() == except {
			continue
		}
		for _, model := range h.Models() {
			for _, effort := range h.Efforts() {
				agent := control.Agent{Harness: h.Name(), Model: model, Effort: effort}
				choices = append(choices, ui.Choice{Label: "switch to " + agent.Label(), Value: encodeAgent(&agent)})
			}
		}
	}
	return choices
}

func encodeAgent(agent *control.Agent) string {
	if agent == nil || agent.Harness == "" {
		return ""
	}
	return agent.Harness + "|" + agent.Model + "|" + agent.Effort
}

func decodeAgent(value string) *control.Agent {
	parts := strings.SplitN(value, "|", 3)
	if len(parts) != 3 || parts[0] == "" {
		return nil
	}
	return &control.Agent{Harness: parts[0], Model: parts[1], Effort: parts[2]}
}

// resolveAgent turns a harness named on the command line into an agent. A
// model left out is the harness's default; an effort left out is the
// preferred one when the harness has it, else its first.
func resolveAgent(harnesses harness.Set, flag, name, model, effort, preferred string) (*loop.Agent, error) {
	h := harnesses.Get(name)
	if h == nil {
		return nil, fmt.Errorf("%s must be one of %s or none, not %s",
			flag, strings.Join(harnesses.Names(), ", "), name)
	}
	if model == "" {
		model = h.Models()[0]
	}
	if effort == "" {
		effort = h.Efforts()[0]
		if contains(h.Efforts(), preferred) {
			effort = preferred
		}
	}
	if !contains(h.Efforts(), effort) {
		return nil, fmt.Errorf("%s-effort for %s must be one of %s",
			flag, h.Name(), strings.Join(h.Efforts(), ", "))
	}
	return &loop.Agent{Harness: h, Model: model, Effort: effort}, nil
}

func yesNoChoices(current bool) []ui.Choice {
	choices := []ui.Choice{{Label: "yes", Value: "yes"}, {Label: "no", Value: "no"}}
	if current {
		return choices
	}
	return []ui.Choice{choices[1], choices[0]}
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func finishLabel(finish string) string {
	switch ui.Finish(finish) {
	case ui.FinishSuspend:
		return "suspend"
	case ui.FinishPoweroff:
		return "power off"
	}
	return "nothing"
}

func secondaryLabel(agent *control.Agent) string {
	if agent == nil {
		return "wait it out"
	}
	return "switch to " + agent.Label()
}

func peakLabel(peak string) string {
	if peak == "wait" {
		return "wait for off-peak"
	}
	return "run anyway"
}
