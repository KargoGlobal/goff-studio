package server

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/go-feature-flag/studio/internal/goff"
	"github.com/go-feature-flag/studio/pkg/splits"
)

func Summarize(f goff.Flag) string {
	if !f.Enabled {
		return "Off for everyone"
	}

	var parts []string
	for _, r := range f.Rules {
		if r.Disabled {
			continue
		}
		if described, ok := describeExperimentRule(f, r); ok {
			parts = append(parts, described)
			continue
		}
		parts = append(parts, describeRule(r))
	}

	fallback := "everyone else gets " + describeOutcome(f.Default)
	if len(parts) == 0 {
		return capitalize(describeOutcome(f.Default) + " for everyone")
	}

	return capitalize(strings.Join(parts, "; ") + "; " + fallback)
}

func describeRule(r goff.Rule) string {
	return fmt.Sprintf("%s get %s", describeWho(r), describeOutcome(r.Outcome))
}

func describeWho(r goff.Rule) string {
	who := "users"
	if r.Advanced {
		who = "users matching a custom rule"
	} else if r.Condition != nil {
		if described := describeCondition(r.Condition); described != "" {
			who = "users where " + described
		}
	}
	return who
}

func describeExperimentRule(f goff.Flag, r goff.Rule) (string, bool) {
	if f.Experiment == nil {
		return "", false
	}
	a := f.Experiment.Allocations[r.Name]
	if a == nil {
		return "", false
	}
	exposed := 0.0
	for _, share := range a.Shares(f.Experiment.TotalShards) {
		exposed += share
	}
	rest := "the rest continue to the next rule"
	if !a.PassesThrough() {
		rest = "the rest get " + describeOutcome(r.Outcome)
	}
	return fmt.Sprintf("%s enter experiment %s (%s%% exposed; %s)",
		describeWho(r), a.KeyFor(f.Key, r.Name), splits.FormatPercent(exposed*100), rest), true
}

func describeCondition(c *goff.Condition) string {
	if c == nil {
		return ""
	}

	if c.IsGroup() {
		parts := make([]string, 0, len(c.Children))
		for _, child := range c.Children {
			if described := describeCondition(child); described != "" {
				parts = append(parts, described)
			}
		}
		if len(parts) == 0 {
			return ""
		}
		if len(parts) == 1 {
			return parts[0]
		}
		joiner := " and "
		if c.Op == "or" {
			joiner = " or "
		}
		if len(parts) > 3 {
			return strings.Join(parts[:3], joiner) + fmt.Sprintf(" or %d more conditions", len(parts)-3)
		}
		return strings.Join(parts, joiner)
	}

	op, ok := goff.LookupOperator(c.Operator)
	if !ok {
		return ""
	}
	if op.Arity == 0 {
		return fmt.Sprintf("%s %s", c.Attribute, op.Label)
	}
	return fmt.Sprintf("%s %s %s", c.Attribute, op.Label, quoteForHumans(c.Value))
}

func describeOutcome(o goff.Outcome) string {
	if len(o.Percentage) > 0 {
		names := make([]string, 0, len(o.Percentage))
		for name := range o.Percentage {
			names = append(names, name)
		}
		sort.Slice(names, func(i, j int) bool {
			if o.Percentage[names[i]] != o.Percentage[names[j]] {
				return o.Percentage[names[i]] > o.Percentage[names[j]]
			}
			return names[i] < names[j]
		})

		parts := make([]string, 0, len(names))
		for _, name := range names {
			parts = append(parts, fmt.Sprintf("%s%% %s", trimFloat(o.Percentage[name]), name))
		}
		return strings.Join(parts, " / ")
	}
	if o.Variation == "" {
		return "no variation"
	}
	return o.Variation
}

func quoteForHumans(v string) string {
	if strings.Contains(v, ",") {
		items := strings.Split(v, ",")
		for i := range items {
			items[i] = strings.TrimSpace(items[i])
		}
		if len(items) > 1 {
			return strings.Join(items[:len(items)-1], ", ") + " or " + items[len(items)-1]
		}
	}
	return v
}

func trimFloat(f float64) string {
	s := strconv.FormatFloat(f, 'f', 2, 64)
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
