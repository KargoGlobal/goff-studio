package server

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-feature-flag/studio/internal/auth"
	"github.com/go-feature-flag/studio/internal/goff"
	"github.com/go-feature-flag/studio/internal/permissions"
	"github.com/go-feature-flag/studio/pkg/splits"
)

const (
	ExperimentOpExposure    = "exposure"
	ExperimentOpCreate      = "create"
	ExperimentOpRerandomize = "rerandomize"
	ExperimentOpWindow      = "window"
)

type ExperimentRequest struct {
	Environment     string       `json:"-"`
	Key             string       `json:"-"`
	Op              string       `json:"op"`
	RuleName        string       `json:"ruleName"`
	ExposurePercent *float64     `json:"exposurePercent"`
	Arms            []splits.Arm `json:"arms"`
	ExperimentKey   string       `json:"experimentKey"`
	Unit            *splits.Unit `json:"unit"`
	Confirm         bool         `json:"confirm"`
	StartAt         *string      `json:"startAt"`
	EndAt           *string      `json:"endAt"`
	FileSHA         string       `json:"fileSha"`
}

type experimentPlan struct {
	action  permissions.Action
	summary string
	apply   func(*goff.Flag) error
}

// Exposure is a ramp; everything that reshapes who is in which arm is a rule change.
func (r ExperimentRequest) action() permissions.Action {
	switch r.Op {
	case ExperimentOpExposure, ExperimentOpWindow:
		return permissions.Rollout
	default:
		return permissions.EditRules
	}
}

func (s *Service) planExperiment(view goff.Flag, req ExperimentRequest) (*experimentPlan, error) {
	req.RuleName = strings.TrimSpace(req.RuleName)
	if req.RuleName == "" {
		return nil, invalid("which rule? an experiment lives on a named targeting rule")
	}
	if !ruleExists(view.Rules, req.RuleName) {
		return nil, invalid("there is no rule named %s on this flag", req.RuleName)
	}
	if view.ExperimentError != "" {
		return nil, invalid("the experiment block on this flag cannot be read (%s); fix it in the file first", view.ExperimentError)
	}

	var (
		summary string
		err     error
	)
	apply := func(f *goff.Flag) error {
		f.Experiment = f.Experiment.Clone()
		summary, err = s.applyExperiment(f, req)
		return err
	}

	probe := view
	if err := apply(&probe); err != nil {
		return nil, err
	}
	if problems := splits.Validate(probe.Experiment, splitsShape(probe)).Err(); problems != nil {
		return nil, invalid("the experiment would not be valid: %s", problems)
	}
	return &experimentPlan{action: req.action(), summary: summary, apply: apply}, nil
}

func (s *Service) applyExperiment(f *goff.Flag, req ExperimentRequest) (string, error) {
	var alloc *splits.Allocation
	if f.Experiment != nil {
		alloc = f.Experiment.Allocations[req.RuleName]
	}
	if req.Op != ExperimentOpCreate && alloc == nil {
		return "", invalid("rule %s has no experiment yet", req.RuleName)
	}

	switch req.Op {
	case ExperimentOpExposure:
		return growExposure(f, alloc, req)
	case ExperimentOpCreate:
		return s.createExperiment(f, alloc, req)
	case ExperimentOpRerandomize:
		return s.rerandomize(f, alloc, req)
	case ExperimentOpWindow:
		return setWindow(alloc, req)
	}
	return "", invalid("unknown experiment change %q", req.Op)
}

func growExposure(f *goff.Flag, alloc *splits.Allocation, req ExperimentRequest) (string, error) {
	if req.ExposurePercent == nil {
		return "", invalid("an exposure percentage is required")
	}
	from, to, err := splits.GrowExposure(alloc, *req.ExposurePercent, f.Experiment.TotalShards)
	if err != nil {
		return "", invalid("%s", err.Error())
	}
	return fmt.Sprintf("exposure %s%% → %s%% in rule %s; existing subjects keep their arm",
		splits.FormatPercent(from), splits.FormatPercent(to), req.RuleName), nil
}

