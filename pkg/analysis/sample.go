package analysis

import (
	"context"
	"fmt"
	"hash/fnv"
	"math"
	"math/rand/v2"
	"slices"
	"time"
)

var segmentValues = map[string][]string{
	"plan":    {"free", "pro"},
	"channel": {"web", "app"},
	"device":  {"desktop", "mobile", "ctv"},
	"country": {"us", "ca"},
}

// Sample generates deterministic, clearly labelled demo results from the
// experiment spec alone, so Studio can be tried without an analysis service.
// It is never the default: set analysis.provider to sample to use it.
type Sample struct {
	// Now defaults to time.Now.
	Now func() time.Time
}

func (*Sample) Name() string { return "sample" }

// Local reports that Sample never does I/O, so list pages may call it freely.
func (*Sample) Local() bool { return true }

func (s *Sample) Results(_ context.Context, req ResultsRequest) (*Results, error) {
	now := time.Now()
	if s.Now != nil {
		now = s.Now()
	}
	if req.AsOf != "" {
		if day, err := time.Parse(time.DateOnly, req.AsOf); err == nil {
			if end := day.Add(24*time.Hour - time.Second); end.Before(now) {
				now = end
			}
		}
	}
	res := generate(req.Spec, now)
	return &res, nil
}

func (*Sample) Power(ctx context.Context, req PowerRequest) (*PowerResult, error) {
	return Builtin{}.Power(ctx, req)
}

func generate(e ExperimentSpec, now time.Time) Results {
	rng := rand.New(rand.NewPCG(seed(e.Key), 0x5eed)) //nolint:gosec // deterministic sample data, seeded on purpose
	now = now.UTC()

	out := Results{
		ExperimentKey: e.Key,
		Status:        "ok",
		Unit:          e.UnitType,
		Method: Method{
			Test: e.Test, Alpha: e.Alpha,
			CUPED: e.CUPED, Correction: e.Correction,
		},
		Sample:      true,
		Metrics:     []MetricResult{},
		Segments:    []SegmentResult{},
		Timeseries:  []TimePoint{},
		Diagnostics: []Diagnostic{},
		Variants:    []VariantUnits{},
	}

	asOf := now.Truncate(time.Hour)
	if asOf.After(e.End) {
		asOf = e.End
	}
	out.AsOf = asOf.Format(time.RFC3339)

	days := int(asOf.Sub(e.Start).Hours() / 24)
	if days < 1 || len(e.Variants) < 2 {
		msg := "The experiment has not collected a full day of data yet."
		out.Status = "insufficient_data"
		out.Message = &msg
		out.SRM = SRM{PValue: 1}
		return out
	}

	share := 1 / float64(len(e.Variants))
	perDay := 40_000 + rng.Float64()*60_000
	var units []int64
	var shares []float64
	for _, v := range e.Variants {
		n := int64(perDay * float64(days) * share * (1 + (rng.Float64()-0.5)*0.002))
		units = append(units, n)
		shares = append(shares, share)
		out.Variants = append(out.Variants, VariantUnits{Key: v, IsControl: v == e.Control, Units: n, ExpectedShare: round(share, 4)})
	}
	out.SRM = SRMCheck(units, shares)

	gen := sampleGen{e: e, rng: rng, days: days, n: perDay * float64(days) * share}
	lifts := map[string]float64{}
	for _, v := range e.Variants {
		if v != e.Control {
			lifts[v] = -0.01 + rng.Float64()*0.05
		}
	}

	for _, m := range e.Metrics {
		out.Metrics = append(out.Metrics, gen.metric(m, lifts))
	}

	for _, dim := range e.Segments {
		values := segmentValues[dim]
		if values == nil {
			values = []string{"a", "b"}
		}
		for i, val := range values {
			segGen := gen
			segGen.n = gen.n / float64(len(values))
			var ms []MetricResult
			for _, m := range e.Metrics {
				if m.Role != "primary" {
					continue
				}
				shifted := map[string]float64{}
				for v, l := range lifts {
					shifted[v] = l * (0.6 + 0.8*float64(i)/float64(len(values)))
				}
				ms = append(ms, segGen.metric(m, shifted))
			}
			out.Segments = append(out.Segments, SegmentResult{Dimension: dim, Value: val, Metrics: ms})
		}
	}

	out.Timeseries = gen.timeseries(out.Metrics)
	out.Diagnostics = diagnostics(out, days, e)
	rec := Decide(out.Metrics, e.End, now)
	out.Decision = &rec
	return out
}

type sampleGen struct {
	e    ExperimentSpec
	rng  *rand.Rand
	days int
	n    float64
}

