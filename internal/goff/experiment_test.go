package goff

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-feature-flag/studio/pkg/splits"
	"gopkg.in/yaml.v3"
)

func experimentFile(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/experiment.goff.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func flagNamed(t *testing.T, content []byte, key string) Flag {
	t.Helper()
	flags, broken, err := New().Parse("f.yaml", content)
	if err != nil {
		t.Fatal(err)
	}
	if len(broken) != 0 {
		t.Fatalf("broken: %+v", broken)
	}
	for _, f := range flags {
		if f.Key == key {
			return f
		}
	}
	t.Fatalf("flag %s not found", key)
	return Flag{}
}

// experimentLines returns the file's metadata.experiment block verbatim.
func experimentLines(t *testing.T, content string) string {
	t.Helper()
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if line != "    experiment:" {
			continue
		}
		end := i + 1
		for end < len(lines) && (strings.HasPrefix(lines[end], "     ") || strings.TrimSpace(lines[end]) == "") {
			end++
		}
		return strings.TrimRight(strings.Join(lines[i:end], "\n"), "\n ")
	}
	t.Fatalf("experiment block not found in:\n%s", content)
	return ""
}

func TestParseExposesTheExperimentBlock(t *testing.T) {
	f := flagNamed(t, experimentFile(t), "request-timeout")
	if f.Experiment == nil {
		t.Fatal("experiment should be parsed")
	}
	a := f.Experiment.Allocations["exp-region-a"]
	if a == nil || len(a.Splits) != 3 || a.Splits[2].Shards[1].Ranges[0] != (splits.Range{Start: 6667, End: 10000}) {
		t.Fatalf("allocation = %+v", a)
	}
	if !f.Rules[0].HasAllocation || f.Rules[1].HasAllocation {
		t.Errorf("hasAllocation = %t, %t", f.Rules[0].HasAllocation, f.Rules[1].HasAllocation)
	}
	if plain := flagNamed(t, experimentFile(t), "plain"); plain.Experiment != nil {
		t.Error("a flag without a block should have a nil experiment")
	}
}