func (s *Service) createExperiment(f *goff.Flag, existing *splits.Allocation, req ExperimentRequest) (string, error) {
	if existing != nil {
		return "", invalid("rule %s already runs an experiment; re-randomize it to change the arms", req.RuleName)
	}
	if req.ExposurePercent == nil {
		return "", invalid("an exposure percentage is required")
	}
	if f.Experiment == nil {
		f.Experiment, _ = splits.ParseJSON([]byte(`{"version":1}`))
		if req.Unit != nil {
			f.Experiment.Unit = *req.Unit
		}
	} else if req.Unit != nil && *req.Unit != f.Experiment.Unit {
		return "", invalid("experiments on this flag already use unit %s/%s", f.Experiment.Unit.Type, f.Experiment.Unit.Key)
	}

	built, err := splits.BuildSplits(req.Arms, *req.ExposurePercent, f.Experiment.TotalShards, s.salt)
	if err != nil {
		return "", invalid("%s", err.Error())
	}
	key := strings.TrimSpace(req.ExperimentKey)
	if key == "" {
		key = f.Key + "-" + req.RuleName
	}
	// Written out even when it equals the default, so renaming the flag cannot rename the experiment.
	f.Experiment.Allocations[req.RuleName] = &splits.Allocation{ExperimentKey: key, Splits: built}
	markAllocations(f)

	return fmt.Sprintf("new experiment %s on rule %s: %s at %s%% exposure",
		key, req.RuleName, describeArms(req.Arms), splits.FormatPercent(*req.ExposurePercent)), nil
}

func (s *Service) rerandomize(f *goff.Flag, alloc *splits.Allocation, req ExperimentRequest) (string, error) {
	if !req.Confirm {
		return "", invalid("re-randomizing reassigns every subject in %s; confirm to continue", req.RuleName)
	}
	total := f.Experiment.TotalShards

	arms, exposure := req.Arms, req.ExposurePercent
	if len(arms) == 0 && exposure == nil {
		// Same ranges, new salts: nothing about the shape to infer.
		if err := splits.Rerandomize(alloc, s.salt); err != nil {
			return "", err
		}
		return fmt.Sprintf("re-randomized rule %s with new salts; every subject is reassigned", req.RuleName), nil
	}
	if layout, ok := alloc.Layout(); ok {
		if len(arms) == 0 {
			covered := 0
			for i, r := range layout.Arms {
				covered += r.Len()
				arms = append(arms, splits.Arm{Variation: alloc.Splits[i].Variation, Weight: float64(r.Len())})
			}
			// Gaps (or overlaps) in the arm salt would silently change exposure once spread over every shard.
			if covered != total {
				return "", invalid("the arms of rule %s cover %d of %d shards, so their weights are ambiguous; give the arms explicitly to re-randomize", req.RuleName, covered, total)
			}
		}
		if exposure == nil {
			current := splits.ShardsToPercent(splits.Shard{Ranges: layout.ExposureRanges}.Covered(total), total)
			exposure = &current
		}
	}

	if len(arms) == 0 || exposure == nil {
		return "", invalid("this allocation has no separate exposure salt; give both the arms and an exposure to rebuild it")
	}

	built, err := splits.BuildSplits(arms, *exposure, total, s.salt)
	if err != nil {
		return "", invalid("%s", err.Error())
	}
	alloc.Splits = built
	return fmt.Sprintf("re-randomized rule %s: %s at %s%% exposure; every subject is reassigned",
		req.RuleName, describeArms(arms), splits.FormatPercent(*exposure)), nil
}

