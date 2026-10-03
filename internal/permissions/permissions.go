package permissions

import (
	"fmt"
	"strings"
)

type Action string

const (
	View           Action = "view"
	Toggle         Action = "toggle"
	Rollout        Action = "rollout"
	EditRules      Action = "edit_rules"
	EditVariations Action = "edit_variations"
	Create         Action = "create"
	Delete         Action = "delete"
)

var AllActions = []Action{View, Toggle, Rollout, EditRules, EditVariations, Create, Delete}

func ParseAction(raw string) (Action, error) {
	candidate := Action(strings.TrimSpace(strings.ToLower(raw)))
	for _, a := range AllActions {
		if a == candidate {
			return a, nil
		}
	}
	return "", fmt.Errorf("unknown action %q", raw)
}

type Rule struct {
	Group        string   `yaml:"group"`
	Teams        []string `yaml:"teams"`
	Environments []string `yaml:"environments"`
	Actions      []string `yaml:"actions"`
}

type Set struct {
	rules []Rule
}

func New(rules []Rule) (*Set, error) {
	for i, r := range rules {
		if strings.TrimSpace(r.Group) == "" {
			return nil, fmt.Errorf("permission rule %d has no group", i)
		}
		if len(r.Teams) == 0 {
			return nil, fmt.Errorf("permission rule for group %q has no teams; list the teams it covers, or [\"*\"] for all", r.Group)
		}
		for _, t := range r.Teams {
			if t = strings.TrimSpace(t); t != "*" && strings.ContainsAny(t, "*?[]/\\") {
				return nil, fmt.Errorf("permission rule for group %q lists %q; name a team or use \"*\" for all teams", r.Group, t)
			}
		}
		for _, a := range r.Actions {
			if _, err := ParseAction(a); err != nil {
				return nil, fmt.Errorf("permission rule for group %q: %w", r.Group, err)
			}
		}
	}
	return &Set{rules: rules}, nil
}

type Request struct {
	Groups      []string
	Environment string
	Team        string
	Action      Action
}

func (s *Set) Allowed(req Request) bool {
	if s == nil || len(s.rules) == 0 {
		return false
	}
	if req.Action == "" {
		return false
	}

	for _, rule := range s.rules {
		if !hasGroup(req.Groups, rule.Group) {
			continue
		}
		if !environmentMatches(rule, req.Environment) {
			continue
		}
		if !actionMatches(rule, req.Action) {
			continue
		}
		if !teamMatches(rule.Teams, req.Team) {
			continue
		}
		return true
	}
	return false
}

func (s *Set) ActionsFor(groups []string, environment, team string) []Action {
	var out []Action
	for _, a := range AllActions {
		if s.Allowed(Request{Groups: groups, Environment: environment, Team: team, Action: a}) {
			out = append(out, a)
		}
	}
	return out
}

func (s *Set) VisibleEnvironments(groups []string, environments []string) []string {
	var out []string
	for _, env := range environments {
		if s.AllowedAnywhere(groups, env, View) {
			out = append(out, env)
		}
	}
	return out
}

func (s *Set) AllowedAnywhere(groups []string, environment string, action Action) bool {
	for _, rule := range s.rules {
		if !hasGroup(groups, rule.Group) {
			continue
		}
		if !environmentMatches(rule, environment) {
			continue
		}
		if !actionMatches(rule, action) {
			continue
		}
		return true
	}
	return false
}

type Grant struct {
	Group    string
	AllTeams bool
	Writes   bool
}

func (s *Set) GrantsFor(environment, team string) []Grant {
	if s == nil {
		return nil
	}
	var out []Grant
	index := map[string]int{}
	for _, rule := range s.rules {
		if !environmentMatches(rule, environment) || !teamMatches(rule.Teams, team) {
			continue
		}
		grant := Grant{Group: rule.Group, AllTeams: coversAllTeams(rule.Teams), Writes: grantsWrites(rule)}
		at, seen := index[rule.Group]
		if !seen {
			index[rule.Group] = len(out)
			out = append(out, grant)
			continue
		}
		out[at].AllTeams = out[at].AllTeams || grant.AllTeams
		out[at].Writes = out[at].Writes || grant.Writes
	}
	return out
}

func coversAllTeams(patterns []string) bool {
	for _, p := range patterns {
		if strings.TrimSpace(p) == "*" {
			return true
		}
	}
	return false
}

func grantsWrites(rule Rule) bool {
	for _, a := range AllActions {
		if a != View && actionMatches(rule, a) {
			return true
		}
	}
	return false
}

func (s *Set) CanCreateEnvironments(groups []string) bool {
	if s == nil {
		return false
	}
	for _, rule := range s.rules {
		if hasGroup(groups, rule.Group) && environmentMatches(rule, "*") && actionMatches(rule, Create) {
			return true
		}
	}
	return false
}

func hasGroup(groups []string, want string) bool {
	if want == "*" {
		return true
	}
	for _, g := range groups {
		if g == want {
			return true
		}
	}
	return false
}

func environmentMatches(rule Rule, environment string) bool {
	if len(rule.Environments) == 0 {
		return true
	}
	for _, e := range rule.Environments {
		if e == "*" || e == environment {
			return true
		}
	}
	return false
}

func actionMatches(rule Rule, action Action) bool {
	if len(rule.Actions) == 0 {
		return true
	}
	for _, a := range rule.Actions {
		parsed, err := ParseAction(a)
		if err != nil {
			continue
		}
		if parsed == action {
			return true
		}
		if parsed != View && action == View {
			return true
		}
	}
	return false
}

// A flag with no team is matched only by "*".
func teamMatches(patterns []string, team string) bool {
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "*" || (team != "" && pattern == team) {
			return true
		}
	}
	return false
}

func NewUnchecked(rules []Rule) *Set {
	return &Set{rules: rules}
}

func (s *Set) NamedTeams() []string {
	if s == nil {
		return nil
	}
	var out []string
	for _, rule := range s.rules {
		for _, t := range rule.Teams {
			if t = strings.TrimSpace(t); t != "*" {
				out = append(out, t)
			}
		}
	}
	return out
}
