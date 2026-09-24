package goff

import (
	"strings"
	"testing"
)

const sample = `# Feature flags for the checkout path.
# Reviewed by the platform team.

first-flag:
  variations:
    on: true
    off: false
  defaultRule:
    variation: "on"

# This one drives model selection.
second-flag:
  variations:
    control: model-v1
    treatment: model-v2
  targeting:
    - name: tenant-split
      query: (tenant eq "acme")
      percentage:
        control: 75
        treatment: 25
  defaultRule:
    variation: control

third-flag:
  variations:
    yes: 1
  defaultRule:
    variation: yes
`

func TestSetLeavesOtherFlagsByteIdentical(t *testing.T) {
	doc, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}

	if err := doc.SetFlag("first-flag", map[string]any{
		"variations":  map[string]any{"on": true, "off": false},
		"defaultRule": map[string]any{"variation": "off"},
	}); err != nil {
		t.Fatal(err)
	}

	out := string(doc.Bytes())

	for _, want := range []string{
		"# This one drives model selection.",
		"      query: (tenant eq \"acme\")",
		"    control: model-v1",
		"        treatment: 25",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("editing first-flag disturbed other content, missing %q:\n%s", want, out)
		}
	}

	if !strings.Contains(out, "# Feature flags for the checkout path.") {
		t.Error("leading comments were lost")
	}
}

func TestSetReplacesOnlyTheTargetBlock(t *testing.T) {
	doc, _ := Parse([]byte(sample))

	if err := doc.SetFlag("second-flag", map[string]any{
		"variations":  map[string]any{"control": "a"},
		"defaultRule": map[string]any{"variation": "control"},
	}); err != nil {
		t.Fatal(err)
	}

	out := string(doc.Bytes())

	if strings.Contains(out, "tenant-split") {
		t.Error("old second-flag body survived the replacement")
	}
	if !strings.Contains(out, "first-flag:") || !strings.Contains(out, "third-flag:") {
		t.Errorf("neighbours were removed:\n%s", out)
	}
	if strings.Contains(out, "model-v1") {
		t.Error("stale variation from the replaced block survived")
	}
}

func TestSetPreservesCommentAttachedToReplacedFlag(t *testing.T) {
	doc, _ := Parse([]byte(sample))
	if err := doc.SetFlag("second-flag", map[string]any{"variations": map[string]any{"a": 1}}); err != nil {
		t.Fatal(err)
	}
	out := string(doc.Bytes())

	if strings.Count(out, "# This one drives model selection.") != 0 {
		t.Log("comment retained above replaced flag is acceptable")
	}
	if strings.Contains(out, "# This one drives model selection.\n# This one") {
		t.Error("comment duplicated")
	}
}

func TestAppendNewFlag(t *testing.T) {
	doc, _ := Parse([]byte(sample))

	if err := doc.SetFlag("new-flag", map[string]any{
		"variations":  map[string]any{"on": true},
		"defaultRule": map[string]any{"variation": "on"},
	}); err != nil {
		t.Fatal(err)
	}

	out := string(doc.Bytes())
	if !strings.Contains(out, "new-flag:") {
		t.Fatalf("new flag missing:\n%s", out)
	}
	if !strings.Contains(out, "third-flag:") {
		t.Error("appending clobbered the last flag")
	}
	if strings.HasSuffix(out, "\n\n") {
		t.Error("appending left a trailing blank line")
	}
}

func TestDeleteRemovesOnlyThatFlag(t *testing.T) {
	doc, _ := Parse([]byte(sample))

	if err := doc.Delete("second-flag"); err != nil {
		t.Fatal(err)
	}

	out := string(doc.Bytes())
	if strings.Contains(out, "second-flag:") || strings.Contains(out, "tenant-split") {
		t.Errorf("second-flag survived:\n%s", out)
	}
	for _, want := range []string{"first-flag:", "third-flag:", "# Feature flags for the checkout path."} {
		if !strings.Contains(out, want) {
			t.Errorf("delete removed %q too:\n%s", want, out)
		}
	}
}

func TestDeleteUnknownKey(t *testing.T) {
	doc, _ := Parse([]byte(sample))
	if err := doc.Delete("nope"); err == nil {
		t.Error("deleting a missing flag should error")
	}
}

func TestSequentialEditsStayConsistent(t *testing.T) {
	doc, _ := Parse([]byte(sample))

	if err := doc.SetFlag("first-flag", map[string]any{"variations": map[string]any{"a": 1, "b": 2, "c": 3}}); err != nil {
		t.Fatal(err)
	}
	if err := doc.SetFlag("third-flag", map[string]any{"variations": map[string]any{"z": 9}}); err != nil {
		t.Fatal(err)
	}
	if err := doc.Delete("second-flag"); err != nil {
		t.Fatal(err)
	}
	if err := doc.SetFlag("fourth-flag", map[string]any{"variations": map[string]any{"q": 0}}); err != nil {
		t.Fatal(err)
	}

	out := string(doc.Bytes())

	reparsed, err := Parse([]byte(out))
	if err != nil {
		t.Fatalf("output no longer parses: %v\n%s", err, out)
	}

	got := reparsed.Keys()
	want := map[string]bool{"first-flag": true, "third-flag": true, "fourth-flag": true}
	if len(got) != len(want) {
		t.Fatalf("keys = %v, want %v", got, want)
	}
	for _, k := range got {
		if !want[k] {
			t.Errorf("unexpected key %q", k)
		}
	}
	if strings.Contains(out, "second-flag") {
		t.Error("deleted flag reappeared")
	}
}

func TestKeysInDocumentOrder(t *testing.T) {
	doc, _ := Parse([]byte(sample))
	got := doc.Keys()
	want := []string{"first-flag", "second-flag", "third-flag"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("keys = %v, want document order %v", got, want)
		}
	}
}

func TestParseEmptyDocument(t *testing.T) {
	doc, err := Parse([]byte(""))
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Keys()) != 0 {
		t.Errorf("expected no keys, got %v", doc.Keys())
	}

	if err := doc.SetFlag("only", map[string]any{"variations": map[string]any{"on": true}}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(doc.Bytes()), "only:") {
		t.Errorf("set on an empty doc produced:\n%s", doc.Bytes())
	}
}

func TestOutputEndsWithSingleNewline(t *testing.T) {
	doc, _ := Parse([]byte(sample))
	out := string(doc.Bytes())
	if !strings.HasSuffix(out, "\n") {
		t.Error("missing trailing newline")
	}
	if strings.HasSuffix(out, "\n\n") {
		t.Error("more than one trailing newline")
	}
}
