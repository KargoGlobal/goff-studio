package permissions

import (
	"fmt"
	"path"
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
	Allow        []string `yaml:"allow"`
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
		if len(r.Allow) == 0 {
			return nil, fmt.Errorf("permission rule for group %q has no allow patterns", r.Group)
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
	File        string
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
		if !fileMatches(rule.Allow, req.File) {
			continue
		}
		return true
	}
	return false
}

func (s *Set) ActionsFor(groups []string, environment, file string) []Action {
	var out []Action
	for _, a := range AllActions {
		if s.Allowed(Request{Groups: groups, Environment: environment, File: file, Action: a}) {
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

func fileMatches(patterns []string, file string) bool {
	base := strings.TrimSuffix(basename(file), ".goff.yaml")
	base = strings.TrimSuffix(base, ".yaml")
	base = strings.TrimSuffix(base, ".yml")

	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "*" || pattern == "**" {
			return true
		}
		if pattern == file || pattern == base {
			return true
		}
		if ok, _ := path.Match(pattern, file); ok {
			return true
		}
		if ok, _ := path.Match(pattern, base); ok {
			return true
		}
	}
	return false
}

func basename(file string) string {
	if idx := strings.LastIndex(file, "/"); idx >= 0 {
		return file[idx+1:]
	}
	return file
}
