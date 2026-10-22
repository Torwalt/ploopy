package main

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Torwalt/ploopy/internal/config"
	"github.com/Torwalt/ploopy/internal/harness"
)

func TestDefaultPickersOfferOnlyThePreferredModelsAndHighEffort(t *testing.T) {
	set := allHarnesses()
	for name, want := range map[string][]string{
		"claude":   {"sonnet", "opus"},
		"codex":    {"gpt-6.1-sol"},
		"opencode": {"opencode-go/deepseek-v4.1-flash"},
	} {
		h := set.Get(name)
		if !reflect.DeepEqual(h.Models(), want) || !reflect.DeepEqual(harness.EffortChoices(h), []string{"high"}) {
			t.Fatalf("%s offers %v, %v", name, h.Models(), harness.EffortChoices(h))
		}
		if name != "opencode" && harness.FallbackModel(h, h.Models()[0]) != "" {
			t.Fatalf("%s unexpectedly has a paid fallback", name)
		}
	}
	if _, ok := set.Get("codex").(harness.Metered); !ok {
		t.Fatal("preferences lost subscription metering")
	}
}

func TestConfiguredChoicesReachEveryPickerAndKeepExplicitOverrides(t *testing.T) {
	cfg := config.Default()
	cfg.Agents = map[string]config.Agent{"codex": {
		Models: []string{"gpt-6.1-sol", "gpt-6-luna"}, Efforts: []string{"high", "xhigh"},
	}}
	e := &env{cfg: cfg, harnesses: allHarnesses(cfg)}
	if err := validateChoices(e.harnesses); err != nil {
		t.Fatal(err)
	}
	h := e.harnesses.Get("codex")
	if got := modelChoices(h, ""); !reflect.DeepEqual(got, cfg.Agents["codex"].Models) {
		t.Fatalf("models %v", got)
	}
	if got := effortChoices(h, ""); !reflect.DeepEqual(got, cfg.Agents["codex"].Efforts) {
		t.Fatalf("efforts %v", got)
	}
	choices := secondaryChoices(e.harnesses, "claude")
	if len(choices) != 6 { // wait, one Flash, and four Codex combinations
		t.Fatalf("secondary choices %v", choices)
	}
	if _, err := unattended(e, runSettings{Harness: "codex", Model: "custom-model", Effort: "max"}); err != nil {
		t.Fatalf("explicit settings must still work: %v", err)
	}
	s := runSettings{}
	fill(e, &s, given{}, &runSettings{Harness: "codex", Model: "gpt-6-astra", Effort: "low"})
	if s.Model != "gpt-6.1-sol" || s.Effort != "high" {
		t.Fatalf("stale picker choices survived: %+v", s)
	}
}

func TestFallbackConfigurationCanReplaceOrDisableTheDefault(t *testing.T) {
	for _, routes := range []map[string]string{{}, {"custom/go": "custom/api"}} {
		cfg := config.Default()
		cfg.Agents = map[string]config.Agent{"opencode": {Fallbacks: routes}}
		h := allHarnesses(cfg).Get("opencode")
		if harness.FallbackModel(h, "opencode-go/deepseek-v4.1-flash") != "" || harness.FallbackModel(h, "custom/go") != routes["custom/go"] {
			t.Fatal("the default fallback was not replaced")
		}
	}
}

func TestTheBoardShowsTheAutomaticRouteBeforeTheSecondaryPolicy(t *testing.T) {
	e := &env{harnesses: allHarnesses()}
	h := e.harnesses.Get("opencode")
	s := &runSettings{Harness: h.Name(), Model: h.Models()[0], Effort: "high"}
	board := &settingsBoard{env: e, s: s}
	column := board.bedrockColumn()
	if len(column.Lines) != 1 || !strings.Contains(column.Lines[0], "deepseek/deepseek-flash") || column.Fields[3].Label != "after" {
		t.Fatalf("column %+v", column)
	}
	chosen, err := resolveSettings(e, *s)
	if err != nil {
		t.Fatal(err)
	}
	chosen.waitOffPeak = true
	if got := peakSuffix(chosen); got != ", off-peak only" {
		t.Fatalf("peak description %q", got)
	}
}