func (g sampleGen) metric(m MetricSpec, lifts map[string]float64) MetricResult {
	if m.Name == "" {
		m.Name = m.Key
	}
	if m.Kind == "" {
		m.Kind = "mean"
	}
	if m.Format == "" {
		m.Format = "number"
	}
	if m.Direction == "" {
		m.Direction = "increase"
	}
	role := m.Role
	res := MetricResult{Key: m.Key, Name: m.Name, Kind: m.Kind, Role: role, Direction: m.Direction, Format: m.Format, Results: []ArmStat{}}

	base, cv := baseline(m.Format, g.rng)
	zFixed := normalQuantile(1 - g.e.Alpha/2)
	z := zFixed
	if g.e.Test == "sequential" {
		z *= 1.25
	}
	spread := 0.5 + g.rng.Float64()

	for _, v := range g.e.Variants {
		if v == g.e.Control {
			continue
		}
		lift := lifts[v] * spread
		if role == "guardrail" {
			lift = -math.Abs(lift) * 0.3
			if m.Direction == "decrease" {
				lift = -lift
			}
		}
		se := cv * math.Sqrt(2/g.n)
		value, control := round(base*(1+lift), 6), round(base, 6)
		// Scaled so p < alpha exactly when the (possibly widened) interval excludes zero.
		estimate := func(se float64) (low, high, p float64) {
			return round(lift-z*se, 6), round(lift+z*se, 6), round(twoSidedP(lift/se*zFixed/z), 6)
		}
		low, high, p := estimate(se)
		stat := ArmStat{Variant: v, Value: value, ControlValue: control, Lift: num(round(lift, 6))}

		if g.e.CUPED {
			// CUPED is the headline readout; the unadjusted one moves to raw.
			vr := 0.2 + g.rng.Float64()*0.3
			stat.Raw = &RawStat{Value: value, ControlValue: control, Lift: stat.Lift, CILow: num(low), CIHigh: num(high), PValue: num(p)}
			low, high, p = estimate(se * math.Sqrt(1-vr))
			stat.CUPED = &CUPEDStat{Value: value, Lift: stat.Lift, CILow: num(low), CIHigh: num(high), VarianceReduction: round(vr, 4)}
		}
		stat.CILow, stat.CIHigh, stat.PValue, stat.AdjustedP = num(low), num(high), num(p), num(p)
		stat.Significant = low > 0 || high < 0

		if role == "guardrail" {
			maxDrop := 0.0
			if m.MaxDropPct != nil {
				maxDrop = *m.MaxDropPct
			}
			worst := low
			if m.Direction == "decrease" {
				worst = -high
			}
			harm := stat.Significant && !good(m.Direction, stat.Lift)
			stat.Guardrail = &GuardrailCheck{MaxDropPct: num(maxDrop), Pass: worst*100 > -maxDrop, SignificantHarm: &harm}
		}
		res.Results = append(res.Results, stat)
	}
	return res
}

func (g sampleGen) timeseries(metrics []MetricResult) []TimePoint {
	out := []TimePoint{}
	for _, m := range metrics {
		if m.Role != "primary" {
			continue
		}
		for _, r := range m.Results {
			low, okLow := val(r.CILow)
			high, okHigh := val(r.CIHigh)
			center, okLift := val(r.Lift)
			if !okLow || !okHigh || !okLift {
				continue
			}
			half := (high - low) / 2
			for d := 1; d <= g.days; d++ {
				scale := math.Sqrt(float64(g.days) / float64(d))
				wobble := (g.rng.Float64() - 0.5) * half * scale * 0.6
				lift := center + wobble
				out = append(out, TimePoint{
					Date:    g.e.Start.AddDate(0, 0, d).Format(time.DateOnly),
					Metric:  m.Key,
					Variant: r.Variant,
					Lift:    round(lift, 6),
					CILow:   round(lift-half*scale, 6),
					CIHigh:  round(lift+half*scale, 6),
				})
			}
		}
	}
	return out
}

func baseline(format string, rng *rand.Rand) (value, cv float64) {
	switch format {
	case "percent":
		p := 0.05 + rng.Float64()*0.5
		return p, math.Sqrt(p*(1-p)) / p
	case "currency":
		return 0.5 + rng.Float64()*4.5, 2.5
	default:
		return 1 + rng.Float64()*99, 1.2
	}
}

func diagnostics(r Results, days int, e ExperimentSpec) []Diagnostic {
	out := []Diagnostic{}
	if r.SRM.Flag {
		out = append(out, Diagnostic{Check: "traffic_balance", Status: "fail", Detail: fmt.Sprintf("Sample ratio mismatch (p = %.4g).", r.SRM.PValue)})
	} else {
		out = append(out, Diagnostic{Check: "traffic_balance", Status: "pass", Detail: fmt.Sprintf("Traffic matches the planned split (p = %.2f).", r.SRM.PValue)})
	}
	if days < 7 {
		out = append(out, Diagnostic{Check: "full_week", Status: "warn", Detail: fmt.Sprintf("Only %d day(s) of data; wait for a full week to cover weekly cycles.", days)})
	} else {
		out = append(out, Diagnostic{Check: "full_week", Status: "pass", Detail: fmt.Sprintf("%d days of data cover at least one full week.", days)})
	}
	failing := 0
	for _, m := range r.Metrics {
		for _, a := range m.Results {
			if a.Guardrail != nil && !a.Guardrail.Pass {
				failing++
			}
		}
	}
	if failing > 0 {
		out = append(out, Diagnostic{Check: "guardrails", Status: "fail", Detail: fmt.Sprintf("%d guardrail check(s) cannot rule out a drop beyond tolerance.", failing)})
	} else if slices.ContainsFunc(e.Metrics, func(m MetricSpec) bool { return m.Role == "guardrail" }) {
		out = append(out, Diagnostic{Check: "guardrails", Status: "pass", Detail: "Every guardrail is within tolerance."})
	}
	if e.UnitType == "entity" {
		out = append(out, Diagnostic{Check: "unit_clustering", Status: "pass", Detail: "Randomized and analysed by entity; no clustering correction needed."})
	}
	out = append(out, Diagnostic{Check: "data_freshness", Status: "pass", Detail: "Sample data is generated on request."})
	return out
}

func seed(key string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	return h.Sum64()
}
