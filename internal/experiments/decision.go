package experiments

import (
	"fmt"
	"time"
)

// good reports whether a lift moves the metric the way its owner wants.
func good(direction string, lift float64) bool {
	if direction == "decrease" {
		return lift < 0
	}
	return lift > 0
}

// harmful reports whether the whole interval sits on the wrong side of zero.
func harmful(direction string, a ArmStat) bool {
	if !a.Significant {
		return false
	}
	if direction == "decrease" {
		return a.CILow > 0
	}
	return a.CIHigh < 0
}

// Decide applies the roll-out rule: a significant win on the primary metric
// ships unless a guardrail is significantly hurt (do not roll out) or cannot
// rule out a drop beyond its tolerance (discuss). Without a win, keep running
// until the planned end, then do not roll out.
func Decide(metrics []MetricResult, end, now time.Time) Recommendation {
	var (
		primary *MetricResult
		best    *ArmStat
	)
	for i := range metrics {
		if metrics[i].Role == "primary" {
			primary = &metrics[i]
			break
		}
	}
	if primary != nil {
		for i := range primary.Results {
			r := &primary.Results[i]
			if !r.Significant || !good(primary.Direction, r.Lift) {
				continue
			}
			if best == nil || abs(r.Lift) > abs(best.Lift) {
				best = r
			}
		}
	}

	if best == nil {
		if !now.Before(end) {
			return Recommendation{
				Recommendation: "do_not_roll_out",
				Reason:         "The planned end has passed without a significant improvement on the primary metric.",
			}
		}
		return Recommendation{
			Recommendation: "keep_running",
			Reason:         "No variant has a significant improvement on the primary metric yet.",
		}
	}

	var failing []string
	for _, m := range metrics {
		if m.Role != "guardrail" {
			continue
		}
		for _, r := range m.Results {
			if r.Variant != best.Variant {
				continue
			}
			if harmful(m.Direction, r) {
				return Recommendation{
					Recommendation: "do_not_roll_out",
					Variant:        best.Variant,
					Reason:         fmt.Sprintf("%s improves %s, but significantly hurts the %s guardrail.", best.Variant, primary.Name, m.Name),
				}
			}
			if r.Guardrail != nil && !r.Guardrail.Pass {
				failing = append(failing, m.Name)
			}
		}
	}
	if len(failing) > 0 {
		return Recommendation{
			Recommendation: "discuss",
			Variant:        best.Variant,
			Reason:         fmt.Sprintf("%s improves %s, but cannot rule out a drop beyond tolerance on %s.", best.Variant, primary.Name, joinNames(failing)),
		}
	}
	return Recommendation{
		Recommendation: "roll_out",
		Variant:        best.Variant,
		Reason:         fmt.Sprintf("%s significantly improves %s and every guardrail passes.", best.Variant, primary.Name),
	}
}

func joinNames(names []string) string {
	out := ""
	for i, n := range names {
		switch {
		case i == 0:
			out = n
		case i == len(names)-1:
			out += " and " + n
		default:
			out += ", " + n
		}
	}
	return out
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
