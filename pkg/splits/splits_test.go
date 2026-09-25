package splits

import (
	"strings"
	"testing"
	"time"
)

func TestShardOfMatchesTheLegacyValues(t *testing.T) {
	// Values produced by the legacy SDK's shard function for the same inputs.
	for _, tc := range []struct {
		salt, subject string
		total, want   int
	}{
		{"salt", "subject", 10000, 2878},
		{"salt", "subject", 100, 78},
		{"abc", "user-1", 10000, 2481},
		{"", "", 10000, 5692},
		{"exposure", "s-42", 10000, 160},
	} {
		if got := ShardOf(tc.salt, tc.subject, tc.total); got != tc.want {
			t.Errorf("ShardOf(%q, %q, %d) = %d, want %d", tc.salt, tc.subject, tc.total, got, tc.want)
		}
	}
}

func TestShardOfHandlesLongInputs(t *testing.T) {
	long := strings.Repeat("x", 400)
	a, b := ShardOf(long, "s", 10000), ShardOf(long, "s", 10000)
	if a != b || a < 0 || a >= 10000 {
		t.Fatalf("unstable or out of range: %d %d", a, b)
	}
}

const sampleBlock = `
version: 1
hash: md5-shard
totalShards: 10000
unit: {type: request, key: targetingKey}
holdout: null
allocations:
  exp-a:
    experimentKey: widget-exp-a
    doLog: true
    startAt: 2026-10-01T00:00:00Z
    endAt: null
    passThrough: true
    layer: null
    splits:
      - variation: control
        extraLogging: {arm: c}
        shards:
          - {salt: "e1", ranges: [[0, 100]]}
          - {salt: "a1", ranges: [[0, 5000]]}
      - variation: treatment
        shards:
          - {salt: "e1", ranges: [[0, 100]]}
          - {salt: "a1", ranges: [[5000, 10000]]}
`

