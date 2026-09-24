package goff

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func golden(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/payments.goff.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestParseReadsEveryFlag(t *testing.T) {
	a := New()
	flags, broken, err := a.Parse("payments.goff.yaml", golden(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(broken) != 0 {
		t.Fatalf("unexpected broken flags: %+v", broken)
	}
	if len(flags) != 3 {
		t.Fatalf("want 3 flags, got %d", len(flags))
	}

	byKey := map[string]Flag{}
	for _, f := range flags {
		byKey[f.Key] = f
	}

	checkout := byKey["new-checkout"]
	if checkout.Type != TypeBool {
		t.Errorf("type = %q", checkout.Type)
	}
	if !checkout.Enabled {
		t.Error("should be enabled when disable is absent")
	}
	if len(checkout.Rules) != 1 {
		t.Fatalf("rules = %+v", checkout.Rules)
	}
	if checkout.Rules[0].Advanced {
		t.Error("a simple OR query should parse into the builder, not fall back to advanced")
	}
	if checkout.Rules[0].Outcome.Percentage["on"] != 20 {
		t.Errorf("percentage = %+v", checkout.Rules[0].Outcome.Percentage)
	}
	if checkout.Default.Variation != "off" {
		t.Errorf("default = %+v", checkout.Default)
	}
	if checkout.Metadata["jira"] != "PAY-100" {
		t.Errorf("metadata = %+v", checkout.Metadata)
	}

	if byKey["model-choice"].Type != TypeString {
		t.Errorf("model-choice type = %q", byKey["model-choice"].Type)
	}
}

func TestProgressiveRolloutIsReadableAndStillFlagged(t *testing.T) {
	a := New()
	flags, _, _ := a.Parse("f.yaml", golden(t))

	for _, f := range flags {
		if f.Key != "legacy-ramp" {
			continue
		}
		if len(f.Rules) != 1 {
			t.Fatalf("rules = %+v", f.Rules)
		}
		if f.Rules[0].Progressive == nil {
			t.Error("a progressive rollout must be readable, not hidden behind advanced")
		}
		joined := strings.Join(f.Preserved, ",")
		for _, want := range []string{"scheduledRollout", "progressiveRollout"} {
			if !strings.Contains(joined, want) {
				t.Errorf("preserved should list %s, got %v", want, f.Preserved)
			}
		}
		return
	}
	t.Fatal("legacy-ramp not found")
}

func TestRoundTripIsByteIdenticalWhenNothingChanges(t *testing.T) {
	a := New()
	original := golden(t)

	flags, _, err := a.Parse("f.yaml", original)
	if err != nil {
		t.Fatal(err)
	}

	out := original
	for _, f := range flags {
		out, err = a.Serialize(out, f.Key, f)
		if err != nil {
			t.Fatalf("serialize %s: %v", f.Key, err)
		}
	}

	if string(out) != string(original) {
		t.Errorf("parse then serialize changed the file.\n--- want ---\n%s\n--- got ---\n%s", original, out)
	}
}

func TestSingleEditProducesMinimalDiff(t *testing.T) {
	a := New()
	original := golden(t)

	flags, _, _ := a.Parse("f.yaml", original)
	var target Flag
	for _, f := range flags {
		if f.Key == "new-checkout" {
			target = f
		}
	}

	target.Enabled = false
	out, err := a.Serialize(original, "new-checkout", target)
	if err != nil {
		t.Fatal(err)
	}

	added, removed := lineDiff(string(original), string(out))
	if len(added) > 1 || len(removed) > 0 {
		t.Errorf("disabling one flag should add exactly one line.\nadded: %v\nremoved: %v\n%s", added, removed, out)
	}
	if len(added) == 1 && strings.TrimSpace(added[0]) != "disable: true" {
		t.Errorf("unexpected added line: %q", added[0])
	}

	body := string(out)
	if !strings.Contains(body, "disable: true") {
		t.Error("the edit did not apply")
	}
	for _, mustKeep := range []string{
		"# Payment flags, owned by @acme/payments",
		"# This one ramps over time; Studio must not touch it.",
		"progressiveRollout:",
		"scheduledRollout:",
		"jira: PAY-100",
	} {
		if !strings.Contains(body, mustKeep) {
			t.Errorf("lost %q", mustKeep)
		}
	}
}

func TestUnknownFieldsSurviveAnEdit(t *testing.T) {
	a := New()
	original := []byte(`future-flag:
  variations:
    on: true
    off: false
  defaultRule:
    variation: "off"
  someFutureGoffField:
    nested: value
    list:
      - 1
      - 2
`)

	flags, broken, err := a.Parse("f.yaml", original)
	if err != nil {
		t.Fatal(err)
	}
	if len(broken) != 0 {
		t.Fatalf("an unknown field should not break parsing: %+v", broken)
	}

	f := flags[0]
	f.Enabled = false
	out, err := a.Serialize(original, f.Key, f)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(out), "someFutureGoffField") || !strings.Contains(string(out), "nested: value") {
		t.Errorf("unknown field was dropped:\n%s", out)
	}
}

func TestValidateUsesGoffsOwnRules(t *testing.T) {
	a := New()

	if err := a.Validate(golden(t)); err != nil {
		t.Errorf("golden file should validate: %v", err)
	}

	bad := []byte(`broken:
  variations:
    on: true
  defaultRule:
    variation: does-not-exist
`)
	err := a.Validate(bad)
	if err == nil {
		t.Fatal("a default pointing at a missing variation must fail")
	}
	if !strings.Contains(err.Error(), "does-not-exist") {
		t.Errorf("error should name the bad variation, got: %v", err)
	}

	unknownVariation := []byte(`broken:
  variations:
    on: true
    off: false
  targeting:
    - name: r
      query: (a eq "b")
      percentage:
        ghost: 100
  defaultRule:
    variation: "off"
`)
	if err := a.Validate(unknownVariation); err == nil {
		t.Error("a percentage naming a nonexistent variation must fail")
	}

	allZero := []byte(`broken:
  variations:
    on: true
    off: false
  targeting:
    - name: r
      query: (a eq "b")
      percentage:
        on: 0
        off: 0
  defaultRule:
    variation: "off"
`)
	if err := a.Validate(allZero); err == nil {
		t.Error("percentages summing to zero must fail")
	}
}

func TestPercentagesNeedNotSumTo100(t *testing.T) {
	a := New()
	partial := []byte(`partial:
  variations:
    on: true
    off: false
  targeting:
    - name: r
      query: (a eq "b")
      percentage:
        on: 10
        off: 10
  defaultRule:
    variation: "off"
`)
	if err := a.Validate(partial); err != nil {
		t.Errorf("GOFF treats percentages as relative weights and only rejects empty or all-zero maps: %v", err)
	}
}

func TestSerializeRefusesInvalidOutputBeforeItReachesGit(t *testing.T) {
	a := New()
	original := golden(t)

	flags, _, _ := a.Parse("f.yaml", original)
	var target Flag
	for _, f := range flags {
		if f.Key == "new-checkout" {
			target = f
		}
	}

	target.Default = Outcome{Variation: "ghost"}
	out, err := a.Serialize(original, target.Key, target)
	if err != nil {
		t.Fatal(err)
	}

	if err := a.Validate(out); err == nil {
		t.Error("Validate must reject a default pointing at a nonexistent variation")
	}
}

func TestEvaluateMatchesProductionBehaviour(t *testing.T) {
	a := New()
	content := golden(t)

	hits := 0
	for i := 0; i < 1000; i++ {
		res := a.Evaluate(content, "new-checkout", itoa(i), map[string]any{"tier": "gold"})
		if res.Error != "" {
			t.Fatalf("eval error: %s", res.Error)
		}
		if res.Value == true {
			hits++
		}
	}
	if hits < 150 || hits > 250 {
		t.Errorf("20%% split produced %d/1000, expected roughly 200", hits)
	}

	miss := a.Evaluate(content, "new-checkout", "u1", map[string]any{"tier": "bronze"})
	if miss.Value != false {
		t.Errorf("non-matching context should fall through to defaultRule, got %v", miss.Value)
	}
	if miss.Reason != "DEFAULT" {
		t.Errorf("reason = %q", miss.Reason)
	}

	other := a.Evaluate(content, "new-checkout", "u1", map[string]any{"account_id": "42"})
	if other.Reason != "TARGETING_MATCH_SPLIT" {
		t.Errorf("the second OR branch should match, got reason %q", other.Reason)
	}
}

func TestEvaluateReportsInvalidDraft(t *testing.T) {
	a := New()
	res := a.Evaluate([]byte("bad:\n  variations:\n    on: true\n  defaultRule:\n    variation: ghost\n"), "bad", "u1", nil)
	if res.Error == "" {
		t.Error("previewing an invalid draft should report the error, not evaluate it")
	}
}

func TestCoerceValue(t *testing.T) {
	cases := []struct {
		raw  string
		t    ValueType
		want any
		bad  bool
	}{
		{"true", TypeBool, true, false},
		{"false", TypeBool, false, false},
		{"yes", TypeBool, nil, true},
		{"1.5", TypeNumber, 1.5, false},
		{"abc", TypeNumber, nil, true},
		{`{"a":1}`, TypeJSON, nil, false},
		{"{nope}", TypeJSON, nil, true},
		{"hello", TypeString, "hello", false},
	}

	for _, c := range cases {
		got, err := CoerceValue(c.raw, c.t)
		if c.bad {
			if err == nil {
				t.Errorf("CoerceValue(%q, %s) should fail", c.raw, c.t)
			}
			continue
		}
		if err != nil {
			t.Errorf("CoerceValue(%q, %s): %v", c.raw, c.t, err)
			continue
		}
		if c.want != nil && got != c.want {
			t.Errorf("CoerceValue(%q, %s) = %v, want %v", c.raw, c.t, got, c.want)
		}
	}
}

func lineDiff(before, after string) (added, removed []string) {
	count := map[string]int{}
	for _, l := range strings.Split(before, "\n") {
		count[l]++
	}
	for _, l := range strings.Split(after, "\n") {
		if count[l] > 0 {
			count[l]--
			continue
		}
		added = append(added, l)
	}
	for l, n := range count {
		for i := 0; i < n; i++ {
			removed = append(removed, l)
		}
	}
	return added, removed
}

func itoa(n int) string {
	return "e" + string(rune('0'+n%10)) + string(rune('a'+(n/10)%26)) + string(rune('A'+(n/260)%26))
}

func TestListFieldsAreNeverNullInJSON(t *testing.T) {
	a := New()

	noRules := []byte(`bare-flag:
  variations:
    on: true
    off: false
  defaultRule:
    variation: "on"
`)

	flags, _, err := a.Parse("f.yaml", noRules)
	if err != nil {
		t.Fatal(err)
	}

	encoded, err := json.Marshal(flags[0])
	if err != nil {
		t.Fatal(err)
	}

	body := string(encoded)
	if strings.Contains(body, `"rules":null`) {
		t.Errorf("a flag with no rules must serialise rules as [], not null, or the UI crashes on .map():\n%s", body)
	}
	if strings.Contains(body, `"variations":null`) {
		t.Errorf("variations must never be null:\n%s", body)
	}

	if flags[0].Rules == nil {
		t.Error("Rules should be an empty slice, not nil")
	}
}

func TestCommentOnlyFileBehavesAsEmpty(t *testing.T) {
	a := New()
	seed := []byte("# Feature flags for staging.\n# Managed by GO Feature Flag Studio.\n")

	flags, broken, err := a.Parse("staging/payments.goff.yaml", seed)
	if err != nil {
		t.Fatalf("a brand new environment file must parse: %v", err)
	}
	if len(flags) != 0 || len(broken) != 0 {
		t.Errorf("expected no flags, got %+v %+v", flags, broken)
	}

	created := Flag{
		Key:        "first-flag",
		Variations: []Variation{{Name: "on", Value: true}, {Name: "off", Value: false}},
		Default:    Outcome{Variation: "off"},
		Enabled:    true,
	}

	out, err := a.Serialize(seed, "first-flag", created)
	if err != nil {
		t.Fatalf("serialising into a comment-only file failed: %v", err)
	}
	if !strings.Contains(string(out), "first-flag:") {
		t.Errorf("flag not written:\n%s", out)
	}
	if !strings.Contains(string(out), "# Feature flags for staging.") {
		t.Errorf("seed comments lost:\n%s", out)
	}
	if err := a.Validate(out); err != nil {
		t.Errorf("result is not loadable by GO Feature Flag: %v", err)
	}
}

func TestTrulyEmptyFileBehavesAsEmpty(t *testing.T) {
	a := New()
	for _, seed := range [][]byte{nil, []byte(""), []byte("\n"), []byte("   \n")} {
		if _, _, err := a.Parse("f.yaml", seed); err != nil {
			t.Errorf("Parse(%q) = %v", seed, err)
		}
	}
}

func TestTeamComesOnlyFromMetadata(t *testing.T) {
	a := New()

	content := []byte(`declared-owner:
  variations:
    on: true
  defaultRule:
    variation: "on"
  metadata:
    team: growth
undeclared-owner:
  variations:
    on: true
  defaultRule:
    variation: "on"
blank-team:
  variations:
    on: true
  defaultRule:
    variation: "on"
  metadata:
    team: "   "
`)

	flags, _, err := a.Parse("production/checkout.goff.yaml", content)
	if err != nil {
		t.Fatal(err)
	}

	byKey := map[string]Flag{}
	for _, f := range flags {
		byKey[f.Key] = f
	}

	if got := byKey["declared-owner"].Team; got != "growth" {
		t.Errorf("a declared team must be reported, got %q", got)
	}
	if got := byKey["undeclared-owner"].Team; got != "" {
		t.Errorf("the filename must not imply a team, got %q", got)
	}
	if got := byKey["blank-team"].Team; got != "" {
		t.Errorf("a blank team is no team, got %q", got)
	}
}

func TestTeamSurvivesAnEdit(t *testing.T) {
	a := New()
	original := []byte(`owned:
  variations:
    on: true
    off: false
  defaultRule:
    variation: "off"
  metadata:
    team: payments
    jira: PAY-1
`)

	flags, _, _ := a.Parse("production/checkout.goff.yaml", original)
	f := flags[0]
	if f.Team != "payments" {
		t.Fatalf("team = %q", f.Team)
	}

	f.Enabled = false
	out, err := a.Serialize(original, f.Key, f)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(out), "team: payments") {
		t.Errorf("the team label must survive an unrelated edit:\n%s", out)
	}
	if !strings.Contains(string(out), "jira: PAY-1") {
		t.Error("other metadata must survive too")
	}
}

func TestTeamIgnoresNonStringMetadata(t *testing.T) {
	cases := []map[string]any{
		nil,
		{},
		{"team": 42},
		{"team": nil},
		{"team": []string{"growth"}},
	}
	for _, metadata := range cases {
		if got := TeamOf(metadata); got != "" {
			t.Errorf("TeamOf(%v) = %q, want empty", metadata, got)
		}
	}
}

func TestNegatedQueryValidatesAndEvaluatesInGOFF(t *testing.T) {
	a := New()
	file := []byte(`nope:
  variations:
    on: true
    off: false
  targeting:
    - name: not-gold
      query: (not (tier eq "gold"))
      variation: "on"
  defaultRule:
    variation: "off"
`)

	if err := a.Validate(file); err != nil {
		t.Fatalf("GOFF rejected a negated query: %v", err)
	}

	if got := a.Evaluate(file, "nope", "u1", map[string]any{"tier": "gold"}); got.Variation != "off" {
		t.Errorf("gold must not match a negated rule, got %+v", got)
	}
	if got := a.Evaluate(file, "nope", "u1", map[string]any{"tier": "bronze"}); got.Variation != "on" {
		t.Errorf("bronze must match a negated rule, got %+v", got)
	}
}

func TestEditingARuleKeepsFieldsStudioCannotModel(t *testing.T) {
	a := New()
	src := []byte(`ramped:
  variations:
    on: true
    off: false
  targeting:
    - name: ramp
      query: (tier eq "gold")
      progressiveRollout:
        initial:
          variation: "off"
          percentage: 0
          date: 2026-01-01T00:00:00Z
        end:
          variation: "on"
          percentage: 100
          date: 2026-02-01T00:00:00Z
  defaultRule:
    variation: "off"
`)

	flags, broken, err := a.Parse("f.yaml", src)
	if err != nil {
		t.Fatal(err)
	}
	if len(broken) > 0 {
		t.Fatalf("unexpected broken flags: %v", broken)
	}

	f := flags[0]
	if len(f.Preserved) == 0 || f.Preserved[0] != "progressiveRollout" {
		t.Errorf("preserved = %v, want progressiveRollout", f.Preserved)
	}

	f.Rules[0].Condition = Leaf("tier", "eq", "platinum")
	f.Rules[0].Advanced = false

	out, err := a.Serialize(src, "ramped", f)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(out), `query: (tier eq "platinum")`) {
		t.Errorf("the edit did not land:\n%s", out)
	}
	if !strings.Contains(string(out), "progressiveRollout:") {
		t.Errorf("progressiveRollout was dropped by an unrelated edit:\n%s", out)
	}
	if !strings.Contains(string(out), "percentage: 100") {
		t.Errorf("progressive rollout detail was lost:\n%s", out)
	}
	if err := a.Validate(out); err != nil {
		t.Errorf("result does not validate: %v", err)
	}
}
