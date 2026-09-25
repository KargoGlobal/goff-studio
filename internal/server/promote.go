package server

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/go-feature-flag/studio/internal/auth"
	"github.com/go-feature-flag/studio/internal/goff"
	"github.com/go-feature-flag/studio/internal/permissions"
)

type PromoteRequest struct {
	Key     string
	From    string
	To      string
	Team    string
	Fields  []string
	FileSHA string
}

func (s *Service) Promote(ctx context.Context, sess auth.Session, req PromoteRequest) (*SaveResult, error) {
	plan, err := s.planPromotion(ctx, sess, req)
	if err != nil {
		return nil, err
	}

	if !plan.target.Present {
		return s.Create(ctx, sess, plan.createRequest())
	}

	return s.Save(ctx, sess, SaveRequest{
		Environment: req.To,
		Key:         req.Key,
		File:        plan.target.File,
		FileSHA:     req.FileSHA,
		LoadedFlag:  s.snapshotPtr(plan.target.File, req.FileSHA, req.Key),
		Action:      permissions.EditRules,
		Summary:     fmt.Sprintf("promoted from %s", req.From),
		Mutate:      plan.mutate,
	})
}

func (s *Service) DiffPromote(ctx context.Context, sess auth.Session, req PromoteRequest) (*DiffResult, error) {
	plan, err := s.planPromotion(ctx, sess, req)
	if err != nil {
		return nil, err
	}

	if !plan.target.Present {
		return s.DiffCreate(ctx, sess, plan.createRequest())
	}

	return s.Diff(ctx, sess, DiffRequest{
		Environment: req.To,
		Key:         req.Key,
		Mutate:      plan.mutate,
		Description: plan.description,
	})
}

type promotion struct {
	source      *CompareSide
	target      *CompareSide
	req         PromoteRequest
	mutate      func(*goff.Flag)
	description string
}

func (p promotion) createRequest() CreateRequest {
	f := *p.source.Flag
	return CreateRequest{
		Environment: p.req.To,
		Key:         p.req.Key,
		Team:        p.team(),
		Type:        f.Type,
		Variations:  f.Variations,
		Default:     f.Default.Variation,
		// A flag arriving in a new environment must never start serving on its own.
		Enabled: false,
	}
}

func (p promotion) team() string {
	if strings.TrimSpace(p.req.Team) != "" {
		return strings.TrimSpace(p.req.Team)
	}
	return p.source.Team
}

func (s *Service) planPromotion(ctx context.Context, sess auth.Session, req PromoteRequest) (*promotion, error) {
	if req.From == req.To {
		return nil, invalid("pick two different environments to promote between")
	}
	if len(req.Fields) == 0 {
		return nil, invalid("pick at least one setting to promote")
	}
	for _, f := range req.Fields {
		if !isCopyableField(f) {
			return nil, invalid("%q is not a promotable setting", f)
		}
	}

	source, err := s.compareSide(ctx, sess, req.Key, req.From)
	if err != nil {
		return nil, err
	}
	if !source.Present {
		return nil, fmt.Errorf("%w: %s does not exist in %s", ErrNotFound, req.Key, req.From)
	}

	target, err := s.compareSide(ctx, sess, req.Key, req.To)
	if err != nil {
		return nil, err
	}

	if target.Present {
		if blockers := promoteBlockers(*source.Flag, *target.Flag); len(blockers) > 0 {
			return nil, invalid("cannot promote: %s", strings.Join(blockers, "; "))
		}
		if err := checkPromotable(*source.Flag, *target.Flag, req.Fields); err != nil {
			return nil, err
		}
	}

	plan := &promotion{source: source, target: target, req: req}
	plan.mutate = promoteMutator(*source.Flag, req.Fields)
	plan.description = fmt.Sprintf("Promote %s from %s to %s: %s",
		req.Key, req.From, req.To, strings.Join(sorted(req.Fields), ", "))
	return plan, nil
}

func promoteMutator(source goff.Flag, fields []string) func(*goff.Flag) {
	want := map[string]bool{}
	for _, f := range fields {
		want[f] = true
	}

	return func(target *goff.Flag) {
		if want[FieldVariations] {
			target.Variations = source.Variations
		}
		if want[FieldDefault] {
			target.Default = source.Default
		}
		if want[FieldRules] {
			target.Rules = frozenRules(source.Rules)
		}
		if want[FieldExperimentation] {
			target.Experimentation = source.Experimentation
		}
		if want[FieldMetadata] {
			target.Metadata = mergedMetadata(source.Metadata, target.Metadata)
		}
		if want[FieldEnabled] {
			target.Enabled = source.Enabled
		}
	}
}

// A ramp's dates mean nothing in the target, so it is frozen at the allocation it reached.
func frozenRules(rules []goff.Rule) []goff.Rule {
	out := make([]goff.Rule, 0, len(rules))
	for _, r := range rules {
		if r.Progressive != nil {
			r.Outcome = goff.Outcome{Percentage: map[string]float64{
				r.Progressive.End.Variation:     r.Progressive.End.Percentage,
				r.Progressive.Initial.Variation: 100 - r.Progressive.End.Percentage,
			}}
			r.Progressive = nil
		}
		out = append(out, r)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// team names the target's own file, so the target's value always wins.
func mergedMetadata(source, target map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range source {
		if k == "team" {
			continue
		}
		out[k] = v
	}
	if team, ok := target["team"]; ok {
		out["team"] = team
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// Refuses a copy whose rules would serve a variation the target does not have.
func checkPromotable(source, target goff.Flag, fields []string) error {
	promotingVariations := false
	promotingRules := false
	for _, f := range fields {
		switch f {
		case FieldVariations:
			promotingVariations = true
		case FieldRules:
			promotingRules = true
		}
	}
	if !promotingRules || promotingVariations {
		return nil
	}

	available := map[string]bool{}
	for _, v := range target.Variations {
		available[v.Name] = true
	}

	missing := map[string]bool{}
	for _, r := range frozenRules(source.Rules) {
		for _, name := range outcomeVariations(r.Outcome) {
			if !available[name] {
				missing[name] = true
			}
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return invalid(
		"the rules in %s serve %s, which %s does not have; promote variations as well",
		source.Environment, strings.Join(sorted(mapKeys(missing)), " and "), target.Environment)
}

func outcomeVariations(o goff.Outcome) []string {
	if len(o.Percentage) > 0 {
		return mapKeys(o.Percentage)
	}
	if o.Variation != "" {
		return []string{o.Variation}
	}
	return nil
}

func isCopyableField(name string) bool {
	for _, f := range CopyableFields {
		if f == name {
			return true
		}
	}
	return false
}

func (s *Service) snapshotPtr(file, sha, key string) *goff.Flag {
	if flag, ok := s.snapshot(file, sha, key); ok {
		return &flag
	}
	return nil
}

func sorted(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

func mapKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
