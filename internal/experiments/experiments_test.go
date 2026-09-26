package experiments

import (
	"strings"
	"testing"
	"time"
)

func catalog() map[string]Metric {
	out := map[string]Metric{}
	for _, m := range []Metric{
		{Key: "conversion_rate", Name: "Conversion rate", Kind: "mean", Numerator: "converted_yn", Format: "percent", Direction: "increase"},
		{Key: "add_to_cart_rate", Name: "Add-to-cart rate", Kind: "mean", Numerator: "added_to_cart_yn", Format: "percent", Direction: "increase"},
		{Key: "avg_order_value", Name: "Average order value", Kind: "ratio", Numerator: "order_value", Denominator: "order_count", Format: "currency", Direction: "increase"},
	} {
		out[m.Key] = m
	}
	return out
}

func valid() Experiment {
	e := Experiment{
		Key:         "checkout-exp-us-east-1",
		Name:        "Checkout US-East",
		Owner:       "checkout",
		Hypothesis:  "One-page checkout raises conversion",
		Flag:        "checkout",
		Environment: "production",
		Allocations: []string{"exp-us-east-1"},
		Control:     "control",
		Variants:    []string{"control", "one_page", "express"},
		Start:       time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		End:         time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC),
		Metrics: MetricSet{
			Primary:    []string{"conversion_rate"},
			Secondary:  []string{"add_to_cart_rate"},
			Guardrails: []Guardrail{{Metric: "avg_order_value", MaxDropPct: 2}},
		},
		Segments: []string{"plan"},
	}
	e = e.Normalized()
	return e
}

func shape() *FlagShape {
	return &FlagShape{Variations: []string{"control", "one_page", "express"}, Rules: []string{"exp-us-east-1"}}
}

func TestValidExperimentPasses(t *testing.T) {
	if err := ValidateExperiment(valid(), shape(), catalog()); err != nil {
		t.Fatal(err)
	}
}

func TestNormalizeFillsContractDefaults(t *testing.T) {
	var e Experiment
	e = e.Normalized()
	if e.Status != StatusDraft || e.Unit.Type != "request" || e.Unit.Key != "targetingKey" ||
		e.Analysis.Test != "sequential" || e.Analysis.Alpha != 0.05 || e.Analysis.Power != 0.8 || e.Analysis.Correction != "none" {
		t.Errorf("defaults not applied: %+v", e)
	}
}

func TestExperimentValidationValidationError(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Experiment)
		flag   *FlagShape
		want   string
	}{
		{"bad key", func(e *Experiment) { e.Key = "Has Spaces" }, shape(), "lowercase"},
		{"no end", func(e *Experiment) { e.End = time.Time{} }, shape(), "end date"},
		{"end before start", func(e *Experiment) { e.End = e.Start.Add(-time.Hour) }, shape(), "after the start"},
		{"too long", func(e *Experiment) { e.End = e.Start.Add(MaxDuration + time.Hour) }, shape(), "8 weeks"},
		{"unknown variant", func(e *Experiment) { e.Variants = append(e.Variants, "mystery_arm") }, shape(), `"mystery_arm" is not a variation`},
		{"unknown rule", func(e *Experiment) { e.Allocations = []string{"nope"} }, shape(), `allocation "nope"`},
		{"control not a variant", func(e *Experiment) { e.Control = "mystery_arm" }, shape(), "control"},
		{"one arm", func(e *Experiment) { e.Variants = []string{"control"} }, shape(), "at least one other"},
		{"unknown metric", func(e *Experiment) { e.Metrics.Secondary = []string{"mystery"} }, shape(), `"mystery" is not in the metric catalog`},
		{"no primary", func(e *Experiment) { e.Metrics.Primary = nil }, shape(), "primary"},
		{"guardrail tolerance", func(e *Experiment) { e.Metrics.Guardrails[0].MaxDropPct = 0 }, shape(), "max drop"},
		{"bad test", func(e *Experiment) { e.Analysis.Test = "bayes" }, shape(), "sequential or fixed"},
		{"bad alpha", func(e *Experiment) { e.Analysis.Alpha = 0.7 }, shape(), "alpha"},
		{"bad correction", func(e *Experiment) { e.Analysis.Correction = "bonf" }, shape(), "holm"},
		{"bad status", func(e *Experiment) { e.Status = "paused" }, shape(), "status"},
		{"bad unit", func(e *Experiment) { e.Unit.Type = "session" }, shape(), "unit type"},
		{"missing flag", func(*Experiment) {}, nil, `no flag "checkout"`},
		{"bad decision", func(e *Experiment) { e.Decision = &Decision{Outcome: "ship"} }, shape(), "decision"},
		{"decision variant", func(e *Experiment) { e.Decision = &Decision{Outcome: "roll_out", Variant: "x"} }, shape(), "not a variant"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := valid()
			tc.mutate(&e)
			err := ValidateExperiment(e, tc.flag, catalog())
			if err == nil {
				t.Fatal("expected a problem")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q should mention %q", err, tc.want)
			}
		})
	}
}

