package experiments

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

var (
	experimentKey = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]*$`)
	metricKey     = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]*$`)
)

// FlagShape is the part of a flag an experiment is checked against.
type FlagShape struct {
	Variations []string
	Rules      []string
}

type Problems []string

func (p Problems) Error() string { return strings.Join(p, "; ") }

func (p *Problems) add(format string, args ...any) {
	*p = append(*p, fmt.Sprintf(format, args...))
}

func (p Problems) Err() error {
	if len(p) == 0 {
		return nil
	}
	return p
}

// ValidateExperiment checks the registry entry on its own, then against the
// flag it names (nil when the flag cannot be found) and the metric catalog.
func ValidateExperiment(e Experiment, flag *FlagShape, catalog map[string]Metric) error {
	var p Problems

	checkIdentity(&p, e)
	checkArms(&p, e)
	checkWindow(&p, e)
	checkMetrics(&p, e, catalog)
	checkAnalysis(&p, e.Analysis)
	checkDecision(&p, e)

	if flag == nil {
		if e.Flag != "" && e.Environment != "" {
			p.add("there is no flag %q in %s", e.Flag, e.Environment)
		}
		return p.Err()
	}
	for _, v := range e.Variants {
		if !slices.Contains(flag.Variations, v) {
			p.add("variant %q is not a variation of flag %s (it has %s)", v, e.Flag, strings.Join(flag.Variations, ", "))
		}
	}
	for _, a := range e.Allocations {
		if !slices.Contains(flag.Rules, a) {
			p.add("allocation %q is not a rule on flag %s", a, e.Flag)
		}
	}
	return p.Err()
}

func checkIdentity(p *Problems, e Experiment) {
	switch {
	case e.Key == "":
		p.add("an experiment needs a key")
	case !experimentKey.MatchString(e.Key):
		p.add("key %q may only use lowercase letters, digits, dots and dashes", e.Key)
	}
	if strings.TrimSpace(e.Name) == "" {
		p.add("an experiment needs a name")
	}
	if strings.TrimSpace(e.Owner) == "" {
		p.add("an experiment needs an owning team")
	}
	if strings.TrimSpace(e.Hypothesis) == "" {
		p.add("write down the hypothesis before the experiment starts")
	}
	if e.Flag == "" {
		p.add("pick the flag this experiment runs on")
	}
	if e.Environment == "" {
		p.add("pick the environment the flag lives in")
	}
	switch e.Status {
	case StatusDraft, StatusRunning, StatusStopped, StatusConcluded:
	default:
		p.add("status must be draft, running, stopped or concluded, not %q", e.Status)
	}
}

func checkArms(p *Problems, e Experiment) {
	if len(e.Allocations) == 0 {
		p.add("pick at least one rule (allocation) on the flag")
	}
	if len(e.Variants) < 2 {
		p.add("an experiment needs a control and at least one other variant")
	}
	seen := map[string]bool{}
	for _, v := range e.Variants {
		if seen[v] {
			p.add("variant %q is listed twice", v)
		}
		seen[v] = true
	}
	if e.Control == "" {
		p.add("pick the control variant")
	} else if !seen[e.Control] {
		p.add("control %q must be one of the variants", e.Control)
	}
	switch e.Unit.Type {
	case "request", "entity":
	default:
		p.add("unit type must be request or entity, not %q", e.Unit.Type)
	}
	if e.Unit.Key == "" {
		p.add("the unit needs a key attribute, usually targetingKey")
	}
}

func checkWindow(p *Problems, e Experiment) {
	if e.Start.IsZero() {
		p.add("an experiment needs a start date")
	}
	if e.End.IsZero() {
		p.add("an experiment needs an end date")
	}
	if e.Start.IsZero() || e.End.IsZero() {
		return
	}
	if !e.End.After(e.Start) {
		p.add("the end date must be after the start date")
		return
	}
	if e.End.Sub(e.Start) > MaxDuration && !e.Extended {
		p.add("experiments run for at most 8 weeks; end by %s or mark it extended", e.Start.Add(MaxDuration).Format(time.DateOnly))
	}
}

func checkMetrics(p *Problems, e Experiment, catalog map[string]Metric) {
	if len(e.Metrics.Primary) == 0 {
		p.add("pick at least one primary metric")
	}
	seen := map[string]bool{}
	for _, key := range e.MetricKeys() {
		if key == "" {
			p.add("a metric entry is empty")
			continue
		}
		if seen[key] {
			p.add("metric %q is used twice", key)
		}
		seen[key] = true
		if _, ok := catalog[key]; !ok {
			p.add("metric %q is not in the metric catalog", key)
		}
	}
	for _, g := range e.Metrics.Guardrails {
		if g.MaxDropPct <= 0 || g.MaxDropPct > 100 {
			p.add("guardrail %s needs a max drop between 0 and 100 percent", g.Metric)
		}
	}
}

func checkAnalysis(p *Problems, a Analysis) {
	switch a.Test {
	case "sequential", "fixed":
	default:
		p.add("analysis test must be sequential or fixed, not %q", a.Test)
	}
	if a.Alpha <= 0 || a.Alpha >= 0.5 {
		p.add("alpha must be between 0 and 0.5, got %g", a.Alpha)
	}
	if a.Power <= 0 || a.Power >= 1 {
		p.add("power must be between 0 and 1, got %g", a.Power)
	}
	switch a.Correction {
	case "none", "holm", "bh":
	default:
		p.add("correction must be none, holm or bh, not %q", a.Correction)
	}
}

func checkDecision(p *Problems, e Experiment) {
	if e.Decision == nil {
		return
	}
	switch e.Decision.Outcome {
	case "roll_out", "do_not_roll_out", "extend":
	default:
		p.add("decision must be roll_out, do_not_roll_out or extend, not %q", e.Decision.Outcome)
	}
	if e.Decision.Variant != "" && !slices.Contains(e.Variants, e.Decision.Variant) {
		p.add("the decision names %q, which is not a variant", e.Decision.Variant)
	}
}

func ValidateMetric(m Metric) error {
	var p Problems
	switch {
	case m.Key == "":
		p.add("a metric needs a key")
	case !metricKey.MatchString(m.Key):
		p.add("key %q may only use lowercase letters, digits, underscores, dots and dashes", m.Key)
	}
	if strings.TrimSpace(m.Name) == "" {
		p.add("a metric needs a name")
	}
	if strings.TrimSpace(m.Numerator) == "" {
		p.add("a metric needs a numerator column")
	}
	switch m.Kind {
	case "mean":
		if m.Denominator != "" {
			p.add("a mean metric has no denominator; use kind ratio")
		}
	case "ratio":
		if m.Denominator == "" {
			p.add("a ratio metric needs a denominator column")
		}
	default:
		p.add("kind must be mean or ratio, not %q", m.Kind)
	}
	switch m.Format {
	case "percent", "currency", "number":
	default:
		p.add("format must be percent, currency or number, not %q", m.Format)
	}
	switch m.Direction {
	case "increase", "decrease":
	default:
		p.add("direction must be increase or decrease, not %q", m.Direction)
	}
	if m.Cap != nil && (m.Cap.Pct <= 50 || m.Cap.Pct > 100) {
		p.add("the winsorization cap must be a percentile above 50 and at most 100")
	}
	return p.Err()
}
