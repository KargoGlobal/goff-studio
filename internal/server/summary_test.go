package server

import (
	"strings"
	"testing"

	"github.com/go-feature-flag/studio/internal/goff"
)

func TestSummarizeSpeaksPlainEnglish(t *testing.T) {
	tests := []struct {
		name string
		flag goff.Flag
		want string
	}{
		{
			name: "disabled flag",
			flag: goff.Flag{Enabled: false},
			want: "Off for everyone",
		},
		{
			name: "no rules",
			flag: goff.Flag{Enabled: true, Default: goff.Outcome{Variation: "off"}},
			want: "Off for everyone",
		},
		{
			name: "single condition to a variation",
			flag: goff.Flag{
				Enabled: true,
				Rules: []goff.Rule{{
					Condition: goff.Group("and", goff.Leaf("plan", "eq", "pro")),
					Outcome:   goff.Outcome{Variation: "on"},
				}},
				Default: goff.Outcome{Variation: "off"},
			},
			want: "Users where plan equals pro get on; everyone else gets off",
		},
		{
			name: "or across two attributes",
			flag: goff.Flag{
				Enabled: true,
				Rules: []goff.Rule{{
					Condition: goff.Group("or", goff.Leaf("plan", "eq", "pro"), goff.Leaf("plan", "eq", "enterprise")),
					Outcome:   goff.Outcome{Variation: "on"},
				}},
				Default: goff.Outcome{Variation: "off"},
			},
			want: "Users where plan equals pro or plan equals enterprise get on; everyone else gets off",
		},
		{
			name: "percentage split",
			flag: goff.Flag{
				Enabled: true,
				Rules: []goff.Rule{{
					Condition: goff.Group("and", goff.Leaf("tier", "eq", "gold")),
					Outcome:   goff.Outcome{Percentage: map[string]float64{"on": 20, "off": 80}},
				}},
				Default: goff.Outcome{Variation: "off"},
			},
			want: "Users where tier equals gold get 80% off / 20% on; everyone else gets off",
		},
		{
			name: "is one of reads as a list",
			flag: goff.Flag{
				Enabled: true,
				Rules: []goff.Rule{{
					Condition: goff.Group("and", goff.Leaf("plan", "in", "pro, enterprise, trial")),
					Outcome:   goff.Outcome{Variation: "on"},
				}},
				Default: goff.Outcome{Variation: "off"},
			},
			want: "Users where plan is one of pro, enterprise or trial get on; everyone else gets off",
		},
		{
			name: "attribute is set",
			flag: goff.Flag{
				Enabled: true,
				Rules: []goff.Rule{{
					Condition: goff.Group("and", goff.Leaf("email", "pr", "")),
					Outcome:   goff.Outcome{Variation: "on"},
				}},
				Default: goff.Outcome{Variation: "off"},
			},
			want: "Users where email is present get on; everyone else gets off",
		},
		{
			name: "advanced rule is not rendered as syntax",
			flag: goff.Flag{
				Enabled: true,
				Rules: []goff.Rule{{
					Advanced: true,
					Query:    `weird ?? "thing"`,
					Outcome:  goff.Outcome{Variation: "on"},
				}},
				Default: goff.Outcome{Variation: "off"},
			},
			want: "Users matching a custom rule get on; everyone else gets off",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Summarize(tt.flag); got != tt.want {
				t.Errorf("Summarize()\ngot:  %s\nwant: %s", got, tt.want)
			}
		})
	}
}

func TestSummaryNeverLeaksQuerySyntax(t *testing.T) {
	flag := goff.Flag{
		Enabled: true,
		Rules: []goff.Rule{{
			Condition: goff.Group("or",
				goff.Leaf("tier", "eq", "gold"),
				goff.Leaf("account_id", "eq", "42"),
			),
			Outcome: goff.Outcome{Percentage: map[string]float64{"on": 10, "off": 90}},
		}},
		Default: goff.Outcome{Variation: "off"},
	}

	got := Summarize(flag)
	for _, forbidden := range []string{" eq ", "(", ")", `"`} {
		if strings.Contains(got, forbidden) {
			t.Errorf("summary leaked query syntax %q: %s", forbidden, got)
		}
	}
}

func TestDisabledRulesAreSkipped(t *testing.T) {
	flag := goff.Flag{
		Enabled: true,
		Rules: []goff.Rule{
			{Disabled: true, Condition: goff.Group("and", goff.Leaf("a", "eq", "b")), Outcome: goff.Outcome{Variation: "on"}},
			{Condition: goff.Group("and", goff.Leaf("c", "eq", "d")), Outcome: goff.Outcome{Variation: "on"}},
		},
		Default: goff.Outcome{Variation: "off"},
	}

	got := Summarize(flag)
	if strings.Contains(got, "a equals b") {
		t.Errorf("a disabled rule should not appear: %s", got)
	}
	if !strings.Contains(got, "c equals d") {
		t.Errorf("the active rule should appear: %s", got)
	}
}

func TestManyConditionsAreTruncated(t *testing.T) {
	flag := goff.Flag{
		Enabled: true,
		Rules: []goff.Rule{{
			Condition: goff.Group("or",
				goff.Leaf("a", "eq", "1"),
				goff.Leaf("b", "eq", "2"),
				goff.Leaf("c", "eq", "3"),
				goff.Leaf("d", "eq", "4"),
				goff.Leaf("e", "eq", "5"),
			),
			Outcome: goff.Outcome{Variation: "on"},
		}},
		Default: goff.Outcome{Variation: "off"},
	}

	got := Summarize(flag)
	if !strings.Contains(got, "2 more conditions") {
		t.Errorf("long conditions should be truncated for readability: %s", got)
	}
}
