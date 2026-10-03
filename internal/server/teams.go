package server

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/go-feature-flag/studio/internal/auth"
	"github.com/go-feature-flag/studio/internal/permissions"
	"github.com/go-feature-flag/studio/internal/storage"
)

type TeamEnvironment struct {
	Name    string `json:"name"`
	File    string `json:"file"`
	Flags   int    `json:"flags"`
	CanEdit bool   `json:"canEdit"`
}

type TeamGroup struct {
	Name     string `json:"name"`
	Edits    bool   `json:"edits"`
	AllTeams bool   `json:"allTeams"`
}

type TeamSummary struct {
	Name         string            `json:"name"`
	Environments []TeamEnvironment `json:"environments"`
	Groups       []TeamGroup       `json:"groups"`
	CanEdit      bool              `json:"canEdit"`
}

type TeamsResult struct {
	Teams []TeamSummary `json:"teams"`
}

type teamCounts map[string]map[string]int

func (s *Service) Teams(ctx context.Context, sess auth.Session) (*TeamsResult, error) {
	counts, err := s.teamCounts(ctx)
	if err != nil {
		return nil, err
	}
	environments, err := s.environmentNames(ctx)
	if err != nil {
		return nil, err
	}

	out := &TeamsResult{Teams: []TeamSummary{}}
	for _, name := range s.cfg.TeamNames() {
		summary := TeamSummary{Name: name, Environments: []TeamEnvironment{}, Groups: []TeamGroup{}}
		groups := map[string]*TeamGroup{}

		for _, environment := range environments {
			if !s.perms.Allowed(permissions.Request{
				Groups: sess.Groups, Environment: environment, Team: name, Action: permissions.View,
			}) {
				continue
			}

			canEdit := canEditAny(s.perms.ActionsFor(sess.Groups, environment, name))
			summary.CanEdit = summary.CanEdit || canEdit
			summary.Environments = append(summary.Environments, TeamEnvironment{
				Name: environment, File: s.teamFileFor(environment, name), Flags: counts[environment][name], CanEdit: canEdit,
			})

			for _, grant := range s.perms.GrantsFor(environment, name) {
				merged, seen := groups[grant.Group]
				if !seen {
					groups[grant.Group] = &TeamGroup{Name: grant.Group, Edits: grant.Writes, AllTeams: grant.AllTeams}
					continue
				}
				merged.Edits = merged.Edits || grant.Writes
				merged.AllTeams = merged.AllTeams && grant.AllTeams
			}
		}

		if len(summary.Environments) == 0 {
			continue
		}
		for _, g := range groups {
			summary.Groups = append(summary.Groups, *g)
		}
		sort.Slice(summary.Groups, func(i, j int) bool {
			a, b := summary.Groups[i], summary.Groups[j]
			if a.AllTeams != b.AllTeams {
				return !a.AllTeams
			}
			return a.Name < b.Name
		})
		out.Teams = append(out.Teams, summary)
	}

	sort.Slice(out.Teams, func(i, j int) bool { return out.Teams[i].Name < out.Teams[j].Name })
	return out, nil
}

func canEditAny(actions []permissions.Action) bool {
	for _, a := range actions {
		if a != permissions.View {
			return true
		}
	}
	return false
}

func (s *Service) teamCounts(ctx context.Context) (teamCounts, error) {
	s.teamMu.Lock()
	defer s.teamMu.Unlock()

	if s.teamIndex != nil && s.now().Sub(s.teamLoadedAt) < s.envTTL {
		return s.teamIndex, nil
	}
	index, err := s.countTeamFlags(ctx)
	if err != nil {
		return nil, err
	}
	s.teamIndex, s.teamLoadedAt = index, s.now()
	return index, nil
}

func (s *Service) invalidateTeams() {
	s.teamMu.Lock()
	defer s.teamMu.Unlock()
	s.teamIndex = nil
	s.teamLoadedAt = time.Time{}
}

func (s *Service) countTeamFlags(ctx context.Context) (teamCounts, error) {
	environments, err := s.environmentNames(ctx)
	if err != nil {
		return nil, err
	}

	index := teamCounts{}
	for _, environment := range environments {
		files, err := s.repo.ListFiles(ctx, environment)
		if errors.Is(err, storage.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("listing environment %s: %w", environment, err)
		}

		counts := map[string]int{}
		for _, file := range files {
			f, err := s.repo.ReadFile(ctx, file)
			if err != nil {
				return nil, err
			}
			flags, _, parseErr := s.adapter.Parse(file, f.Content)
			if parseErr != nil {
				continue
			}
			for _, flag := range flags {
				if team := s.owner(file, flag.Team); team != "" {
					counts[team]++
				}
			}
		}
		index[environment] = counts
	}
	return index, nil
}
