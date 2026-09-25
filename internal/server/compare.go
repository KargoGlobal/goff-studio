package server

import (
	"context"
	"errors"
	"reflect"
	"sort"

	"github.com/go-feature-flag/studio/internal/auth"
	"github.com/go-feature-flag/studio/internal/goff"
)

const (
	FieldEnabled         = "enabled"
	FieldVariations      = "variations"
	FieldDefault         = "default"
	FieldRules           = "rules"
	FieldExperimentation = "experimentation"
	FieldMetadata        = "metadata"
)

var CopyableFields = []string{
	FieldVariations,
	FieldDefault,
	FieldRules,
	FieldExperimentation,
	FieldMetadata,
	FieldEnabled,
}

type CompareSide struct {
	Environment string     `json:"environment"`
	Display     string     `json:"display"`
	Present     bool       `json:"present"`
	Enabled     bool       `json:"enabled"`
	Summary     string     `json:"summary"`
	File        string     `json:"file,omitempty"`
	FileSHA     string     `json:"fileSha,omitempty"`
	Team        string     `json:"team,omitempty"`
	Writable    bool       `json:"writable"`
	Flag        *goff.Flag `json:"flag,omitempty"`
}

type CompareResult struct {
	Key      string      `json:"key"`
	From     CompareSide `json:"from"`
	To       CompareSide `json:"to"`
	Differs  []string    `json:"differs"`
	Blockers []string    `json:"blockers"`
	Diff     string      `json:"diff"`
}

func (s *Service) Compare(ctx context.Context, sess auth.Session, key, from, to string) (*CompareResult, error) {
	if from == to {
		return nil, invalid("pick two different environments to compare")
	}

	source, err := s.compareSide(ctx, sess, key, from)
	if err != nil {
		return nil, err
	}
	target, err := s.compareSide(ctx, sess, key, to)
	if err != nil {
		return nil, err
	}
	if !source.Present && !target.Present {
		return nil, ErrNotFound
	}

	out := &CompareResult{Key: key, From: *source, To: *target}
	if source.Present && target.Present {
		out.Differs = diffFields(*source.Flag, *target.Flag)
		out.Blockers = promoteBlockers(*source.Flag, *target.Flag)
	} else if source.Present {
		out.Differs = []string{"missing"}
	}

	out.Diff = UnifiedDiff(s.flagYAML(source.Flag), s.flagYAML(target.Flag), 3)
	if out.Differs == nil {
		out.Differs = []string{}
	}
	if out.Blockers == nil {
		out.Blockers = []string{}
	}
	return out, nil
}

func (s *Service) compareSide(ctx context.Context, sess auth.Session, key, env string) (*CompareSide, error) {
	known := s.Environments(ctx, sess)
	var found bool
	side := &CompareSide{Environment: env, Display: env}
	for _, e := range known {
		if e.Name == env {
			found = true
			if e.Display != "" {
				side.Display = e.Display
			}
		}
	}
	if !found {
		return nil, ErrForbidden
	}

	view, err := s.Get(ctx, sess, env, key)
	switch {
	case err == nil:
	case errors.Is(err, ErrNotFound):
		return side, nil
	default:
		return nil, err
	}

	flag := view.Flag
	side.Present = true
	side.Enabled = flag.Enabled
	side.Summary = view.Summary
	side.File = view.File
	side.FileSHA = view.FileSHA
	side.Team = teamNameOf(view.File)
	side.Flag = &flag
	for _, a := range view.Actions {
		if a == "edit_rules" || a == "edit_variations" {
			side.Writable = true
		}
	}
	return side, nil
}

// Compares parsed values so comments, key order and quoting never read as drift.
func diffFields(source, target goff.Flag) []string {
	var out []string
	for field, same := range map[string]bool{
		FieldEnabled:         source.Enabled == target.Enabled,
		FieldVariations:      sameVariations(source.Variations, target.Variations),
		FieldDefault:         reflect.DeepEqual(source.Default, target.Default),
		FieldRules:           reflect.DeepEqual(source.Rules, target.Rules),
		FieldExperimentation: reflect.DeepEqual(source.Experimentation, target.Experimentation),
		FieldMetadata:        sameMetadata(source.Metadata, target.Metadata),
	} {
		if !same {
			out = append(out, field)
		}
	}
	sort.Strings(out)
	return out
}

func sameVariations(a, b []goff.Variation) bool {
	if len(a) != len(b) {
		return false
	}
	byName := make(map[string]any, len(b))
	for _, v := range b {
		byName[v.Name] = v.Value
	}
	for _, v := range a {
		other, ok := byName[v.Name]
		if !ok || !reflect.DeepEqual(v.Value, other) {
			return false
		}
	}
	return true
}

// team is Studio's own label for the file, so it must not count as drift.
func sameMetadata(a, b map[string]any) bool {
	strip := func(in map[string]any) map[string]any {
		if len(in) == 0 {
			return nil
		}
		out := make(map[string]any, len(in))
		for k, v := range in {
			if k == "team" {
				continue
			}
			out[k] = v
		}
		if len(out) == 0 {
			return nil
		}
		return out
	}
	return reflect.DeepEqual(strip(a), strip(b))
}

func promoteBlockers(source, target goff.Flag) []string {
	var out []string
	if source.Type != target.Type {
		out = append(out, "the flags have different types, "+string(source.Type)+" and "+string(target.Type))
	}
	return out
}

func (s *Service) flagYAML(f *goff.Flag) string {
	if f == nil {
		return ""
	}
	out, err := s.adapter.Serialize(nil, f.Key, *f)
	if err != nil {
		return ""
	}
	return string(out)
}
