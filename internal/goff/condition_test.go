package goff

import "testing"

func TestCompileFlatConditions(t *testing.T) {
	tests := []struct {
		name string
		cond *Condition
		want string
	}{
		{
			name: "two attributes or'd",
			cond: Group("or", Leaf("property_a", "eq", "x"), Leaf("property_b", "eq", "y")),
			want: `(property_a eq "x") or (property_b eq "y")`,
		},
		{
			name: "and join",
			cond: Group("and", Leaf("tier", "eq", "gold"), Leaf("country", "eq", "US")),
			want: `(tier eq "gold") and (country eq "US")`,
		},
		{
			name: "numeric stays unquoted",
			cond: Group("and", Leaf("age", "gt", "21")),
			want: `(age gt 21)`,
		},
		{
			name: "boolean unquoted",
			cond: Group("and", Leaf("active", "eq", "true")),
			want: `(active eq true)`,
		},
		{
			name: "is present takes no value",
			cond: Group("and", Leaf("email", "pr", "ignored")),
			want: `(email pr)`,
		},
		{
			name: "is one of builds a list",
			cond: Group("and", Leaf("tier", "in", "gold, silver , bronze")),
			want: `(tier in ["gold", "silver", "bronze"])`,
		},
		{
			name: "quotes escaped",
			cond: Group("and", Leaf("name", "eq", `he said "hi"`)),
			want: `(name eq "he said \"hi\"")`,
		},
		{
			name: "single leaf without a group",
			cond: Leaf("tier", "eq", "gold"),
			want: `(tier eq "gold")`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CompileCondition(tt.cond); got != tt.want {
				t.Errorf("CompileCondition()\ngot:  %s\nwant: %s", got, tt.want)
			}
		})
	}
}

func TestCompileNestedGroups(t *testing.T) {
	cond := Group("and",
		Group("or", Leaf("tier", "eq", "gold"), Leaf("tier", "eq", "platinum")),
		Group("or", Leaf("region", "eq", "us"), Leaf("region", "eq", "eu")),
	)

	want := `((tier eq "gold") or (tier eq "platinum")) and ((region eq "us") or (region eq "eu"))`
	if got := CompileCondition(cond); got != want {
		t.Errorf("nested groups\ngot:  %s\nwant: %s", got, want)
	}
}

func TestRoundTripIncludingNesting(t *testing.T) {
	queries := []string{
		`(property_a eq "x") or (property_b eq "y")`,
		`(tier eq "gold") and (country eq "US")`,
		`(age gt 21)`,
		`(active eq true)`,
		`(email pr)`,
		`(tier in ["gold", "silver", "bronze"])`,
		`(name eq "he said \"hi\"")`,
		`(account_id eq "42")`,
		`((tier eq "gold") or (tier eq "platinum")) and ((region eq "us") or (region eq "eu"))`,
		`((a eq "1") and (b eq "2")) or (c eq "3")`,
		`(not (tier eq "gold"))`,
		`(not ((tier eq "gold") and (plan eq "pro")))`,
		`(country eq "US") and (not (tier eq "gold"))`,
		`(not (tier eq "gold")) or (plan eq "pro")`,
		`(not (tier in ["gold", "silver"]))`,
	}

	for _, q := range queries {
		t.Run(q, func(t *testing.T) {
			parsed := ParseQuery(q)
			if !parsed.Simple {
				t.Fatalf("ParseQuery(%q) fell back to advanced", q)
			}
			if got := CompileCondition(parsed.Condition); got != q {
				t.Errorf("round trip\norig: %s\ngot:  %s", q, got)
			}
		})
	}
}

func TestMixedJoinsAtSameLevelBecomeAdvanced(t *testing.T) {
	notSimple := []string{
		`(a eq "x") and (b eq "y") or (c eq "z")`,
		`weird ?? "x"`,
		`a eq`,
		`(a eq "x") or`,
		`(unclosed eq "x"`,
	}

	for _, q := range notSimple {
		t.Run(q, func(t *testing.T) {
			if ParseQuery(q).Simple {
				t.Errorf("ParseQuery(%q) should be advanced", q)
			}
		})
	}
}

