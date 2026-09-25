package experiments

import (
	"math"
	"strings"
	"testing"
	"time"
)

func catalog() map[string]Metric {
	out := map[string]Metric{}
	for _, m := range []Metric{
		{Key: "dsp_bid_rate", Name: "DSP bid rate", Kind: "mean", Numerator: "dsp_bid_yn", Format: "percent", Direction: "increase"},
		{Key: "dsp_win_rate", Name: "DSP win rate", Kind: "mean", Numerator: "dsp_won_yn", Format: "percent", Direction: "increase"},
		{Key: "avg_bid_cpm", Name: "Average bid CPM", Kind: "ratio", Numerator: "dsp_bid_price", Denominator: "dsp_bid_count", Format: "currency", Direction: "increase"},
	} {
		out[m.Key] = m
	}
	return out
}

func valid() Experiment {
	e := Experiment{
		Key:         "tmax-exp-us-east-1",
		Name:        "TMAX US-East",
		Owner:       "bidder",
		Hypothesis:  "Lower tmax raises bid rate",
		Flag:        "tmax",
		Environment: "production",
		Allocations: []string{"exp-us-east-1"},
		Control:     "control",
		Variants:    []string{"control", "tmax150", "tmax225"},
		Start:       time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		End:         time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC),
		Metrics: MetricSet{
			Primary:    []string{"dsp_bid_rate"},
			Secondary:  []string{"dsp_win_rate"},
			Guardrails: []Guardrail{{Metric: "avg_bid_cpm", MaxDropPct: 2}},
		},
		Segments: []string{"auction_type"},
	}
	e = e.Normalized()
	return e
}

func shape() *FlagShape {
	return &FlagShape{Variations: []string{"control", "tmax150", "tmax225"}, Rules: []string{"exp-us-east-1"}}
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
		{"unknown variant", func(e *Experiment) { e.Variants = append(e.Variants, "tmax999") }, shape(), `"tmax999" is not a variation`},
		{"unknown rule", func(e *Experiment) { e.Allocations = []string{"nope"} }, shape(), `allocation "nope"`},
		{"control not a variant", func(e *Experiment) { e.Control = "tmax999" }, shape(), "control"},
		{"one arm", func(e *Experiment) { e.Variants = []string{"control"} }, shape(), "at least one other"},
		{"unknown metric", func(e *Experiment) { e.Metrics.Secondary = []string{"mystery"} }, shape(), `"mystery" is not in the metric catalog`},
		{"no primary", func(e *Experiment) { e.Metrics.Primary = nil }, shape(), "primary"},
		{"guardrail tolerance", func(e *Experiment) { e.Metrics.Guardrails[0].MaxDropPct = 0 }, shape(), "max drop"},
		{"bad test", func(e *Experiment) { e.Analysis.Test = "bayes" }, shape(), "sequential or fixed"},
		{"bad alpha", func(e *Experiment) { e.Analysis.Alpha = 0.7 }, shape(), "alpha"},
		{"bad correction", func(e *Experiment) { e.Analysis.Correction = "bonf" }, shape(), "holm"},
		{"bad status", func(e *Experiment) { e.Status = "paused" }, shape(), "status"},
		{"bad unit", func(e *Experiment) { e.Unit.Type = "session" }, shape(), "unit type"},
		{"missing flag", func(*Experiment) {}, nil, `no flag "tmax"`},
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

func TestNormalQuantile(t *testing.T) {
	for p, want := range map[float64]float64{0.975: 1.959964, 0.8: 0.841621, 0.5: 0, 0.01: -2.326348} {
		if got := normalQuantile(p); math.Abs(got-want) > 1e-5 {
			t.Errorf("quantile(%v) = %v, want %v", p, got, want)
		}
	}
}

func TestSRMCheck(t *testing.T) {
	balanced := SRMCheck([]int64{50_000, 50_100}, []float64{0.5, 0.5})
	if balanced.Flag || balanced.PValue < 0.5 {
		t.Errorf("balanced traffic flagged: %+v", balanced)
	}
	skewed := SRMCheck([]int64{50_000, 52_000}, []float64{0.5, 0.5})
	if !skewed.Flag || skewed.PValue > 0.001 {
		t.Errorf("skewed traffic not flagged: %+v", skewed)
	}
	// Chi-square for 50000/52000 is 2000^2/102000 = 39.2 with one degree of freedom.
	if math.Abs(skewed.Chi2-39.2157) > 0.01 {
		t.Errorf("chi2 = %v", skewed.Chi2)
	}
}

func arm(variant string, lift, lo, hi float64, guard *GuardrailCheck) ArmStat {
	return ArmStat{Variant: variant, Lift: lift, CILow: lo, CIHigh: hi, Significant: lo > 0 || hi < 0, Guardrail: guard}
}

func TestDecide(t *testing.T) {
	end := time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)
	before, after := end.Add(-time.Hour), end.Add(time.Hour)
	win := MetricResult{Name: "Bid rate", Role: "primary", Direction: "increase", Results: []ArmStat{arm("b", 0.03, 0.01, 0.05, nil)}}
	flat := MetricResult{Name: "Bid rate", Role: "primary", Direction: "increase", Results: []ArmStat{arm("b", 0.01, -0.01, 0.03, nil)}}
	guardOK := MetricResult{Name: "CPM", Role: "guardrail", Direction: "increase", Results: []ArmStat{arm("b", -0.001, -0.01, 0.008, &GuardrailCheck{MaxDropPct: 2, Pass: true})}}
	guardWide := MetricResult{Name: "CPM", Role: "guardrail", Direction: "increase", Results: []ArmStat{arm("b", -0.005, -0.04, 0.03, &GuardrailCheck{MaxDropPct: 2, Pass: false})}}
	guardHurt := MetricResult{Name: "CPM", Role: "guardrail", Direction: "increase", Results: []ArmStat{arm("b", -0.05, -0.07, -0.03, &GuardrailCheck{MaxDropPct: 2, Pass: false})}}
	lowerIsBetter := MetricResult{Name: "Timeouts", Role: "primary", Direction: "decrease", Results: []ArmStat{arm("b", -0.1, -0.15, -0.05, nil)}}

	cases := []struct {
		name    string
		metrics []MetricResult
		now     time.Time
		want    string
	}{
		{"win, guardrails pass", []MetricResult{win, guardOK}, before, "roll_out"},
		{"win, guardrail inconclusive", []MetricResult{win, guardWide}, before, "discuss"},
		{"win, guardrail hurt", []MetricResult{win, guardHurt}, before, "do_not_roll_out"},
		{"neutral before end", []MetricResult{flat}, before, "keep_running"},
		{"neutral after end", []MetricResult{flat}, after, "do_not_roll_out"},
		{"decrease metric improving", []MetricResult{lowerIsBetter}, before, "roll_out"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Decide(tc.metrics, end, tc.now)
			if got.Recommendation != tc.want {
				t.Errorf("got %s (%s), want %s", got.Recommendation, got.Reason, tc.want)
			}
		})
	}
}

