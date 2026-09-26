package analysis

import (
	"encoding/json"
	"math"
	"testing"
	"time"
)

func spec() ExperimentSpec {
	two := 2.0
	return ExperimentSpec{
		Key:        "checkout-exp-us-east-1",
		Control:    "control",
		Variants:   []string{"control", "one_page", "express"},
		UnitType:   "request",
		Start:      time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		End:        time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC),
		Test:       "sequential",
		Alpha:      0.05,
		Correction: "none",
		Metrics: []MetricSpec{
			{Key: "conversion_rate", Name: "Conversion rate", Kind: "mean", Role: "primary", Numerator: "converted_yn", Format: "percent", Direction: "increase"},
			{Key: "add_to_cart_rate", Name: "Add-to-cart rate", Kind: "mean", Role: "secondary", Numerator: "added_to_cart_yn", Format: "percent", Direction: "increase"},
			{Key: "avg_order_value", Name: "Average order value", Kind: "ratio", Role: "guardrail", Numerator: "order_value", Denominator: "order_count", Format: "currency", Direction: "increase", MaxDropPct: &two},
		},
		Segments: []string{"plan"},
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
	return ArmStat{Variant: variant, Lift: num(lift), CILow: num(lo), CIHigh: num(hi), Significant: lo > 0 || hi < 0, Guardrail: guard}
}

func TestDecide(t *testing.T) {
	end := time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)
	before, after := end.Add(-time.Hour), end.Add(time.Hour)
	win := MetricResult{Name: "Bid rate", Role: "primary", Direction: "increase", Results: []ArmStat{arm("b", 0.03, 0.01, 0.05, nil)}}
	flat := MetricResult{Name: "Bid rate", Role: "primary", Direction: "increase", Results: []ArmStat{arm("b", 0.01, -0.01, 0.03, nil)}}
	guardOK := MetricResult{Name: "Order value", Role: "guardrail", Direction: "increase", Results: []ArmStat{arm("b", -0.001, -0.01, 0.008, &GuardrailCheck{MaxDropPct: num(2), Pass: true})}}
	guardWide := MetricResult{Name: "Order value", Role: "guardrail", Direction: "increase", Results: []ArmStat{arm("b", -0.005, -0.04, 0.03, &GuardrailCheck{MaxDropPct: num(2), Pass: false})}}
	guardHurt := MetricResult{Name: "Order value", Role: "guardrail", Direction: "increase", Results: []ArmStat{arm("b", -0.05, -0.07, -0.03, &GuardrailCheck{MaxDropPct: num(2), Pass: false})}}
	undefined := MetricResult{Name: "Bid rate", Role: "primary", Direction: "increase", Results: []ArmStat{{Variant: "b"}}}
	no, yes := false, true
	guardNoData := MetricResult{Name: "Order value", Role: "guardrail", Direction: "increase", Results: []ArmStat{{Variant: "b", Guardrail: &GuardrailCheck{Pass: false, SignificantHarm: &no, Reason: "no usable data"}}}}
	guardHarmFlag := MetricResult{Name: "Order value", Role: "guardrail", Direction: "increase", Results: []ArmStat{arm("b", -0.001, -0.01, 0.008, &GuardrailCheck{MaxDropPct: num(2), Pass: false, SignificantHarm: &yes})}}
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
		{"undefined lift is never a win", []MetricResult{undefined}, after, "do_not_roll_out"},
		{"guardrail with no usable data", []MetricResult{win, guardNoData}, before, "discuss"},
		{"guardrail reports significant harm", []MetricResult{win, guardHarmFlag}, before, "do_not_roll_out"},
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
	e := spec()
	e.CUPED = true
	now := e.Start.Add(10 * 24 * time.Hour)

	a := generate(e, now)
	b := generate(e, now)
	if !a.Sample {
		t.Fatal("generated results must be labelled sample")
	}
	if a.Status != "ok" || len(a.Metrics) != 3 || len(a.Variants) != 3 {
		t.Fatalf("unexpected shape: status=%s metrics=%d variants=%d", a.Status, len(a.Metrics), len(a.Variants))
	}
	if *a.Metrics[0].Results[0].Lift != *b.Metrics[0].Results[0].Lift || a.SRM != b.SRM {
		t.Error("sample results must be deterministic for the same inputs")
	}
	for _, m := range a.Metrics {
		for _, r := range m.Results {
			if *r.CILow > *r.Lift || *r.CIHigh < *r.Lift {
				t.Errorf("%s/%s: lift %v outside CI [%v, %v]", m.Key, r.Variant, *r.Lift, *r.CILow, *r.CIHigh)
			}
			if (*r.PValue < e.Alpha) != r.Significant {
				t.Errorf("%s/%s: p = %v disagrees with significant = %v", m.Key, r.Variant, *r.PValue, r.Significant)
			}
			if r.CUPED == nil || r.Raw == nil {
				t.Fatalf("%s/%s: CUPED requested but cuped/raw missing", m.Key, r.Variant)
			}
			// The headline is the adjusted estimate: it matches the cuped block and is no wider than raw.
			if *r.CILow != *r.CUPED.CILow || *r.CIHigh != *r.CUPED.CIHigh {
				t.Errorf("%s/%s: top level is not the CUPED estimate", m.Key, r.Variant)
			}
			if *r.CIHigh-*r.CILow > *r.Raw.CIHigh-*r.Raw.CILow {
				t.Errorf("%s/%s: adjusted interval wider than raw", m.Key, r.Variant)
			}
			if (m.Role == "guardrail") != (r.Guardrail != nil) {
				t.Errorf("%s/%s: guardrail block only belongs on guardrail metrics", m.Key, r.Variant)
			}
		}
	}
	if len(a.Segments) != 2 {
		t.Errorf("plan should give two segments, got %d", len(a.Segments))
	}
	if len(a.Timeseries) != 10*2 {
		t.Errorf("timeseries should have a point per day per treatment, got %d", len(a.Timeseries))
	}
	if a.Decision == nil || a.Decision.Recommendation == "" {
		t.Error("sample results need a decision")
	}
}

