package server

import (
	"net/http"
	"strings"
	"testing"

	"github.com/go-feature-flag/studio/internal/auth"
	"github.com/go-feature-flag/studio/internal/goff"
	"github.com/go-feature-flag/studio/internal/permissions"
)

func listedFlag(t *testing.T, list ListResult, key string) FlagView {
	t.Helper()
	for _, f := range list.Flags {
		if f.Key == key {
			return f
		}
	}
	t.Fatalf("flag %q not listed", key)
	return FlagView{}
}

func TestAFileNamedForAnUndeclaredTeamIsAdminOnly(t *testing.T) {
	repo := newRepo()
	repo.files["production/legacy.goff.yaml"] = strings.Replace(growthFile, "banner-test", "old-banner", 1)
	repo.shas["production/legacy.goff.yaml"] = "sha-legacy"

	rules := []permissions.Rule{
		{Group: "flags-admins", Teams: []string{"*"}},
		{Group: "marketing", Teams: []string{"growth", "legacy"}},
	}
	srv, sealer := testServer(t, repo, rules)

	all := listFor(t, srv, sealer, admin())
	old := listedFlag(t, all, "old-banner")
	if old.Team != "legacy" || !old.UnknownTeam {
		t.Errorf("want team legacy marked unknown, got team=%q unknown=%v", old.Team, old.UnknownTeam)
	}
	if listedFlag(t, all, "banner-test").UnknownTeam {
		t.Error("a declared team must not be marked unknown")
	}

	for _, f := range listFor(t, srv, sealer, marketer()).Flags {
		if f.Key == "old-banner" {
			t.Error("naming an undeclared team in a rule must not grant access to it")
		}
	}
}

func TestTheEnvironmentFileHoldsFlagsWithNoTeam(t *testing.T) {
	repo := newRepo()
	repo.files["production/flags.goff.yaml"] = strings.Replace(growthFile, "banner-test", "shared-banner", 1)
	repo.shas["production/flags.goff.yaml"] = "sha-flags"
	srv, sealer := testServer(t, repo, []permissions.Rule{
		{Group: "flags-admins", Teams: []string{"*"}},
		{Group: "marketing", Teams: []string{"growth"}},
	})

	shared := listedFlag(t, listFor(t, srv, sealer, admin()), "shared-banner")
	if shared.Team != "" || shared.UnknownTeam {
		t.Errorf("flags.goff.yaml holds no-team flags, got team=%q unknown=%v", shared.Team, shared.UnknownTeam)
	}
	for _, f := range listFor(t, srv, sealer, marketer()).Flags {
		if f.Key == "shared-banner" {
			t.Error("a named-team rule must not reach no-team flags")
		}
	}
}

func TestWithNoTeamsDeclaredOnlyNoTeamIsOffered(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())
	srv.cfg.Teams = nil

	list := listFor(t, srv, sealer, admin())
	if len(list.Teams) != 1 || list.Teams[0].Name != "" || list.Teams[0].File != "production/flags.goff.yaml" {
		t.Errorf("teams = %+v, want only no team", list.Teams)
	}

	body := `{"key":"x-flag","team":"growth","type":"boolean","enabled":true,` +
		`"variations":[{"name":"on","value":"true"}],"default":"on"}`
	rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/environments/production/flags", body)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "declares no teams") {
		t.Errorf("status %d: %s, want 400 saying no teams are declared", rec.Code, rec.Body)
	}
}

func TestSingleFileCannotMoveAFlagIntoAnUndeclaredTeam(t *testing.T) {
	repo := singleFileRepo()
	srv, _ := singleFileServer(t, repo, adminRules())

	_, err := srv.svc.Save(t.Context(), *admin(), SaveRequest{
		Environment: "production", Key: "banner", File: "production/flags.goff.yaml",
		Action: permissions.EditRules, Summary: "moved",
		Mutate: func(f *goff.Flag) { f.Metadata = map[string]any{"team": "made-up"} },
	})
	if err == nil || !strings.Contains(err.Error(), "not a team") {
		t.Fatalf("moving into an undeclared team must be refused, got %v", err)
	}
	if repo.files["production/flags.goff.yaml"] != sharedFile {
		t.Error("a refused move was written")
	}
}

func TestSingleFileUnknownMetadataTeamIsAdminOnly(t *testing.T) {
	repo := singleFileRepo()
	repo.files["production/flags.goff.yaml"] = strings.Replace(sharedFile, "team: growth", "team: retired", 1)
	srv, sealer := singleFileServer(t, repo, append(teamRules(),
		permissions.Rule{Group: "growth", Teams: []string{"retired"}}))
	grower := &auth.Session{Subject: "okta|g", Groups: []string{"growth"}}

	banner := listedFlag(t, listFor(t, srv, sealer, admin()), "banner")
	if banner.Team != "retired" || !banner.UnknownTeam {
		t.Errorf("want retired marked unknown, got %+v", banner)
	}
	if keys := flagKeys(listFor(t, srv, sealer, grower)); len(keys) != 0 {
		t.Errorf("a rule naming a removed team must grant nothing, got %v", keys)
	}
}
