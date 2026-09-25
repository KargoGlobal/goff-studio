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

// ValidationError lists every problem found, so a form can show them all at once.
type ValidationError []string

func (v ValidationError) Error() string { return strings.Join(v, "; ") }

type problems []string

func (p *problems) addf(format string, args ...any) {
	*p = append(*p, fmt.Sprintf(format, args...))
}

func (p *problems) err() error {
	if len(*p) == 0 {
		return nil
	}
	return ValidationError(*p)
}

// ValidateExperiment checks the registry entry on its own, then against the
// flag it names (nil when the flag cannot be found) and the metric catalog.
func ValidateExperiment(e Experiment, flag *FlagShape, catalog map[string]Metric) error {
	var p problems

	checkIdentity(&p, e)
	checkArms(&p, e)
	checkWindow(&p, e)
	checkMetrics(&p, e, catalog)
	checkAnalysis(&p, e.Analysis)
	checkDecision(&p, e)

	if flag == nil {
		if e.Flag != "" && e.Environment != "" {
			p.addf("there is no flag %q in %s", e.Flag, e.Environment)
		}
		return p.err()
	}
	for _, v := range e.Variants {
		if !slices.Contains(flag.Variations, v) {
			p.addf("variant %q is not a variation of flag %s (it has %s)", v, e.Flag, strings.Join(flag.Variations, ", "))
		}
	}
	for _, a := range e.Allocations {
		if !slices.Contains(flag.Rules, a) {
			p.addf("allocation %q is not a rule on flag %s", a, e.Flag)
		}
	}
	return p.err()
}

func checkIdentity(p *problems, e Experiment) {
	switch {
	case e.Key == "":
		p.addf("an experiment needs a key")
	case !experimentKey.MatchString(e.Key):
		p.addf("key %q may only use lowercase letters, digits, dots and dashes", e.Key)
	}
	if strings.TrimSpace(e.Name) == "" {
		p.addf("an experiment needs a name")
	}
	if strings.TrimSpace(e.Owner) == "" {
		p.addf("an experiment needs an owning team")
	}
	if strings.TrimSpace(e.Hypothesis) == "" {
		p.addf("write down the hypothesis before the experiment starts")
	}
	if e.Flag == "" {
		p.addf("pick the flag this experiment runs on")
	}
	if e.Environment == "" {
		p.addf("pick the environment the flag lives in")
	}
	switch e.Status {
	case StatusDraft, StatusRunning, StatusStopped, StatusConcluded:
	default:
		p.addf("status must be draft, running, stopped or concluded, not %q", e.Status)
	}
}

func checkArms(p *problems, e Experiment) {
	if len(e.Allocations) == 0 {
		p.addf("pick at least one rule (allocation) on the flag")
	}
	if len(e.Variants) < 2 {
		p.addf("an experiment needs a control and at least one other variant")
	}
	seen := map[string]bool{}
	for _, v := range e.Variants {
		if seen[v] {
			p.addf("variant %q is listed twice", v)
		}
		seen[v] = true
	}
	if e.Control == "" {
		p.addf("pick the control variant")
	} else if !seen[e.Control] {
		p.addf("control %q must be one of the variants", e.Control)
	}
	switch e.Unit.Type {
	case "request", "entity":
	default:
		p.addf("unit type must be request or entity, not %q", e.Unit.Type)
	}
	if e.Unit.Key == "" {
		p.addf("the unit needs a key attribute, usually targetingKey")
	}
}

func checkWindow(p *problems, e Experiment) {
	if e.Start.IsZero() {
		p.addf("an experiment needs a start date")
	}
	if e.End.IsZero() {
		p.addf("an experiment needs an end date")
	}
	if e.Start.IsZero() || e.End.IsZero() {
		return
	}
	if !e.End.After(e.Start) {
		p.addf("the end date must be after the start date")
		return
	}
	if e.End.Sub(e.Start) > MaxDuration && !e.Extended {
		p.addf("experiments run for at most 8 weeks; end by %s or mark it extended", e.Start.Add(MaxDuration).Format(time.DateOnly))
	}
}

func checkMetrics(p *problems, e Experiment, catalog map[string]Metric) {
	if len(e.Metrics.Primary) == 0 {
		p.addf("pick at least one primary metric")
	}
	seen := map[string]bool{}
	for _, key := range e.MetricKeys() {
		if key == "" {
			p.addf("a metric entry is empty")
			continue
		}
		if seen[key] {
			p.addf("metric %q is used twice", key)
		}
		seen[key] = true
		if _, ok := catalog[key]; !ok {
			p.addf("metric %q is not in the metric catalog", key)
		}
	}
	for _, g := range e.Metrics.Guardrails {
		if g.MaxDropPct <= 0 || g.MaxDropPct > 100 {
			p.addf("guardrail %s needs a max drop between 0 and 100 percent", g.Metric)
		}
	}
}

func checkAnalysis(p *problems, a Analysis) {
	switch a.Test {
	case "sequential", "fixed":
	default:
		p.addf("analysis test must be sequential or fixed, not %q", a.Test)
	}
	if a.Alpha <= 0 || a.Alpha >= 0.5 {
		p.addf("alpha must be between 0 and 0.5, got %g", a.Alpha)
	}
	if a.Power <= 0 || a.Power >= 1 {
		p.addf("power must be between 0 and 1, got %g", a.Power)
	}
	switch a.Correction {
	case "none", "holm", "bh":
	default:
		p.addf("correction must be none, holm or bh, not %q", a.Correction)
	}
}

func checkDecision(p *problems, e Experiment) {
	if e.Decision == nil {
		return
	}
	switch e.Decision.Outcome {
	case "roll_out", "do_not_roll_out", "extend":
	default:
		p.addf("decision must be roll_out, do_not_roll_out or extend, not %q", e.Decision.Outcome)
	}
	if e.Decision.Variant != "" && !slices.Contains(e.Variants, e.Decision.Variant) {
		p.addf("the decision names %q, which is not a variant", e.Decision.Variant)
	}
}

func ValidateMetric(m Metric) error {
	var p problems
	switch {
	case m.Key == "":
		p.addf("a metric needs a key")
	case !metricKey.MatchString(m.Key):
		p.addf("key %q may only use lowercase letters, digits, underscores, dots and dashes", m.Key)
	}
	if strings.TrimSpace(m.Name) == "" {
		p.addf("a metric needs a name")
	}
	if strings.TrimSpace(m.Numerator) == "" {
		p.addf("a metric needs a numerator column")
	}
	switch m.Kind {
	case "mean":
		if m.Denominator != "" {
			p.addf("a mean metric has no denominator; use kind ratio")
		}
	case "ratio":
		if m.Denominator == "" {
			p.addf("a ratio metric needs a denominator column")
		}
	default:
		p.addf("kind must be mean or ratio, not %q", m.Kind)
	}
	switch m.Format {
	case "percent", "currency", "number":
	default:
		p.addf("format must be percent, currency or number, not %q", m.Format)
	}
	switch m.Direction {
	case "increase", "decrease":
	default:
		p.addf("direction must be increase or decrease, not %q", m.Direction)
	}
	if m.Cap != nil && (m.Cap.Pct <= 50 || m.Cap.Pct > 100) {
		p.addf("the winsorization cap must be a percentile above 50 and at most 100")
	}
	return p.err()
}