func TestSampleBeforeStartHasNoData(t *testing.T) {
	e := spec()
	got := generate(e, e.Start.Add(-time.Hour))
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

func TestSampleWithoutCUPEDHasNoRawOrCupedBlocks(t *testing.T) {
	e := spec()
	e.CUPED = false
	r := generate(e, e.Start.Add(5*24*time.Hour))
	for _, m := range r.Metrics {
		for _, a := range m.Results {
			if a.Raw != nil || a.CUPED != nil {
				t.Errorf("%s/%s: raw and cuped must be null without CUPED", m.Key, a.Variant)
			}
		}
	}
}

func TestResultsReadNullEstimatesAndNewFields(t *testing.T) {
	doc := `{"status":"ok","method":{"test":"sequential","alpha":0.05,"cuped":true,"correction":"none","sequential_tuning":{"b":1.2}},
	"metrics":[{"key":"m","role":"guardrail","results":[{"variant":"b","value":0,"control_value":0,"lift":null,"ci_low":null,"ci_high":null,
	"p_value":null,"adjusted_p":null,"significant":false,"raw":null,"cuped":null,
	"guardrail":{"pass":false,"significant_harm":false,"reason":"no usable data"}}]}]}`
	var r Results
	if err := json.Unmarshal([]byte(doc), &r); err != nil {
		t.Fatal(err)
	}
	a := r.Metrics[0].Results[0]
	if a.Lift != nil || a.PValue != nil || a.Guardrail.Reason != "no usable data" || a.Guardrail.MaxDropPct != nil {
		t.Errorf("parsed %+v", a)
	}
	if r.Method.SequentialTuning["b"] != 1.2 {
		t.Errorf("sequential_tuning = %v", r.Method.SequentialTuning)
	}
}
