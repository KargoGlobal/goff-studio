package experiments

import "github.com/go-feature-flag/studio/pkg/analysis"

// Spec describes the experiment to an analysis provider, with each metric's
// catalog definition resolved. A metric missing from the catalog is passed by
// key alone.
func (e Experiment) Spec(catalog map[string]Metric) analysis.ExperimentSpec {
	spec := analysis.ExperimentSpec{
		Key:        e.Key,
		Control:    e.Control,
		Variants:   append([]string(nil), e.Variants...),
		UnitType:   e.Unit.Type,
		Start:      e.Start,
		End:        e.End,
		Test:       e.Analysis.Test,
		Alpha:      e.Analysis.Alpha,
		CUPED:      e.Analysis.CUPED,
		Correction: e.Analysis.Correction,
		Metrics:    []analysis.MetricSpec{},
		Segments:   append([]string{}, e.Segments...),
	}
	add := func(key, role string, maxDrop *float64) {
		m := catalog[key]
		spec.Metrics = append(spec.Metrics, analysis.MetricSpec{
			Key: key, Name: m.Name, Kind: m.Kind, Role: role, Direction: m.Direction, Format: m.Format,
			Numerator: m.Numerator, Denominator: m.Denominator, MaxDropPct: maxDrop,
		})
	}
	for _, key := range e.Metrics.Primary {
		add(key, "primary", nil)
	}
	for _, key := range e.Metrics.Secondary {
		add(key, "secondary", nil)
	}
	for _, g := range e.Metrics.Guardrails {
		add(g.Metric, "guardrail", &g.MaxDropPct)
	}
	return spec
}
