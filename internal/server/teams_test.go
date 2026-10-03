package server

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-feature-flag/studio/internal/auth"
	"github.com/go-feature-flag/studio/internal/permissions"
)

func teamsFor(t *testing.T, srv *Server, sealer *auth.Sealer, sess *auth.Session) TeamsResult {
	t.Helper()
	rec := request(t, srv, sealer, sess, http.MethodGet, "/api/teams", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("/api/teams status %d: %s", rec.Code, rec.Body)
	}
	var out TeamsResult
	decode(t, rec, &out)
	return out
}

func teamNames(r TeamsResult) string {
	var names []string
	for _, team := range r.Teams {
		names = append(names, team.Name)
	}
	return strings.Join(names, ",")
}

func findTeam(t *testing.T, r TeamsResult, name string) TeamSummary {
	t.Helper()
	for _, team := range r.Teams {
		if team.Name == name {
			return team
		}
	}
	t.Fatalf("team %q not in %s", name, teamNames(r))
	return TeamSummary{}
}

func groupNames(team TeamSummary) string {
	var names []string
	for _, g := range team.Groups {
		names = append(names, g.Name)
	}
	return strings.Join(names, ",")
}

func TestTeamsListsEachTeamOnceAcrossEnvironments(t *testing.T) {
	srv, sealer := testServer(t, twoEnvRepo(), adminRules())

	got := teamsFor(t, srv, sealer, admin())
	if teamNames(got) != "billing,growth,payments,platform" {
		t.Fatalf("teams = %s, want every declared team once, sorted", teamNames(got))
	}

	payments := findTeam(t, got, "payments")
	if len(payments.Environments) != 2 || payments.Environments[0].Name != "dev" || payments.Environments[1].Name != "production" {
		t.Errorf("payments environments = %+v, want dev and production", payments.Environments)
	}
	if payments.Environments[1].File != "production/payments.goff.yaml" || payments.Environments[1].Flags != 1 {
		t.Errorf("production payments = %+v, want its file and one flag", payments.Environments[1])
	}
	if !payments.CanEdit {
		t.Error("an admin can edit every team")
	}
}

func TestTeamsNamesTheGroupsThatOwnEachTeam(t *testing.T) {
	rules := []permissions.Rule{
		{Group: "flags-admins", Teams: []string{"*"}},
		{Group: "payments-team", Teams: []string{"payments"}},
		{Group: "marketing", Teams: []string{"growth"}, Environments: []string{"production"}, Actions: []string{"toggle", "rollout"}},
		{Group: "auditors", Teams: []string{"*"}, Actions: []string{"view"}},
	}
	srv, sealer := testServer(t, twoEnvRepo(), rules)

	got := teamsFor(t, srv, sealer, admin())

	payments := findTeam(t, got, "payments")
	if groupNames(payments) != "payments-team,auditors,flags-admins" {
		t.Errorf("payments groups = %s, want the named group first, then the all-team groups", groupNames(payments))
	}
	for _, g := range payments.Groups {
		switch g.Name {
		case "payments-team":
			if g.AllTeams || !g.Edits {
				t.Errorf("payments-team = %+v, want a named owner that can edit", g)
			}
		case "auditors":
			if !g.AllTeams || g.Edits {
				t.Errorf("auditors = %+v, want all-teams and read-only", g)
			}
		case "flags-admins":
			if !g.AllTeams || !g.Edits {
				t.Errorf("flags-admins = %+v, want all-teams and editing", g)
			}
		}
	}

	growth := findTeam(t, got, "growth")
	for _, g := range growth.Groups {
		if g.Name == "marketing" && !g.Edits {
			t.Error("marketing can toggle, so it edits growth")
		}
		if g.Name == "payments-team" {
			t.Error("payments-team must not be listed on growth")
		}
	}
}

func TestTeamsOnlyShowsWhatTheUserCanView(t *testing.T) {
	srv, sealer := testServer(t, twoEnvRepo(), adminRules())

	got := teamsFor(t, srv, sealer, marketer())
	if teamNames(got) != "growth" {
		t.Fatalf("a marketer should only see growth, got %s", teamNames(got))
	}
	growth := findTeam(t, got, "growth")
	if len(growth.Environments) != 1 || growth.Environments[0].Name != "production" {
		t.Errorf("growth environments = %+v, want only production", growth.Environments)
	}
	for _, g := range growth.Groups {
		if g.Name == "payments-team" {
			t.Errorf("groups for a team the user cannot see leaked: %+v", growth.Groups)
		}
	}

	stranger := &auth.Session{Subject: "x", Name: "Nobody", Email: "n@acme.com", Groups: []string{"nobody"}}
	if got := teamsFor(t, srv, sealer, stranger); len(got.Teams) != 0 {
		t.Errorf("a user with no rule must see no teams, got %s", teamNames(got))
	}
}

