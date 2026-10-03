package config

import (
	"testing"

	"github.com/go-feature-flag/studio/internal/permissions"
)

const sampleTeams = `teams:
  - name: growth
    editors: [marketing]
`

func withTeams(t *testing.T, block string) string {
	t.Helper()
	return replace(t, sampleTeams, block)
}

const twoTeams = sampleTeams + `  - name: payments
    editors: [payments-team, payments-oncall]
`

func TestTeamsExpandIntoOneRulePerEditor(t *testing.T) {
	cfg, err := Load(write(t, withTeams(t, twoTeams)))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.TeamNames(); len(got) != 2 || got[0] != "growth" || got[1] != "payments" {
		t.Fatalf("team names = %v", got)
	}

	set, err := permissions.New(cfg.Rules())
	if err != nil {
		t.Fatal(err)
	}
	if !set.Allowed(permissions.Request{Groups: []string{"payments-oncall"}, Environment: "dev", Team: "payments", Action: permissions.Delete}) {
		t.Error("every editor group of a team gets its access")
	}
	if set.Allowed(permissions.Request{Groups: []string{"payments-oncall"}, Environment: "dev", Team: "growth", Action: permissions.View}) {
		t.Error("an editor of one team must not reach another")
	}
	if !set.Allowed(permissions.Request{Groups: []string{"marketing"}, Environment: "production", Team: "growth", Action: permissions.Delete}) {
		t.Error("a team's editors get every action in every environment")
	}
}

func TestNoTeamsIsAllowed(t *testing.T) {
	cfg, err := Load(write(t, withTeams(t, "")))
	if err != nil {
		t.Fatalf("Studio must start without teams: %v", err)
	}
	if len(cfg.TeamNames()) != 0 {
		t.Errorf("teams = %v", cfg.TeamNames())
	}

	cfg, err = Load(write(t, withTeams(t, "")+"\n  - group: g\n    teams: [growth]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !warned(cfg, `"growth", which is not declared`) {
		t.Errorf("a named team with no teams declared must warn, got %v", cfg.Warnings())
	}
}

func TestARuleNamingAnUndeclaredTeamWarns(t *testing.T) {
	rule := "\n  - group: auditors\n    teams: [growth, payments]\n    actions: [view]\n"

	cfg, err := Load(write(t, base(t)+rule))
	if err != nil {
		t.Fatal(err)
	}
	if !warned(cfg, `"payments", which is not declared`) || warned(cfg, `"growth", which`) {
		t.Errorf("want a warning about payments only, got %v", cfg.Warnings())
	}

	cfg, err = Load(write(t, withTeams(t, twoTeams)+rule))
	if err != nil {
		t.Fatal(err)
	}
	if warned(cfg, "not declared") {
		t.Errorf("declared teams must not warn, got %v", cfg.Warnings())
	}
}

func TestBadTeamsAreRejected(t *testing.T) {
	cases := map[string][]string{
		"\nteams:\n  - name: growth\n  - name: growth\n":    {"teams[1].name", "repeats", "GOFF_STUDIO_TEAMS"},
		"\nteams:\n  - name: \"\"\n":                        {"teams[0].name"},
		"\nteams:\n  - name: has space\n":                   {"teams[0].name", "has space"},
		"\nteams:\n  - name: ../up\n":                       {"teams[0].name"},
		"\nteams:\n  - name: flags\n":                       {"teams[0].name", "reserved"},
		"\nteams:\n  - name: ok\n    editors: [\"\"]\n":     {"teams[0].editors[0]"},
		"\nteams:\n  - name: ok\n    actions: [view]\n":     {"actions", "permissions rule"},
		"\nteams:\n  - name: ok\n    environments: [dev]\n": {"environments", "permissions rule"},
	}
	for body, want := range cases {
		assertMentions(t, loadErr(t, withTeams(t, body)), want...)
	}
}

func TestTeamsFromTheEnvironment(t *testing.T) {
	t.Setenv("GOFF_STUDIO_TEAMS", `[{name: billing, editors: [billing-team]}]`)
	cfg, err := Load(write(t, withTeams(t, "")))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.DeclaresTeam("billing") {
		t.Errorf("teams = %v", cfg.TeamNames())
	}
}