func TestParseQueryWithoutParens(t *testing.T) {
	parsed := ParseQuery(`property_a eq "x" or property_b eq "y"`)
	if !parsed.Simple {
		t.Fatal("expected simple")
	}
	if parsed.Condition.Op != "or" || len(parsed.Condition.Children) != 2 {
		t.Fatalf("condition = %+v", parsed.Condition)
	}
	if parsed.Condition.Children[0].Attribute != "property_a" {
		t.Errorf("first child = %+v", parsed.Condition.Children[0])
	}
}

func TestEmptyQueryIsSimpleAndEmpty(t *testing.T) {
	parsed := ParseQuery("")
	if !parsed.Simple || parsed.Condition != nil {
		t.Errorf("empty query = %+v", parsed)
	}
}

func TestCompileSkipsUnknownOperator(t *testing.T) {
	if got := CompileCondition(Group("and", Leaf("a", "bogus", "x"))); got != "" {
		t.Errorf("unknown operator should be skipped, got %q", got)
	}
}

func TestCompileSkipsBlankAttributes(t *testing.T) {
	cond := Group("or", Leaf("", "eq", "x"), Leaf("b", "eq", "y"))
	if got := CompileCondition(cond); got != `(b eq "y")` {
		t.Errorf("got %q", got)
	}
}

func TestFrontendBackendSerializersAgree(t *testing.T) {
	cases := []struct {
		cond *Condition
		want string
	}{
		{Group("or", Leaf("property_a", "eq", "x"), Leaf("property_b", "eq", "y")), `(property_a eq "x") or (property_b eq "y")`},
		{Group("and", Leaf("tier", "eq", "gold"), Leaf("country", "eq", "US")), `(tier eq "gold") and (country eq "US")`},
		{Group("and", Leaf("age", "gt", "21")), `(age gt 21)`},
		{Group("and", Leaf("age", "gt", "0")), `(age gt 0)`},
		{Group("and", Leaf("account_id", "eq", "42")), `(account_id eq "42")`},
		{Group("and", Leaf("active", "eq", "true")), `(active eq true)`},
		{Group("and", Leaf("active", "eq", "false")), `(active eq false)`},
		{Group("and", Leaf("email", "pr", "")), `(email pr)`},
		{Group("and", Leaf("tier", "in", "gold, silver , bronze")), `(tier in ["gold", "silver", "bronze"])`},
		{Group("and", Leaf("name", "eq", `he said "hi"`)), `(name eq "he said \"hi\"")`},
		{Group("and", Leaf("a", "eq", "b")), `(a eq "b")`},
		{
			Group("and",
				Group("or", Leaf("tier", "eq", "gold"), Leaf("tier", "eq", "platinum")),
				Group("or", Leaf("region", "eq", "us"), Leaf("region", "eq", "eu")),
			),
			`((tier eq "gold") or (tier eq "platinum")) and ((region eq "us") or (region eq "eu"))`,
		},
		{
			Group("and",
				Group("or", Leaf("tier", "eq", "gold"), Leaf("tier", "eq", "platinum")),
				Leaf("region", "eq", "us"),
			),
			`((tier eq "gold") or (tier eq "platinum")) and (region eq "us")`,
		},
	}

	for _, c := range cases {
		got := CompileCondition(c.cond)
		if got != c.want {
			t.Errorf("Go serializer disagrees with the TS one (web/src/lib/query.test.ts)\ngot:  %s\nwant: %s", got, c.want)
		}
		if parsed := ParseQuery(got); !parsed.Simple {
			t.Errorf("%s does not parse back into the builder", got)
		}
	}
}

