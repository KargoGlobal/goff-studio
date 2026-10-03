package server

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/go-feature-flag/studio/internal/auth"
	"github.com/go-feature-flag/studio/internal/goff"
	"github.com/go-feature-flag/studio/internal/permissions"
	"github.com/go-feature-flag/studio/internal/storage"
)

const (
	DefaultSearchResults = 25
	MaxSearchResults     = 100
	maxSearchQuery       = 200
)

type FlagStatus struct {
	Environment string `json:"environment"`
	Protected   bool   `json:"protected"`
	Present     bool   `json:"present"`
	Enabled     bool   `json:"enabled"`
	Team        string `json:"team,omitempty"`
	Summary     string `json:"summary,omitempty"`
	CanToggle   bool   `json:"canToggle"`
}

// FlagStatus answers "where does this flag exist and is it on", which is the first question in an incident.
func (s *Service) FlagStatus(ctx context.Context, sess auth.Session, key string) ([]FlagStatus, error) {
	if err := validKey(key); err != nil {
		return nil, err
	}
	envs, err := s.Environments(ctx, sess)
	if err != nil {
		return nil, err
	}

	out := []FlagStatus{}
	for _, env := range envs {
		status := FlagStatus{Environment: env.Name, Protected: env.Protected}
		view, err := s.Get(ctx, sess, env.Name, key)
		switch {
		case errors.Is(err, ErrNotFound), errors.Is(err, ErrForbidden):
		case err != nil:
			return nil, err
		default:
			status.Present = true
			status.Enabled = view.Enabled
			status.Team = s.teamOf(view)
			status.Summary = view.Summary
			status.CanToggle = allows(view.Actions, permissions.Toggle)
		}
		out = append(out, status)
	}
	return out, nil
}

type SearchHit struct {
	Environment string `json:"environment"`
	Key         string `json:"key"`
	Team        string `json:"team,omitempty"`
	Enabled     bool   `json:"enabled"`
	Description string `json:"description,omitempty"`
	Summary     string `json:"summary"`
}

type SearchResult struct {
	Hits      []SearchHit `json:"hits"`
	Truncated bool        `json:"truncated"`
}

// FindFlags matches key, team and description case-insensitively, across every environment the caller can see.
func (s *Service) FindFlags(ctx context.Context, sess auth.Session, query, environment string, limit int) (*SearchResult, error) {
	query = strings.TrimSpace(query)
	if hasControlChars(query) || len(query) > maxSearchQuery {
		return nil, invalid("a search must be a single line of at most %d characters", maxSearchQuery)
	}
	if limit <= 0 {
		limit = DefaultSearchResults
	}
	limit = min(limit, MaxSearchResults)

	var names []string
	if environment != "" {
		if err := validEnvironment(environment); err != nil {
			return nil, err
		}
		names = []string{environment}
	} else {
		envs, err := s.Environments(ctx, sess)
		if err != nil {
			return nil, err
		}
		for _, e := range envs {
			names = append(names, e.Name)
		}
	}

	needle := strings.ToLower(query)
	out := &SearchResult{Hits: []SearchHit{}}
	for _, env := range names {
		list, err := s.List(ctx, sess, env)
		if errors.Is(err, ErrForbidden) && environment == "" {
			continue
		}
		// Hidden and missing look the same, so a search cannot probe for environment names.
		if environment != "" && (errors.Is(err, ErrForbidden) || errors.Is(err, storage.ErrNotFound)) {
			return nil, ErrNoSuchEnvironment
		}
		if err != nil {
			return nil, err
		}
		for i := range list.Flags {
			v := &list.Flags[i]
			description := flagDescription(v.Flag)
			team := s.teamOf(v)
			if needle != "" && !strings.Contains(strings.ToLower(v.Key), needle) &&
				!strings.Contains(strings.ToLower(team), needle) &&
				!strings.Contains(strings.ToLower(description), needle) {
				continue
			}
			out.Hits = append(out.Hits, SearchHit{
				Environment: env, Key: v.Key, Team: team, Enabled: v.Enabled, Description: description, Summary: v.Summary,
			})
		}
	}
	sort.SliceStable(out.Hits, func(i, j int) bool {
		if out.Hits[i].Key != out.Hits[j].Key {
			return out.Hits[i].Key < out.Hits[j].Key
		}
		return out.Hits[i].Environment < out.Hits[j].Environment
	})
	if len(out.Hits) > limit {
		out.Hits, out.Truncated = out.Hits[:limit], true
	}
	return out, nil
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	result, err := s.svc.FlagStatus(r.Context(), sess, r.PathValue("key"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"key": r.PathValue("key"), "environments": result})
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	result, err := s.svc.FindFlags(r.Context(), sess, q.Get("q"), q.Get("environment"), limit)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func flagDescription(f goff.Flag) string {
	d, _ := f.Metadata[metaDescription].(string)
	return d
}