func TestParseYAMLAndMetadataAgree(t *testing.T) {
	fromYAML, err := ParseYAML([]byte(sampleBlock))
	if err != nil {
		t.Fatal(err)
	}

	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	fromMeta, err := FromMetadata(map[string]any{
		"team": "growth",
		"experiment": map[string]any{
			"version": 1, "hash": "md5-shard", "totalShards": 10000,
			"unit": map[string]any{"type": "request", "key": "targetingKey"},
			"allocations": map[string]any{
				"exp-a": map[string]any{
					"experimentKey": "widget-exp-a", "doLog": true, "startAt": start,
					"endAt": nil, "passThrough": true,
					"splits": []any{
						map[string]any{
							"variation": "control", "extraLogging": map[string]any{"arm": "c"},
							"shards": []any{
								map[any]any{"salt": "e1", "ranges": []any{[]any{0, 100}}},
								map[string]any{"salt": "a1", "ranges": []any{[]any{0, 5000}}},
							},
						},
						map[string]any{
							"variation": "treatment",
							"shards": []any{
								map[string]any{"salt": "e1", "ranges": []any{[]any{0, 100}}},
								map[string]any{"salt": "a1", "ranges": []any{[]any{5000.0, 10000.0}}},
							},
						},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	for label, e := range map[string]*Experiment{"yaml": fromYAML, "metadata": fromMeta} {
		a := e.Allocations["exp-a"]
		if a == nil {
			t.Fatalf("%s: allocation missing", label)
		}
		if a.StartAt == nil || !a.StartAt.Equal(start) {
			t.Errorf("%s: startAt = %v", label, a.StartAt)
		}
		if a.EndAt != nil {
			t.Errorf("%s: endAt should be nil", label)
		}
		if got := a.Splits[1].Shards[1].Ranges[0]; got != (Range{5000, 10000}) {
			t.Errorf("%s: range = %v", label, got)
		}
		if a.Splits[0].ExtraLogging["arm"] != "c" {
			t.Errorf("%s: extraLogging lost", label)
		}
	}
}

func TestFromMetadataWithoutExperiment(t *testing.T) {
	e, err := FromMetadata(map[string]any{"team": "x"})
	if err != nil || e != nil {
		t.Fatalf("got %v, %v; want nil, nil", e, err)
	}
}

func TestParseDefaults(t *testing.T) {
	e, err := ParseYAML([]byte("version: 1\nallocations: {}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if e.Hash != HashMD5Shard || e.TotalShards != 10000 || e.Unit.Type != UnitRequest || e.Unit.Key != TargetingKey {
		t.Errorf("defaults not applied: %+v", e)
	}
}

func TestParseRejectsMalformedRanges(t *testing.T) {
	for _, src := range []string{
		"version: 1\nallocations: {a: {splits: [{variation: x, shards: [{salt: s, ranges: [[1]]}]}]}}",
		"version: 1\nallocations: {a: {splits: [{variation: x, shards: [{salt: s, ranges: [[1.5, 2]]}]}]}}",
		"- not a mapping",
	} {
		if _, err := ParseYAML([]byte(src)); err == nil {
			t.Errorf("expected an error for %q", src)
		}
	}
}

func TestToMetadataRoundTrips(t *testing.T) {
	e, err := ParseYAML([]byte(sampleBlock))
	if err != nil {
		t.Fatal(err)
	}
	back, err := FromMetadata(map[string]any{"experiment": e.ToMetadata()})
	if err != nil {
		t.Fatal(err)
	}
	if back.Allocations["exp-a"].Splits[1].Shards[1].Ranges[0] != (Range{5000, 10000}) {
		t.Errorf("round trip lost ranges: %+v", back.Allocations["exp-a"])
	}
	if !back.Allocations["exp-a"].StartAt.Equal(*e.Allocations["exp-a"].StartAt) {
		t.Error("round trip lost startAt")
	}
}

func widgetFlag() Flag {
	return Flag{
		Key:        "widget",
		Variations: []string{"control", "treatment", "other"},
		Rules: []Rule{
			{Name: "exp-a", Query: `region in ["us"]`, Variation: "control"},
			{Name: "stock", Query: `region eq "eu"`, Variation: "other"},
		},
		Default: Rule{Variation: "control"},
	}
}

func TestValidate(t *testing.T) {
	base := func() *Experiment {
		e, err := ParseYAML([]byte(sampleBlock))
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	f := widgetFlag()

	for _, tc := range []struct {
		name     string
		mutate   func(*Experiment)
		severity Severity
		want     string
	}{
		{"valid", func(*Experiment) {}, "", ""},
		{"version", func(e *Experiment) { e.Version = 2 }, SeverityError, "only version 1"},
		{"hash", func(e *Experiment) { e.Hash = "sha1" }, SeverityError, "md5-shard"},
		{"unit", func(e *Experiment) { e.Unit.Type = "session" }, SeverityError, "unit.type"},
		{"range too high", func(e *Experiment) {
			e.Allocations["exp-a"].Splits[0].Shards[0].Ranges[0] = Range{9000, 10001}
		}, SeverityError, "must lie within"},
		{"negative start", func(e *Experiment) {
			e.Allocations["exp-a"].Splits[0].Shards[0].Ranges[0] = Range{-1, 10}
		}, SeverityError, "must lie within"},
		{"empty range", func(e *Experiment) {
			e.Allocations["exp-a"].Splits[0].Shards[0].Ranges[0] = Range{10, 10}
		}, SeverityError, "less than end"},
		{"unknown variation", func(e *Experiment) {
			e.Allocations["exp-a"].Splits[0].Variation = "nope"
		}, SeverityError, `"nope" is not a variation`},
		{"unknown rule", func(e *Experiment) {
			e.Allocations["ghost"] = e.Allocations["exp-a"]
		}, SeverityError, `no targeting rule named "ghost"`},
		{"missing salt", func(e *Experiment) {
			e.Allocations["exp-a"].Splits[0].Shards[0].Salt = ""
		}, SeverityError, "salt"},
		{"window order", func(e *Experiment) {
			end := e.Allocations["exp-a"].StartAt.Add(-time.Hour)
			e.Allocations["exp-a"].EndAt = &end
		}, SeverityError, "must be after startAt"},
		{"no splits", func(e *Experiment) {
			e.Allocations["exp-a"].Splits = nil
		}, SeverityError, "at least one split"},
		{"overlapping arms", func(e *Experiment) {
			e.Allocations["exp-a"].Splits[1].Shards[1].Ranges[0] = Range{4000, 10000}
		}, SeverityWarning, "can both match"},
		{"holdout range", func(e *Experiment) {
			e.Holdout = &Shard{Salt: "h", Ranges: []Range{{0, 20000}}}
		}, SeverityError, "holdout"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := base()
			tc.mutate(e)
			problems := Validate(e, &f)
			if tc.want == "" {
				if len(problems) != 0 {
					t.Fatalf("expected no problems, got %v", problems)
				}
				return
			}
			found := false
			for _, p := range problems {
				if p.Severity == tc.severity && strings.Contains(p.String(), tc.want) {
					found = true
				}
			}
			if !found {
				t.Fatalf("expected a %s containing %q, got %v", tc.severity, tc.want, problems)
			}
			if tc.severity == SeverityWarning && problems.Err() != nil {
				t.Fatalf("a warning must not fail validation: %v", problems.Err())
			}
		})
	}
}

// excluding returns a range that does not contain subject's shard for salt.
func excluding(salt, subject string) Range {
	v := ShardOf(salt, subject, 10000)
	if v == 0 {
		return Range{1, 10000}
	}
	return Range{0, v}
}

func everyone(salt string) Shard { return Shard{Salt: salt, Ranges: []Range{{0, 10000}}} }

func nobody(salt, subject string) Shard {
	return Shard{Salt: salt, Ranges: []Range{excluding(salt, subject)}}
}

func experimentWith(a *Allocation) *Experiment {
	e := &Experiment{Version: 1, Allocations: map[string]*Allocation{"exp-a": a}}
	e.applyDefaults()
	return e
}

func ptr[T any](v T) *T { return &v }

func TestEvaluate(t *testing.T) {
	now := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	us := map[string]any{"region": "us"}
	subject := "user-1"

	treatment := func(shards ...Shard) []Split {
		return []Split{{Variation: "treatment", Shards: shards, ExtraLogging: map[string]string{"arm": "t"}}}
	}

	for _, tc := range []struct {
		name      string
		flag      func(*Flag)
		exp       *Experiment
		attrs     map[string]any
		subject   string
		want      Assignment
		wantExtra bool
	}{
		{
			name:  "split",
			exp:   experimentWith(&Allocation{Splits: treatment(everyone("x"))}),
			attrs: us,
			want: Assignment{Variation: "treatment", ExperimentKey: "widget-exp-a", Allocation: "exp-a",
				DoLog: true, Reason: ReasonSplit},
			wantExtra: true,
		},
		{
			name:  "custom experiment key and no logging",
			exp:   experimentWith(&Allocation{ExperimentKey: "k1", DoLog: ptr(false), Splits: treatment(everyone("x"))}),
			attrs: us,
			want: Assignment{Variation: "treatment", ExperimentKey: "k1", Allocation: "exp-a",
				Reason: ReasonSplit},
			wantExtra: true,
		},
		{
			name:  "query does not match falls to the stock rule",
			exp:   experimentWith(&Allocation{Splits: treatment(everyone("x"))}),
			attrs: map[string]any{"region": "eu"},
			want:  Assignment{Variation: "other", Allocation: "stock", Reason: ReasonStock},
		},
		{
			name:  "not exposed passes through to the default",
			exp:   experimentWith(&Allocation{Splits: treatment(nobody("x", subject))}),
			attrs: us,
			want:  Assignment{Variation: "control", Allocation: DefaultAllocation, Reason: ReasonPassThrough},
		},
		{
			name:  "not exposed without pass-through serves the stock variation unlogged",
			exp:   experimentWith(&Allocation{PassThrough: ptr(false), Splits: treatment(nobody("x", subject))}),
			attrs: us,
			want:  Assignment{Variation: "control", Allocation: "exp-a", Reason: ReasonStock},
		},
		{
			name:  "before the window passes through",
			exp:   experimentWith(&Allocation{StartAt: ptr(now.Add(time.Hour)), Splits: treatment(everyone("x"))}),
			attrs: us,
			want:  Assignment{Variation: "control", Allocation: DefaultAllocation, Reason: ReasonOutsideWindow},
		},
		{
			name: "after the window without pass-through serves stock",
			exp: experimentWith(&Allocation{PassThrough: ptr(false), EndAt: ptr(now.Add(-time.Hour)),
				Splits: treatment(everyone("x"))}),
			attrs: us,
			want:  Assignment{Variation: "control", Allocation: "exp-a", Reason: ReasonOutsideWindow},
		},
		{
			name: "inside the window",
			exp: experimentWith(&Allocation{StartAt: ptr(now.Add(-time.Hour)), EndAt: ptr(now.Add(time.Hour)),
				Splits: treatment(everyone("x"))}),
			attrs: us,
			want: Assignment{Variation: "treatment", ExperimentKey: "widget-exp-a", Allocation: "exp-a",
				DoLog: true, Reason: ReasonSplit},
			wantExtra: true,
		},
		{
			name: "holdout gets the default and is logged",
			exp: func() *Experiment {
				e := experimentWith(&Allocation{Splits: treatment(everyone("x"))})
				e.Holdout = &Shard{Salt: "h", Ranges: []Range{{0, 10000}}}
				return e
			}(),
			attrs: us,
			want: Assignment{Variation: "control", ExperimentKey: "widget-exp-a", Allocation: "exp-a",
				DoLog: true, Reason: ReasonHoldout},
		},
		{
			name: "outside the holdout is assigned normally",
			exp: func() *Experiment {
				e := experimentWith(&Allocation{Splits: treatment(everyone("x"))})
				h := nobody("h", subject)
				e.Holdout = &h
				return e
			}(),
			attrs: us,
			want: Assignment{Variation: "treatment", ExperimentKey: "widget-exp-a", Allocation: "exp-a",
				DoLog: true, Reason: ReasonSplit},
			wantExtra: true,
		},
		{
			name:  "outside the layer passes through",
			exp:   experimentWith(&Allocation{Layer: ptr(nobody("l", subject)), Splits: treatment(everyone("x"))}),
			attrs: us,
			want:  Assignment{Variation: "control", Allocation: DefaultAllocation, Reason: ReasonPassThrough},
		},
		{
			name: "first matching split wins",
			exp: experimentWith(&Allocation{Splits: []Split{
				{Variation: "other", Shards: []Shard{nobody("x", subject)}},
				{Variation: "treatment", Shards: []Shard{everyone("x"), everyone("y")}},
				{Variation: "control", Shards: []Shard{everyone("x")}},
			}}),
			attrs: us,
			want: Assignment{Variation: "treatment", ExperimentKey: "widget-exp-a", Allocation: "exp-a",
				DoLog: true, Reason: ReasonSplit},
		},
		{
			name:  "a split needs every shard",
			exp:   experimentWith(&Allocation{Splits: treatment(everyone("x"), nobody("y", subject))}),
			attrs: us,
			want:  Assignment{Variation: "control", Allocation: DefaultAllocation, Reason: ReasonPassThrough},
		},
		{
			name:  "disabled flag",
			flag:  func(f *Flag) { f.Disabled = true },
			exp:   experimentWith(&Allocation{Splits: treatment(everyone("x"))}),
			attrs: us,
			want:  Assignment{Reason: ReasonDisabled},
		},
		{
			name:  "flag window closed",
			flag:  func(f *Flag) { f.ActiveUntil = ptr(now.Add(-time.Minute)) },
			exp:   experimentWith(&Allocation{Splits: treatment(everyone("x"))}),
			attrs: us,
			want:  Assignment{Reason: ReasonDisabled},
		},
		{
			name:  "disabled rule is skipped",
			flag:  func(f *Flag) { f.Rules[0].Disabled = true },
			exp:   experimentWith(&Allocation{Splits: treatment(everyone("x"))}),
			attrs: us,
			want:  Assignment{Variation: "control", Allocation: DefaultAllocation, Reason: ReasonDefault},
		},
		{
			name: "entity unit hashes the named attribute",
			exp: func() *Experiment {
				e := experimentWith(&Allocation{Splits: treatment(Shard{Salt: "x", Ranges: []Range{excluding("x", "slot-9")}})})
				e.Unit = Unit{Type: UnitEntity, Key: "slot"}
				return e
			}(),
			attrs: map[string]any{"region": "us", "slot": "slot-9"},
			want:  Assignment{Variation: "control", Allocation: DefaultAllocation, Reason: ReasonPassThrough},
		},
		{
			name: "entity unit missing passes through",
			exp: func() *Experiment {
				e := experimentWith(&Allocation{Splits: treatment(everyone("x"))})
				e.Unit = Unit{Type: UnitEntity, Key: "slot"}
				return e
			}(),
			attrs: us,
			want:  Assignment{Variation: "control", Allocation: DefaultAllocation, Reason: ReasonPassThrough},
		},
		{
			name: "entity unit numeric attribute",
			exp: func() *Experiment {
				e := experimentWith(&Allocation{Splits: treatment(Shard{Salt: "x", Ranges: []Range{{
					ShardOf("x", "42", 10000), ShardOf("x", "42", 10000) + 1,
				}}})})
				e.Unit = Unit{Type: UnitEntity, Key: "slot"}
				return e
			}(),
			attrs: map[string]any{"region": "us", "slot": 42.0},
			want: Assignment{Variation: "treatment", ExperimentKey: "widget-exp-a", Allocation: "exp-a",
				DoLog: true, Reason: ReasonSplit},
			wantExtra: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := widgetFlag()
			if tc.flag != nil {
				tc.flag(&f)
			}
			ev, err := New(f, tc.exp)
			if err != nil {
				t.Fatal(err)
			}
			s := subject
			if tc.subject != "" {
				s = tc.subject
			}
			got := ev.EvaluateAt(now, s, tc.attrs)
			if (got.ExtraLogging != nil) != tc.wantExtra {
				t.Errorf("extraLogging = %v, want present=%t", got.ExtraLogging, tc.wantExtra)
			}
			got.ExtraLogging = nil
			if !sameAssignment(got, tc.want) {
				t.Errorf("got  %+v\nwant %+v", got, tc.want)
			}
		})
	}
}

func TestInComparesAsStrings(t *testing.T) {
	f := Flag{
		Key:        "k",
		Variations: []string{"a", "b"},
		Rules:      []Rule{{Name: "r", Query: `device in ["1", "3"] and not (tier in ["gold"])`, Variation: "a"}},
		Default:    Rule{Variation: "a"},
	}
	e := experimentWith(nil)
	e.Allocations = map[string]*Allocation{"r": {Splits: []Split{{Variation: "b", Shards: []Shard{everyone("s")}}}}}
	ev, err := New(f, e)
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		attrs map[string]any
		want  string
	}{
		{map[string]any{"device": 3}, "b"},
		{map[string]any{"device": int64(1)}, "b"},
		{map[string]any{"device": 3.0}, "b"},
		{map[string]any{"device": float32(1)}, "b"},
		{map[string]any{"device": uint8(3)}, "b"},
		{map[string]any{"device": "3"}, "b"},
		{map[string]any{"device": 3.5}, "a"},
		{map[string]any{"device": "03"}, "a"},
		{map[string]any{"device": 2}, "a"},
		{map[string]any{"device": 3, "tier": "gold"}, "a"},
		{map[string]any{"device": 3, "tier": "Gold"}, "b"},
		{map[string]any{}, "a"},
	} {
		if got := ev.Evaluate("u", tc.attrs); got.Variation != tc.want {
			t.Errorf("%v: got %s (%s), want %s", tc.attrs, got.Variation, got.Reason, tc.want)
		}
	}
}

func TestInHandlesBoolsNumbersAndNestedAttributes(t *testing.T) {
	for _, tc := range []struct {
		query string
		attrs map[string]any
		want  bool
	}{
		{`beta in ["true"]`, map[string]any{"beta": true}, true},
		{`beta in ["false"]`, map[string]any{"beta": true}, false},
		{`size in [10, 20]`, map[string]any{"size": 20.0}, true},
		{`size in [10, 20]`, map[string]any{"size": "20"}, true},
		{`user.plan in ["pro"]`, map[string]any{"user": map[string]any{"plan": "pro"}}, true},
		{`name in ["a, b", "c]"]`, map[string]any{"name": "c]"}, true},
		{`name in ["say \"hi\""]`, map[string]any{"name": `say "hi"`}, true},
		{`name eq "in [x]"`, map[string]any{"name": "in [x]"}, true},
		{`id in ["u"]`, map[string]any{}, true},
		{`id in ["u"]`, map[string]any{"id": "other"}, false},
		{`targetingKey eq "u"`, map[string]any{}, true},
		{`(a in ["1"]) or (b in ["2"])`, map[string]any{"b": 2}, true},
	} {
		q, err := compileQuery(tc.query)
		if err != nil {
			t.Fatalf("%s: %v", tc.query, err)
		}
		ctx := map[string]any{"targetingKey": "u"}
		for k, v := range tc.attrs {
			ctx[k] = v
		}
		if _, ok := tc.attrs["id"]; !ok {
			ctx["id"] = "u"
		}
		if got := q.matches(ctx); got != tc.want {
			t.Errorf("%s with %v = %t, want %t", tc.query, tc.attrs, got, tc.want)
		}
	}
}

func TestCompileQueryRejectsBrokenQueries(t *testing.T) {
	for _, q := range []string{`a in ["x"`, `a eq "x`} {
		if _, err := compileQuery(q); err == nil {
			t.Errorf("%s: expected an error", q)
		}
	}
}

func TestStockRulesUseGOFFPercentages(t *testing.T) {
	f := Flag{
		Key:        "k",
		Variations: []string{"a", "b"},
		Rules:      []Rule{{Name: "pct", Query: `targetingKey pr`, Percentages: map[string]float64{"a": 50, "b": 50}}},
		Default:    Rule{Variation: "a"},
	}
	ev, err := New(f, nil)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for i := 0; i < 2000; i++ {
		got := ev.Evaluate("user-"+string(rune('a'+i%26))+strings.Repeat("x", i%7)+time.Duration(i).String(), nil)
		if got.Reason != ReasonStock || got.Allocation != "pct" || got.DoLog {
			t.Fatalf("unexpected %+v", got)
		}
		seen[got.Variation]++
	}
	if seen["a"] < 800 || seen["b"] < 800 {
		t.Errorf("GO Feature Flag percentages not applied: %v", seen)
	}
}

func TestJSONLogicExperimentRule(t *testing.T) {
	f := Flag{
		Key:        "k",
		Variations: []string{"a", "b"},
		Rules:      []Rule{{Name: "r", Query: `{"==": [{"var": "region"}, "us"]}`, Variation: "a"}},
		Default:    Rule{Variation: "a"},
	}
	e := experimentWith(nil)
	e.Allocations = map[string]*Allocation{"r": {Splits: []Split{{Variation: "b", Shards: []Shard{everyone("s")}}}}}
	ev, err := New(f, e)
	if err != nil {
		t.Fatal(err)
	}
	if got := ev.Evaluate("u", map[string]any{"region": "us"}); got.Variation != "b" {
		t.Errorf("jsonlogic match: %+v", got)
	}
	if got := ev.Evaluate("u", map[string]any{"region": "eu"}); got.Variation != "a" || got.Reason != ReasonDefault {
		t.Errorf("jsonlogic miss: %+v", got)
	}
}

func TestNewRejectsInvalidExperiments(t *testing.T) {
	e := experimentWith(&Allocation{Splits: []Split{{Variation: "missing", Shards: []Shard{everyone("s")}}}})
	if _, err := New(widgetFlag(), e); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("expected a validation error, got %v", err)
	}
}

func TestEvaluateDoesNotMutateAttributes(t *testing.T) {
	ev, err := New(widgetFlag(), experimentWith(&Allocation{Splits: []Split{{Variation: "treatment", Shards: []Shard{everyone("s")}}}}))
	if err != nil {
		t.Fatal(err)
	}
	attrs := map[string]any{"region": "us"}
	ev.Evaluate("u", attrs)
	if len(attrs) != 1 {
		t.Fatalf("attributes were mutated: %v", attrs)
	}
}

func TestGrowRanges(t *testing.T) {
	for _, tc := range []struct {
		name  string
		in    []Range
		extra int
		want  []Range
	}{
		{"from nothing", nil, 100, []Range{{0, 100}}},
		{"extends the end", []Range{{0, 100}}, 100, []Range{{0, 200}}},
		{"fills a gap after the last range", []Range{{0, 100}, {300, 400}}, 50, []Range{{0, 100}, {300, 450}}},
		{"wraps to the start", []Range{{9900, 10000}}, 150, []Range{{0, 150}, {9900, 10000}}},
		{"skips covered ranges when wrapping", []Range{{0, 50}, {9950, 10000}}, 100, []Range{{0, 150}, {9950, 10000}}},
		{"caps at everything", []Range{{0, 9990}}, 50, []Range{{0, 10000}}},
		{"no growth", []Range{{0, 10}}, 0, []Range{{0, 10}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := GrowRanges(tc.in, tc.extra, 10000)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("got %v, want %v", got, tc.want)
				}
			}
			for _, r := range tc.in {
				for v := r.Start; v < r.End; v++ {
					if !(Shard{Ranges: got}).contains(v) {
						t.Fatalf("growth dropped shard %d", v)
					}
				}
			}
		})
	}
}

func sameAssignment(a, b Assignment) bool {
	return a.Variation == b.Variation && a.ExperimentKey == b.ExperimentKey &&
		a.Allocation == b.Allocation && a.DoLog == b.DoLog && a.Reason == b.Reason
}
