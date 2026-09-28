package goff

import (
	"errors"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

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

func TestEditingAnOutcomeKeepsTheAuthorsQueryText(t *testing.T) {
	src := []byte(`request-timeout:
  variations: {fast: 100, slow: 500}
  targeting:
    - name: regional
      query: region in ["region-a"]
      variation: fast
  defaultRule: {variation: fast}
`)
	f := flagNamed(t, src, "request-timeout")
	f.Rules[0].Outcome = Outcome{Variation: "slow"}
	out, err := New().Serialize(src, "request-timeout", f)
	if err != nil {
		t.Fatal(err)
	}
	added, removed := lineDiff(string(src), string(out))
	if len(added) != 1 || len(removed) != 1 || strings.TrimSpace(added[0]) != "variation: slow" {
		t.Errorf("want one changed line, got +%v -%v", added, removed)
	}
	if !strings.Contains(string(out), `query: region in ["region-a"]`) {
		t.Errorf("the rule's query was rewritten:\n%s", out)
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
	f := flagNamed(t, src, "f")
	f.Rules[0], f.Rules[1] = f.Rules[1], f.Rules[0]
	out, err := New().Serialize(src, "f", f)
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

func TestEditingOneMetadataKeyLeavesTheOthersVerbatim(t *testing.T) {
	src := []byte(`f:
  variations: {on: true, off: false}
  defaultRule: {variation: "off"}
  metadata:
    team:    growth   # aligned on purpose
    description: "quoted stays quoted"
    owner: someone
`)
	f := flagNamed(t, src, "f")
	f.Metadata["owner"] = "someone-else"
	out, err := New().Serialize(src, "f", f)
	if err != nil {
		t.Fatal(err)
	}
	added, removed := lineDiff(string(src), string(out))
	if len(added) != 1 || len(removed) != 1 || strings.TrimSpace(added[0]) != "owner: someone-else" {
		t.Errorf("want one changed line, got +%v -%v\n%s", added, removed, out)
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

func TestEditingThroughAnAliasDetachesIt(t *testing.T) {
	src := []byte(anchoredFlags)
	b := flagNamed(t, src, "b")
	b.Metadata["team"] = "payments"
	out, err := New().Serialize(src, "b", b)
	if err != nil {
		t.Fatal(err)
	}
	if got := flagNamed(t, out, "a"); got.Metadata["team"] != "growth" {
		t.Errorf("the anchoring flag changed: %+v\n%s", got.Metadata, out)
	}
	if got := flagNamed(t, out, "b"); got.Metadata["team"] != "payments" {
		t.Errorf("the edit was lost: %+v\n%s", got.Metadata, out)
	}
}

func TestTrailingCommentIsNotDuplicatedOnMetadataWrites(t *testing.T) {
	for name, src := range map[string]string{
		"before another flag": "f:\n  variations: {on: true, off: false}\n  targeting:\n    - name: r\n      query: a eq \"b\"\n      variation: \"off\"\n  defaultRule: {variation: \"off\"}\n  metadata:\n    team: x\n  # end of f\n\ng:\n  variations: {on: true}\n  defaultRule: {variation: \"on\"}\n",
		"at end of file":      "f:\n  variations: {on: true, off: false}\n  targeting:\n    - name: r\n      query: a eq \"b\"\n      variation: \"off\"\n  defaultRule: {variation: \"off\"}\n  metadata:\n    team: x\n# end of f\n",
	} {
		f := flagNamed(t, []byte(src), "f")
		f.Metadata["owner"] = "y"
		out, err := New().Serialize([]byte(src), "f", f)
		if err != nil {
			t.Fatal(err)
		}
		if n := strings.Count(string(out), "# end of f"); n != 1 {
			t.Errorf("%s: comment appears %d times:\n%s", name, n, out)
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