func TestSampleIsDeterministicAndLabelled(t *testing.T) {
	e := valid()
	e.Analysis.CUPED = true
	now := e.Start.Add(10 * 24 * time.Hour)

	a := Sample(e, catalog(), now)
	b := Sample(e, catalog(), now)
	if !a.Sample {
		t.Fatal("generated results must be labelled sample")
	}
	if a.Status != "ok" || len(a.Metrics) != 3 || len(a.Variants) != 3 {
		t.Fatalf("unexpected shape: status=%s metrics=%d variants=%d", a.Status, len(a.Metrics), len(a.Variants))
	}
	if a.Metrics[0].Results[0].Lift != b.Metrics[0].Results[0].Lift || a.SRM != b.SRM {
		t.Error("sample results must be deterministic for the same inputs")
	}
	for _, m := range a.Metrics {
		for _, r := range m.Results {
			if r.CILow > r.Lift || r.CIHigh < r.Lift {
				t.Errorf("%s/%s: lift %v outside CI [%v, %v]", m.Key, r.Variant, r.Lift, r.CILow, r.CIHigh)
			}
			if (r.PValue < e.Analysis.Alpha) != r.Significant {
				t.Errorf("%s/%s: p = %v disagrees with significant = %v", m.Key, r.Variant, r.PValue, r.Significant)
			}
			if r.CUPED == nil {
				t.Errorf("%s/%s: CUPED requested but missing", m.Key, r.Variant)
			}
			if (m.Role == "guardrail") != (r.Guardrail != nil) {
				t.Errorf("%s/%s: guardrail block only belongs on guardrail metrics", m.Key, r.Variant)
			}
		}
	}
	if len(a.Segments) != 2 {
		t.Errorf("auction_type should give two segments, got %d", len(a.Segments))
	}
	if len(a.Timeseries) != 10*2 {
		t.Errorf("timeseries should have a point per day per treatment, got %d", len(a.Timeseries))
	}
	if a.Decision == nil || a.Decision.Recommendation == "" {
		t.Error("sample results need a decision")
	}
}

func TestSampleBeforeStartHasNoData(t *testing.T) {
	e := valid()
	got := Sample(e, catalog(), e.Start.Add(-time.Hour))
	if got.Status != "insufficient_data" || got.Message == nil {
		t.Errorf("status = %s", got.Status)
	}
}

func TestEstimate(t *testing.T) {
	req := PowerRequest{BaselineMean: 0.4, Variance: 0.24, NPerDay: 100_000, Arms: 2, Alpha: 0.05, Power: 0.8, Days: 14, TargetMDE: 0.01}
	got, err := Estimate(req)
	if err != nil {
		t.Fatal(err)
	}
	// n per arm = 700000; mde = 2.8016 * sqrt(2*0.24/700000) / 0.4
	want := 2.80158 * math.Sqrt(2*0.24/700_000) / 0.4
	if math.Abs(got.MDE-want) > 1e-5 {
		t.Errorf("mde = %v, want %v", got.MDE, want)
	}
	if got.DaysToMDE == nil || *got.DaysToMDE < 1 || *got.DaysToMDE > 14 {
		t.Errorf("days to 1%% MDE = %v", got.DaysToMDE)
	}
	withCUPED := req
	withCUPED.CUPEDRho2 = 0.5
	reduced, _ := Estimate(withCUPED)
	if reduced.MDE >= got.MDE {
		t.Error("CUPED should shrink the MDE")
	}
	if len(got.Curve) != 8 {
		t.Errorf("curve points = %d", len(got.Curve))
	}
	if _, err := Estimate(PowerRequest{}); err == nil {
		t.Error("an empty request must be rejected")
	}
}