func TestExtendedAllowsLongerWindows(t *testing.T) {
	e := valid()
	e.End = e.Start.Add(MaxDuration + 24*time.Hour)
	e.Extended = true
	if err := ValidateExperiment(e, shape(), catalog()); err != nil {
		t.Fatal(err)
	}
}

func TestExactlyEightWeeksIsAllowed(t *testing.T) {
	e := valid()
	e.End = e.Start.Add(MaxDuration)
	if err := ValidateExperiment(e, shape(), catalog()); err != nil {
		t.Fatal(err)
	}
}

func TestMetricValidation(t *testing.T) {
	ok := Metric{Key: "gross_revenue", Name: "Gross revenue", Kind: "mean", Numerator: "gross_revenue", Format: "currency", Direction: "increase"}
	if err := ValidateMetric(ok); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		mutate func(*Metric)
		want   string
	}{
		{"ratio needs denominator", func(m *Metric) { m.Kind = "ratio" }, "denominator"},
		{"mean has none", func(m *Metric) { m.Denominator = "x" }, "no denominator"},
		{"bad kind", func(m *Metric) { m.Kind = "sum" }, "kind"},
		{"bad format", func(m *Metric) { m.Format = "bytes" }, "format"},
		{"bad direction", func(m *Metric) { m.Direction = "up" }, "direction"},
		{"bad cap", func(m *Metric) { m.Cap = &Cap{Pct: 10} }, "cap"},
		{"bad key", func(m *Metric) { m.Key = "Gross Revenue" }, "key"},
		{"no numerator", func(m *Metric) { m.Numerator = "" }, "numerator"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := ok
			tc.mutate(&m)
			err := ValidateMetric(m)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %v should mention %q", err, tc.want)
			}
		})
	}
}

func TestYAMLRoundTripKeepsUnknownKeys(t *testing.T) {
	raw := []byte(`key: a
name: A
owner: t
hypothesis: h
flag: f
environment: production
allocations: [r]
control: c
variants: [c, v]
unit: {type: request, key: targetingKey}
start: 2026-10-01T00:00:00Z
end: 2026-10-08T00:00:00Z
status: running
metrics:
  primary: [m]
analysis:
  test: fixed
  alpha: 0.1
  power: 0.9
  cuped: true
  covariate: pre_bids
  correction: holm
future_field:
  nested: 1
`)
	e, err := ParseExperiment(raw)
	if err != nil {
		t.Fatal(err)
	}
	if e.Analysis.Test != "fixed" || e.Analysis.Covariate != "pre_bids" || e.Status != StatusRunning {
		t.Errorf("parsed = %+v", e)
	}
	out, err := e.YAML()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "future_field:") {
		t.Errorf("unknown keys must survive a round trip:\n%s", out)
	}
	if !strings.Contains(string(out), "start: 2026-10-01T00:00:00Z") {
		t.Errorf("dates should be written as RFC 3339:\n%s", out)
	}
	again, err := ParseExperiment(out)
	if err != nil || !again.End.Equal(e.End) {
		t.Errorf("re-parse failed: %v %+v", err, again)
	}
}

func TestSpecResolvesCatalogMetricsInReportingOrder(t *testing.T) {
	spec := valid().Spec(catalog())
	var got []string
	for _, m := range spec.Metrics {
		got = append(got, m.Role+":"+m.Key)
	}
	if strings.Join(got, ",") != "primary:conversion_rate,secondary:add_to_cart_rate,guardrail:avg_order_value" {
		t.Errorf("metrics = %v", got)
	}
	guard := spec.Metrics[2]
	if guard.MaxDropPct == nil || *guard.MaxDropPct != 2 || guard.Kind != "ratio" || guard.Denominator != "order_count" {
		t.Errorf("guardrail = %+v", guard)
	}
	if spec.Metrics[0].MaxDropPct != nil {
		t.Error("only guardrails carry a tolerance")
	}
	if spec.Test != "sequential" || spec.Alpha != 0.05 || spec.Control != "control" || len(spec.Variants) != 3 {
		t.Errorf("spec = %+v", spec)
	}
}