func TestIsOneOfRoundTripsBothWays(t *testing.T) {
	cases := []struct {
		cond *Condition
		want string
	}{
		{Group("and", Leaf("tier", "in", "gold")), `(tier eq "gold")`},
		{Group("and", Leaf("tier", "in", "gold, silver")), `(tier in ["gold", "silver"])`},
		{Group("and", Leaf("tier", "notin", "gold")), `(tier ne "gold")`},
		{Group("and", Leaf("tier", "notin", "gold, silver")), `(not (tier in ["gold", "silver"]))`},
		{Group("and", Leaf("account_id", "in", "42")), `(account_id eq "42")`},
		{
			Group("or", Leaf("property_a", "in", "x"), Leaf("property_b", "in", "y")),
			`(property_a eq "x") or (property_b eq "y")`,
		},
	}

	for _, c := range cases {
		got := CompileCondition(c.cond)
		if got != c.want {
			t.Errorf("CompileCondition\ngot:  %s\nwant: %s", got, c.want)
			continue
		}

		parsed := ParseQuery(got)
		if !parsed.Simple {
			t.Errorf("%s must stay editable in the builder", got)
			continue
		}
		if again := CompileCondition(parsed.Condition); again != got {
			t.Errorf("round trip changed the query\nfirst:  %s\nsecond: %s", got, again)
		}
	}
}

func TestParserKeepsFileOperatorsFaithful(t *testing.T) {
	for query, wantOp := range map[string]string{
		`(tier eq "gold")`:                   "eq",
		`(tier ne "gold")`:                   "ne",
		`(tier in ["gold", "silver"])`:       "in",
		`(not (tier in ["gold", "silver"]))`: "notin",
	} {
		parsed := ParseQuery(query)
		if !parsed.Simple || parsed.Condition == nil {
			t.Fatalf("%s did not parse", query)
		}

		leaf := parsed.Condition
		if leaf.IsGroup() {
			leaf = leaf.Children[0]
		}
		if leaf.Operator != wantOp {
			t.Errorf("%s parsed as %q, want %q; the frontend maps eq/ne onto its is-one-of control, the parser stays faithful to the file", query, leaf.Operator, wantOp)
		}
	}
}

func TestNegatedGroupsCompile(t *testing.T) {
	cases := map[string]*Condition{
		`(not (tier eq "gold"))`: NotGroup("and", Leaf("tier", "eq", "gold")),
		`(not ((tier eq "gold") and (plan eq "pro")))`: NotGroup("and",
			Leaf("tier", "eq", "gold"), Leaf("plan", "eq", "pro")),
		`(country eq "US") and (not (tier eq "gold"))`: Group("and",
			Leaf("country", "eq", "US"), NotGroup("and", Leaf("tier", "eq", "gold"))),
	}

	for want, cond := range cases {
		if got := CompileCondition(cond); got != want {
			t.Errorf("negated group\ngot:  %s\nwant: %s", got, want)
		}
	}
}

func TestNotBindsTighterThanCombinators(t *testing.T) {
	// Without the inner parens this would read as "not a, and b", which is a different rule.
	cond := NotGroup("or", Leaf("a", "eq", "1"), Leaf("b", "eq", "2"))
	want := `(not ((a eq "1") or (b eq "2")))`
	if got := CompileCondition(cond); got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestNegatedInListStaysANotinLeaf(t *testing.T) {
	parsed := ParseQuery(`(not (tier in ["gold", "silver"]))`)
	if !parsed.Simple {
		t.Fatal("a negated in-list must stay simple")
	}
	if parsed.Condition.Not {
		t.Error("it should round-trip as a notin leaf, not a negated group")
	}
	if parsed.Condition.Operator != "notin" {
		t.Errorf("operator = %q, want notin", parsed.Condition.Operator)
	}
	if parsed.Condition.Value != "gold, silver" {
		t.Errorf("value = %q", parsed.Condition.Value)
	}
}
