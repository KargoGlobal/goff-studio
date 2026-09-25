package splits

import (
	"fmt"
	"sort"
	"strings"
)

type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
)

type Problem struct {
	Severity Severity `json:"severity"`
	Path     string   `json:"path"`
	Message  string   `json:"message"`
}

func (p Problem) String() string { return p.Path + ": " + p.Message }

type Problems []Problem

func (ps Problems) Errors() Problems {
	var out Problems
	for _, p := range ps {
		if p.Severity == SeverityError {
			out = append(out, p)
		}
	}
	return out
}

func (ps Problems) Err() error {
	errs := ps.Errors()
	if len(errs) == 0 {
		return nil
	}
	parts := make([]string, len(errs))
	for i, p := range errs {
		parts[i] = p.String()
	}
	return fmt.Errorf("%s", strings.Join(parts, "; "))
}

// Validate checks the block against the flag it lives in. Variation and rule
// names are taken from the flag; pass nil to skip those cross-checks.
func Validate(e *Experiment, f *Flag) Problems {
	var ps Problems
	errorf := func(path, format string, args ...any) {
		ps = append(ps, Problem{SeverityError, path, fmt.Sprintf(format, args...)})
	}
	warnf := func(path, format string, args ...any) {
		ps = append(ps, Problem{SeverityWarning, path, fmt.Sprintf(format, args...)})
	}

	if e == nil {
		return nil
	}
	if e.Version != 1 {
		errorf("version", "only version 1 is supported, got %d", e.Version)
	}
	if e.Hash != HashMD5Shard {
		errorf("hash", "only %q is supported, got %q", HashMD5Shard, e.Hash)
	}
	if e.TotalShards <= 0 {
		errorf("totalShards", "must be a positive number")
		return ps
	}
	if e.Unit.Type != UnitRequest && e.Unit.Type != UnitEntity {
		errorf("unit.type", "must be %q or %q, got %q", UnitRequest, UnitEntity, e.Unit.Type)
	}
	if e.Holdout != nil {
		checkShard(e.Holdout, "holdout", e.TotalShards, errorf)
	}

	var variations, rules map[string]bool
	if f != nil {
		variations = map[string]bool{}
		for _, v := range f.Variations {
			variations[v] = true
		}
		rules = map[string]bool{}
		for _, r := range f.Rules {
			rules[r.Name] = true
		}
	}

	for _, name := range sortedAllocationNames(e) {
		a := e.Allocations[name]
		path := "allocations." + name
		if a == nil {
			errorf(path, "is empty")
			continue
		}
		if rules != nil && !rules[name] {
			errorf(path, "there is no targeting rule named %q on this flag", name)
		}
		if a.StartAt != nil && a.EndAt != nil && !a.EndAt.After(*a.StartAt) {
			errorf(path+".endAt", "must be after startAt")
		}
		if a.Layer != nil {
			checkShard(a.Layer, path+".layer", e.TotalShards, errorf)
		}
		if len(a.Splits) == 0 {
			errorf(path+".splits", "an allocation needs at least one split")
		}
		for i, s := range a.Splits {
			spath := fmt.Sprintf("%s.splits[%d]", path, i)
			if s.Variation == "" {
				errorf(spath+".variation", "is required")
			} else if variations != nil && !variations[s.Variation] {
				errorf(spath+".variation", "%q is not a variation on this flag", s.Variation)
			}
			for j := range s.Shards {
				checkShard(&s.Shards[j], fmt.Sprintf("%s.shards[%d]", spath, j), e.TotalShards, errorf)
			}
		}
		for i := range a.Splits {
			for j := i + 1; j < len(a.Splits); j++ {
				if splitsOverlap(a.Splits[i], a.Splits[j]) {
					warnf(path, "splits %d and %d can both match the same subject; the first one wins", i, j)
				}
			}
		}
	}
	return ps
}

func checkShard(s *Shard, path string, total int, errorf func(string, string, ...any)) {
	if s.Salt == "" {
		errorf(path+".salt", "is required")
	}
	for i, r := range s.Ranges {
		rpath := fmt.Sprintf("%s.ranges[%d]", path, i)
		if r.Start < 0 || r.End > total {
			errorf(rpath, "[%d, %d) must lie within [0, %d)", r.Start, r.End, total)
		}
		if r.Start >= r.End {
			errorf(rpath, "start %d must be less than end %d", r.Start, r.End)
		}
	}
}

// Two splits are disjoint only if some salt they share has non-intersecting ranges.
func splitsOverlap(a, b Split) bool {
	for _, x := range a.Shards {
		for _, y := range b.Shards {
			if x.Salt == y.Salt && !rangesIntersect(x.Ranges, y.Ranges) {
				return false
			}
		}
	}
	return true
}

func sortedAllocationNames(e *Experiment) []string {
	names := make([]string, 0, len(e.Allocations))
	for name := range e.Allocations {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