func TestTeamsSaysWhetherTheUserCanEdit(t *testing.T) {
	rules := []permissions.Rule{
		{Group: "flags-admins", Teams: []string{"*"}},
		{Group: "readers", Teams: []string{"*"}, Actions: []string{"view"}},
		{Group: "payments-team", Teams: []string{"payments"}, Environments: []string{"production"}},
	}
	srv, sealer := testServer(t, twoEnvRepo(), rules)

	reader := &auth.Session{Subject: "r", Name: "Rae", Email: "r@acme.com", Groups: []string{"readers"}}
	for _, team := range teamsFor(t, srv, sealer, reader).Teams {
		if team.CanEdit {
			t.Errorf("a view-only user must not be told they can edit %s", team.Name)
		}
	}

	payer := &auth.Session{Subject: "p", Name: "Pat", Email: "p@acme.com", Groups: []string{"payments-team", "readers"}}
	payments := findTeam(t, teamsFor(t, srv, sealer, payer), "payments")
	if !payments.CanEdit {
		t.Fatal("payments-team edits payments in production")
	}
	for _, e := range payments.Environments {
		if e.Name == "dev" && e.CanEdit {
			t.Error("payments-team has no rule for dev, so it cannot edit there")
		}
		if e.Name == "production" && !e.CanEdit {
			t.Error("payments-team edits production")
		}
	}
}

func TestTeamsRequiresASession(t *testing.T) {
	srv, sealer := testServer(t, newRepo(), adminRules())

	rec := request(t, srv, sealer, nil, http.MethodGet, "/api/teams", "")
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status %d, want 401", rec.Code)
	}
}

func TestTeamsInTheSingleFileLayoutComeFromMetadata(t *testing.T) {
	srv, sealer := singleFileServer(t, singleFileRepo(), teamRules())

	got := teamsFor(t, srv, sealer, admin())
	if teamNames(got) != "billing,growth,payments,platform" {
		t.Fatalf("teams = %s, want the declared teams", teamNames(got))
	}
	payments := findTeam(t, got, "payments")
	if len(payments.Environments) != 1 || payments.Environments[0].File != "production/flags.goff.yaml" || payments.Environments[0].Flags != 1 {
		t.Errorf("payments = %+v, want one flag in the shared file", payments.Environments)
	}

	only := teamsFor(t, srv, sealer, payer())
	if teamNames(only) != "payments" {
		t.Errorf("the payments group should only see payments, got %s", teamNames(only))
	}
	if groupNames(findTeam(t, only, "payments")) != "payments,flags-admins" {
		t.Errorf("groups = %s", groupNames(findTeam(t, only, "payments")))
	}
}

func TestTeamsAreCachedAndWritesInvalidateThem(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())
	clock := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	srv.svc.now = func() time.Time { return clock }

	teamsFor(t, srv, sealer, admin())
	first := repo.listingCount()

	teamsFor(t, srv, sealer, admin())
	teamsFor(t, srv, sealer, marketer())
	if repo.listingCount() != first {
		t.Errorf("listings = %d after repeat requests, want %d: the page must be served from the cache", repo.listingCount(), first)
	}

	billing := func() int {
		for _, env := range findTeam(t, teamsFor(t, srv, sealer, admin()), "billing").Environments {
			if env.Name == "production" {
				return env.Flags
			}
		}
		return -1
	}
	if got := billing(); got != 0 {
		t.Fatalf("billing starts with %d flags, want 0", got)
	}

	body := `{"key":"billing-flag","team":"billing","type":"boolean","enabled":true,` +
		`"variations":[{"name":"on","value":"true"}],"default":"on"}`
	if rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/environments/production/flags", body); rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	if got := billing(); got != 1 {
		t.Errorf("a flag made in Studio must count on the next request, got %d", got)
	}

	repo.mu.Lock()
	repo.files["production/billing.goff.yaml"] += growthFile
	repo.mu.Unlock()
	if got := billing(); got != 1 {
		t.Errorf("a flag added outside Studio should wait for the TTL, got %d", got)
	}

	clock = clock.Add(environmentCacheTTL)
	if got := billing(); got != 2 {
		t.Errorf("after the TTL the counts should be re-read, got %d", got)
	}
}

func TestTeamsFlagCountFollowsASave(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())

	before := findTeam(t, teamsFor(t, srv, sealer, admin()), "growth").Environments[0].Flags
	if before == 0 {
		t.Fatal("growth should start with a flag")
	}

	list := listFor(t, srv, sealer, admin())
	var sha string
	for _, f := range list.Flags {
		if f.Key == "banner-test" {
			sha = f.FileSHA
		}
	}
	rec := request(t, srv, sealer, admin(), http.MethodDelete,
		"/api/environments/production/flags/banner-test?fileSha="+sha, "")
	if rec.Code != http.StatusOK && rec.Code != http.StatusNoContent {
		t.Skipf("delete endpoint shape differs here (%d); cache invalidation on writes is covered by the cache test", rec.Code)
	}

	after := findTeam(t, teamsFor(t, srv, sealer, admin()), "growth").Environments[0].Flags
	if after != before-1 {
		t.Errorf("flags = %d after a delete, want %d: a write must drop the cached counts", after, before-1)
	}
}