func TestExperimentIsNullInJSONWhenAbsent(t *testing.T) {
	plain := flagNamed(t, experimentFile(t), "plain")
	raw, err := json.Marshal(plain)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"experiment":null`) {
		t.Errorf("want experiment:null, got %s", raw)
	}
}

func TestMalformedExperimentIsReportedNotFatal(t *testing.T) {
	src := []byte(`f:
  variations: {a: 1, b: 2}
  defaultRule: {variation: a}
  metadata:
    experiment:
      version: 1
      allocations: {r: {splits: [{variation: a, shards: [{salt: s, ranges: [[1]]}]}]}}
`)
	f := flagNamed(t, src, "f")
	if f.Experiment != nil || !strings.Contains(f.ExperimentError, "range") {
		t.Errorf("experiment = %+v, error = %q", f.Experiment, f.ExperimentError)
	}
}

func TestEditingOtherFieldsKeepsTheExperimentByteIdentical(t *testing.T) {
	a := New()
	original := experimentFile(t)

	for name, edit := range map[string]func(*Flag){
		"toggle":          func(f *Flag) { f.Enabled = false },
		"default":         func(f *Flag) { f.Default = Outcome{Variation: "slow"} },
		"rule outcome":    func(f *Flag) { f.Rules[1].Outcome = Outcome{Variation: "slow"} },
		"rule query":      func(f *Flag) { f.Rules[0].Query = `region in ["region-a", "region-b"]`; f.Rules[0].Advanced = true },
		"variation value": func(f *Flag) { f.Variations[0].Value = 175 },
		"team metadata":   func(f *Flag) { f.Metadata["owner"] = "someone" },
	} {
		t.Run(name, func(t *testing.T) {
			f := flagNamed(t, original, "request-timeout")
			edit(&f)
			out, err := a.Serialize(original, "request-timeout", f)
			if err != nil {
				t.Fatal(err)
			}
			if err := a.Validate(out); err != nil {
				t.Fatal(err)
			}
			if got, want := experimentLines(t, string(out)), experimentLines(t, string(original)); got != want {
				t.Errorf("experiment block changed.\n--- want ---\n%s\n--- got ---\n%s", want, got)
			}
		})
	}
}

func TestRoundTripWithExperimentIsByteIdentical(t *testing.T) {
	a := New()
	original := experimentFile(t)
	out := original
	flags, _, _ := a.Parse("f.yaml", original)
	for _, f := range flags {
		var err error
		if out, err = a.Serialize(out, f.Key, f); err != nil {
			t.Fatal(err)
		}
	}
	if string(out) != string(original) {
		t.Errorf("round trip changed the file:\n%s", out)
	}
}

func TestGrowingExposureDiffsOnlyTheExposureRanges(t *testing.T) {
	a := New()
	original := experimentFile(t)
	f := flagNamed(t, original, "request-timeout")

	f.Experiment = f.Experiment.Clone()
	if _, _, err := splits.GrowExposure(f.Experiment.Allocations["exp-region-a"], 2, 10000); err != nil {
		t.Fatal(err)
	}
	if !f.ExperimentChanged() {
		t.Fatal("the change should be detected")
	}
	out, err := a.Serialize(original, "request-timeout", f)
	if err != nil {
		t.Fatal(err)
	}

	added, removed := lineDiff(string(original), string(out))
	if len(added) != 3 || len(removed) != 3 {
		t.Fatalf("want 3 lines changed, got +%v -%v\n%s", added, removed, out)
	}
	for _, line := range added {
		if strings.TrimSpace(line) != `- {salt: "c1e0a7d25f", ranges: [[0, 200]]}` {
			t.Errorf("unexpected added line %q", line)
		}
	}
	for _, keep := range []string{"# imported allocation key kept", "# one entry per arm", "# Request timeout experiments."} {
		if !strings.Contains(string(out), keep) {
			t.Errorf("lost comment %q", keep)
		}
	}
	back := flagNamed(t, out, "request-timeout")
	if back.Experiment.Allocations["exp-region-a"].Splits[1].Shards[0].Ranges[0] != (splits.Range{Start: 0, End: 200}) {
		t.Errorf("did not read back: %+v", back.Experiment.Allocations["exp-region-a"])
	}
}

func TestCreatingAnExperimentWritesTheContractShape(t *testing.T) {
	a := New()
	original := experimentFile(t)
	f := flagNamed(t, original, "plain")

	built, err := splits.BuildSplits([]splits.Arm{{Variation: "off", Weight: 1}, {Variation: "on", Weight: 1}}, 10, 10000,
		func() (string, error) { return "fixed", nil })
	if err != nil {
		t.Fatal(err)
	}
	built[0].Shards[1].Salt, built[1].Shards[1].Salt = "arm", "arm"
	exp, err := splits.ParseJSON([]byte(`{"version":1}`))
	if err != nil {
		t.Fatal(err)
	}
	f.Rules = []Rule{{Name: "exp-all", Query: "targetingKey pr", Outcome: Outcome{Variation: "off"}}}
	exp.Allocations["exp-all"] = &splits.Allocation{ExperimentKey: "plain-exp-all", Splits: built}
	f.Experiment = exp

	out, err := a.Serialize(original, "plain", f)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Validate(out); err != nil {
		t.Fatal(err)
	}
	want := `  metadata:
    experiment:
      version: 1
      hash: md5-shard
      totalShards: 10000
      unit: {type: request, key: targetingKey}
      holdout: null
      allocations:
        exp-all:
          experimentKey: plain-exp-all
          startAt: null
          endAt: null
          layer: null
          splits:
            - variation: "off"
              shards:
                - {salt: fixed, ranges: [[0, 1000]]}
                - {salt: arm, ranges: [[0, 5000]]}
            - variation: "on"
              shards:
                - {salt: fixed, ranges: [[0, 1000]]}
                - {salt: arm, ranges: [[5000, 10000]]}
`
	if !strings.HasSuffix(string(out), want) {
		t.Errorf("unexpected shape:\n%s", out)
	}
	if !bytes.HasPrefix(out, original[:bytes.Index(original, []byte("plain:"))]) {
		t.Error("the other flag changed")
	}
	back := flagNamed(t, out, "plain")
	if back.Experiment == nil || len(back.Experiment.Allocations["exp-all"].Splits) != 2 {
		t.Errorf("did not read back: %+v", back.Experiment)
	}
}

func TestRemovingTheExperimentDeletesOnlyThatKey(t *testing.T) {
	a := New()
	original := experimentFile(t)
	f := flagNamed(t, original, "request-timeout")
	f.Experiment = nil
	out, err := a.Serialize(original, "request-timeout", f)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "experiment:") || !strings.Contains(string(out), "team: platform") {
		t.Errorf("unexpected result:\n%s", out)
	}
}

func TestEditingAnOutcomeKeepsTheAuthorsQueryText(t *testing.T) {
	a := New()
	original := experimentFile(t)
	f := flagNamed(t, original, "request-timeout")
	f.Rules[1].Outcome = Outcome{Variation: "slow"}
	out, err := a.Serialize(original, "request-timeout", f)
	if err != nil {
		t.Fatal(err)
	}
	added, removed := lineDiff(string(original), string(out))
	if len(added) != 1 || len(removed) != 1 || strings.TrimSpace(added[0]) != "variation: slow" {
		t.Errorf("want one changed line, got +%v -%v", added, removed)
	}
	if !strings.Contains(string(out), `query: region in ["region-a"]`) {
		t.Error("the experiment rule's query was rewritten")
	}
}

func TestReorderingRulesMovesTheirTextVerbatim(t *testing.T) {
	src := []byte(`f:
  variations: {a: 1, b: 2}
  targeting:
    - name: first     # keep me aligned
      query: region in ["x"]
      variation: a
    - name: second
      query: tier eq "gold"
      variation: b
  defaultRule: {variation: a}
`)
	a := New()
	f := flagNamed(t, src, "f")
	f.Rules[0], f.Rules[1] = f.Rules[1], f.Rules[0]
	out, err := a.Serialize(src, "f", f)
	if err != nil {
		t.Fatal(err)
	}
	want := `f:
  variations: {a: 1, b: 2}
  targeting:
    - name: second
      query: tier eq "gold"
      variation: b
    - name: first     # keep me aligned
      query: region in ["x"]
      variation: a
  defaultRule: {variation: a}
`
	if string(out) != want {
		t.Errorf("got:\n%s\nwant:\n%s", out, want)
	}
}

func TestEvaluateSplitsUsesTheShardEvaluator(t *testing.T) {
	a := New()
	content := experimentFile(t)
	at := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)

	if _, handled := a.EvaluateSplits(content, "plain", "u", nil, at); handled {
		t.Error("a flag without an experiment should fall back to the GO Feature Flag engine")
	}

	exposed := ""
	for i := 0; i < 5000 && exposed == ""; i++ {
		key := fmt.Sprintf("req-%d", i)
		if splits.ShardOf("c1e0a7d25f", key, 10000) < 100 {
			exposed = key
		}
	}
	got, handled := a.EvaluateSplits(content, "request-timeout", exposed, map[string]any{"region": "region-a"}, at)
	if !handled || got.Error != "" {
		t.Fatalf("got %+v, handled=%t", got, handled)
	}
	if got.Reason != splits.ReasonSplit || got.ExperimentKey != "request-timeout-exp-region-a" ||
		got.Allocation != "exp-region-a" || got.DoLog == nil || !*got.DoLog || got.Value == nil {
		t.Errorf("unexpected %+v", got)
	}

	before := at.AddDate(0, -1, 0)
	got, _ = a.EvaluateSplits(content, "request-timeout", exposed, map[string]any{"region": "region-a"}, before)
	if got.Reason != splits.ReasonOutsideWindow || got.Variation != "control" || *got.DoLog {
		t.Errorf("before the window: %+v", got)
	}

	got, _ = a.EvaluateSplits(content, "request-timeout", exposed, map[string]any{"region": "region-b", "tier": "internal"}, at)
	if got.Reason != splits.ReasonStock || got.Variation != "fast" || got.Allocation != "legacy-override" {
		t.Errorf("stock rule: %+v", got)
	}
}

func newExperimentFor(t *testing.T, rule string) *splits.Experiment {
	t.Helper()
	exp, err := splits.ParseJSON([]byte(`{"version":1}`))
	if err != nil {
		t.Fatal(err)
	}
	exp.Allocations[rule] = &splits.Allocation{Splits: []splits.Split{{Variation: "on", Shards: []splits.Shard{{Salt: "s", Ranges: []splits.Range{{Start: 0, End: 10000}}}}}}}
	return exp
}

func TestAddingAnExperimentToNullMetadataReplacesItInPlace(t *testing.T) {
	for _, meta := range []string{"  metadata:\n", "  metadata: ~\n", "  metadata: null\n"} {
		src := []byte("f:\n  variations: {on: true, off: false}\n  targeting:\n    - name: r\n      query: a eq \"b\"\n      variation: \"off\"\n  defaultRule: {variation: \"off\"}\n" + meta)
		f := flagNamed(t, src, "f")
		f.Experiment = newExperimentFor(t, "r")
		out, err := New().Serialize(src, "f", f)
		if err != nil {
			t.Fatalf("%q: %v", meta, err)
		}
		if n := strings.Count(string(out), "metadata:"); n != 1 {
			t.Errorf("%q: %d metadata keys in\n%s", meta, n, out)
		}
		if err := New().Validate(out); err != nil {
			t.Errorf("%q: %v", meta, err)
		}
		if back := flagNamed(t, out, "f"); back.Experiment == nil || back.Experiment.Allocations["r"] == nil {
			t.Errorf("%q: experiment did not read back from\n%s", meta, out)
		}
	}
}

const anchoredFlags = `a:
  variations: {on: true, off: false}
  defaultRule: {variation: "off"}
  metadata: &meta
    team: growth
b:
  variations: {on: true, off: false}
  targeting:
    - name: r
      query: a eq "b"
      variation: "off"
  defaultRule: {variation: "off"}
  metadata: *meta
`

func TestEditingAnAnchoredValueIsRefused(t *testing.T) {
	src := []byte(anchoredFlags)

	a := flagNamed(t, src, "a")
	a.Metadata["team"] = "payments"
	if _, err := New().Serialize(src, "a", a); !errors.Is(err, ErrUneditable) || !strings.Contains(err.Error(), "&meta") {
		t.Errorf("changing an anchored metadata: got %v", err)
	}

	a = flagNamed(t, src, "a")
	a.Enabled = false
	if _, err := New().Serialize(src, "a", a); err != nil {
		t.Errorf("an unrelated edit of the anchoring flag should work: %v", err)
	}
}

func TestEditingAnAliasDetachesIt(t *testing.T) {
	src := []byte(anchoredFlags)
	b := flagNamed(t, src, "b")
	b.Experiment = newExperimentFor(t, "r")
	out, err := New().Serialize(src, "b", b)
	if err != nil {
		t.Fatal(err)
	}
	if got := flagNamed(t, out, "a"); got.Experiment != nil || got.Metadata["team"] != "growth" {
		t.Errorf("the anchoring flag changed: %+v", got.Metadata)
	}
	if got := flagNamed(t, out, "b"); got.Experiment == nil || got.Metadata["team"] != "growth" {
		t.Errorf("the edited flag lost its metadata: %+v\n%s", got.Metadata, out)
	}
}

func TestTrailingCommentIsNotDuplicatedOnMetadataWrites(t *testing.T) {
	for name, src := range map[string]string{
		"before another flag": "f:\n  variations: {on: true, off: false}\n  targeting:\n    - name: r\n      query: a eq \"b\"\n      variation: \"off\"\n  defaultRule: {variation: \"off\"}\n  metadata:\n    team: x\n  # end of f\n\ng:\n  variations: {on: true}\n  defaultRule: {variation: \"on\"}\n",
		"at end of file":      "f:\n  variations: {on: true, off: false}\n  targeting:\n    - name: r\n      query: a eq \"b\"\n      variation: \"off\"\n  defaultRule: {variation: \"off\"}\n  metadata:\n    team: x\n# end of f\n",
	} {
		for _, edit := range []func(*Flag){
			func(f *Flag) { f.Experiment = newExperimentFor(t, "r") },
			func(f *Flag) { f.Metadata["owner"] = "y" },
		} {
			f := flagNamed(t, []byte(src), "f")
			edit(&f)
			out, err := New().Serialize([]byte(src), "f", f)
			if err != nil {
				t.Fatal(err)
			}
			if n := strings.Count(string(out), "# end of f"); n != 1 {
				t.Errorf("%s: comment appears %d times:\n%s", name, n, out)
			}
		}
	}
}

func TestMetadataTypeOnlyChangesAreWritten(t *testing.T) {
	src := []byte("f:\n  variations: {on: true, off: false}\n  defaultRule: {variation: \"off\"}\n  metadata:\n    team: x\n    tier: \"1\"\n    beta: \"true\"\n")
	f := flagNamed(t, src, "f")
	f.Metadata["tier"] = 1
	f.Metadata["beta"] = true
	out, err := New().Serialize(src, "f", f)
	if err != nil {
		t.Fatal(err)
	}
	back := flagNamed(t, out, "f")
	if back.Metadata["tier"] != 1 || back.Metadata["beta"] != true {
		t.Errorf("type change dropped: %#v\n%s", back.Metadata, out)
	}
	if !sameValue(&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: "1"}, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: "1"}) {
		t.Error("identical scalars must compare equal")
	}
}