func setWindow(alloc *splits.Allocation, req ExperimentRequest) (string, error) {
	if req.StartAt == nil && req.EndAt == nil {
		return "", invalid("give a start, an end, or both")
	}
	parse := func(label string, raw *string, into **time.Time) error {
		if raw == nil {
			return nil
		}
		if strings.TrimSpace(*raw) == "" {
			*into = nil
			return nil
		}
		t, err := parseDate(label, strings.TrimSpace(*raw))
		if err != nil {
			return invalid("%s", err.Error())
		}
		t = t.UTC()
		*into = &t
		return nil
	}
	if err := parse("start", req.StartAt, &alloc.StartAt); err != nil {
		return "", err
	}
	if err := parse("end", req.EndAt, &alloc.EndAt); err != nil {
		return "", err
	}
	if alloc.StartAt != nil && alloc.EndAt != nil && !alloc.EndAt.After(*alloc.StartAt) {
		return "", invalid("the end must be after the start")
	}
	return fmt.Sprintf("experiment window on rule %s: %s", req.RuleName, describeWindow(alloc)), nil
}

func describeWindow(a *splits.Allocation) string {
	when := func(t *time.Time, open string) string {
		if t == nil {
			return open
		}
		return t.UTC().Format(time.RFC3339)
	}
	return fmt.Sprintf("from %s until %s", when(a.StartAt, "now"), when(a.EndAt, "stopped"))
}

func describeArms(arms []splits.Arm) string {
	total := 0.0
	for _, a := range arms {
		total += a.Weight
	}
	parts := make([]string, 0, len(arms))
	for _, a := range arms {
		if total == 0 || a.Weight == 0 {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s%% %s", splits.FormatPercent(a.Weight/total*100), a.Variation))
	}
	return strings.Join(parts, " / ")
}

func markAllocations(f *goff.Flag) {
	f.Rules = append([]goff.Rule(nil), f.Rules...)
	for i := range f.Rules {
		_, f.Rules[i].HasAllocation = f.Experiment.Allocations[f.Rules[i].Name]
	}
}

func splitsShape(f goff.Flag) *splits.Flag {
	out := &splits.Flag{Key: f.Key}
	for _, v := range f.Variations {
		out.Variations = append(out.Variations, v.Name)
	}
	for _, r := range f.Rules {
		out.Rules = append(out.Rules, splits.Rule{Name: r.Name})
	}
	return out
}

func (s *Service) SaveFlagExperiment(ctx context.Context, sess auth.Session, req ExperimentRequest) (*SaveResult, error) {
	view, err := s.Get(ctx, sess, req.Environment, req.Key)
	if err != nil {
		return nil, err
	}
	if !s.perms.Allowed(permissions.Request{
		Groups: sess.Groups, Environment: req.Environment, File: view.File, Action: req.action(),
	}) {
		return nil, ErrForbidden
	}
	plan, err := s.planExperiment(view.Flag, req)
	if err != nil {
		return nil, err
	}
	return s.Save(ctx, sess, SaveRequest{
		Environment: req.Environment,
		Key:         req.Key,
		File:        view.File,
		FileSHA:     req.FileSHA,
		LoadedFlag:  s.snapshotFor(view.File, req.Key, req.FileSHA),
		Action:      plan.action,
		Summary:     plan.summary,
		MutateErr:   plan.apply,
	})
}

func (s *Service) DiffFlagExperiment(ctx context.Context, sess auth.Session, req ExperimentRequest) (*DiffResult, error) {
	view, err := s.Get(ctx, sess, req.Environment, req.Key)
	if err != nil {
		return nil, err
	}
	if !s.perms.Allowed(permissions.Request{
		Groups: sess.Groups, Environment: req.Environment, File: view.File, Action: req.action(),
	}) {
		return nil, ErrForbidden
	}
	plan, err := s.planExperiment(view.Flag, req)
	if err != nil {
		return nil, err
	}
	description := capitalize(plan.summary)
	if req.Op == ExperimentOpCreate || req.Op == ExperimentOpRerandomize {
		description += ". Salts are generated fresh when you save"
	}
	return s.Diff(ctx, sess, DiffRequest{
		Environment: req.Environment,
		Key:         req.Key,
		MutateErr:   plan.apply,
		Description: description,
	})
}

func (s *Service) snapshotFor(file, key, sha string) *goff.Flag {
	if sha == "" {
		return nil
	}
	if snap, ok := s.snapshot(file, sha, key); ok {
		return &snap
	}
	return nil
}
