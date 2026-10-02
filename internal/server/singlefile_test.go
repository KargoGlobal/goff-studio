package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/go-feature-flag/studio/internal/auth"
	"github.com/go-feature-flag/studio/internal/config"
	"github.com/go-feature-flag/studio/internal/goff"
	"github.com/go-feature-flag/studio/internal/permissions"
	"github.com/go-feature-flag/studio/internal/storage"
)

const sharedFile = `# Feature flags for production.
checkout:
  variations:
    on: true
    off: false
  defaultRule:
    variation: "off"
  metadata:
    team: payments
banner:
  variations:
    on: true
    off: false
  defaultRule:
    variation: "on"
  metadata:
    team: growth
`

func singleFileRepo() *repoState {
	return &repoState{
		files: map[string]string{"production/flags.goff.yaml": sharedFile},
		shas:  map[string]string{"production/flags.goff.yaml": "sha-shared"},
	}
}

func singleFileServer(t *testing.T, repo *repoState, rules []permissions.Rule) (*Server, *auth.Sealer) {
	t.Helper()
	srv, sealer := testServer(t, repo, rules)
	srv.cfg.Layout = config.LayoutSingleFile
	return srv, sealer
}

func payer() *auth.Session {
	return &auth.Session{Subject: "okta|pay", Name: "Pat Payments", Email: "pat@acme.com", Groups: []string{"payments"}}
}

func teamRules() []permissions.Rule {
	return []permissions.Rule{
		{Group: "flags-admins", Allow: []string{"*"}},
		{Group: "payments", Allow: []string{"payments"}},
	}
}

func listFor(t *testing.T, srv *Server, sealer *auth.Sealer, sess *auth.Session) ListResult {
	t.Helper()
	rec := request(t, srv, sealer, sess, http.MethodGet, "/api/environments/production/flags", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list failed: %d: %s", rec.Code, rec.Body)
	}
	var list ListResult
	decode(t, rec, &list)
	return list
}

func TestSingleFileListsOnlyTheTeamsFlagsYouMayView(t *testing.T) {
	srv, sealer := singleFileServer(t, singleFileRepo(), teamRules())

	list := listFor(t, srv, sealer, payer())
	if keys := flagKeys(list); len(keys) != 1 || keys[0] != "checkout" {
		t.Fatalf("payments should only see its own flag, got %v", keys)
	}
	if len(list.Teams) != 1 || list.Teams[0].Name != "payments" || list.Teams[0].File != "production/flags.goff.yaml" {
		t.Errorf("teams = %+v, want payments in the shared file", list.Teams)
	}

	all := listFor(t, srv, sealer, admin())
	if len(flagKeys(all)) != 2 || len(all.Teams) != 2 {
		t.Errorf("admin should see both flags and both teams, got %v and %+v", flagKeys(all), all.Teams)
	}
}

func flagKeys(list ListResult) []string {
	var out []string
	for _, f := range list.Flags {
		out = append(out, f.Key)
	}
	return out
}

func TestSingleFileToggleIsCheckedAgainstTheFlagsTeam(t *testing.T) {
	repo := singleFileRepo()
	srv, sealer := singleFileServer(t, repo, teamRules())
	warmCache(t, srv, sealer, payer(), "production")

	rec := request(t, srv, sealer, payer(), http.MethodPost, "/api/environments/production/flags/checkout/state", `{"enabled":false,"fileSha":"sha-shared"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("payments should toggle its own flag: %d: %s", rec.Code, rec.Body)
	}

	rec = request(t, srv, sealer, payer(), http.MethodPost, "/api/environments/production/flags/banner/state", `{"enabled":false,"fileSha":"sha-shared-next"}`)
	if rec.Code != http.StatusNotFound && rec.Code != http.StatusForbidden {
		t.Fatalf("payments must not toggle a growth flag in the same file: %d: %s", rec.Code, rec.Body)
	}
	if strings.Contains(repo.files["production/flags.goff.yaml"], "banner:\n  variations:\n    on: true\n    off: false\n  defaultRule:\n    variation: \"on\"\n  metadata:\n    team: growth\n  disable: true") {
		t.Error("the growth flag was changed")
	}
}

func TestSingleFileCreateWritesIntoTheSharedFileWithANewTeam(t *testing.T) {
	repo := singleFileRepo()
	srv, sealer := singleFileServer(t, repo, adminRules())
	warmCache(t, srv, sealer, admin(), "production")

	body := `{"key":"billing-flag","team":"billing","type":"boolean","enabled":true,` +
		`"variations":[{"name":"on","value":"true"},{"name":"off","value":"false"}],"default":"off","fileSha":"sha-shared"}`
	rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/environments/production/flags", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if _, ok := repo.files["production/billing.goff.yaml"]; ok {
		t.Error("the single-file layout must never create a per-team file")
	}
	flags := mustLoad(t, repo, "production/flags.goff.yaml")
	if len(flags) != 3 {
		t.Fatalf("want three flags in the shared file, got %v", keysOf(flags))
	}
	if stored := repo.files["production/flags.goff.yaml"]; !strings.Contains(stored, "team: billing") || !strings.HasPrefix(stored, "# Feature flags for production.") {
		t.Errorf("the new flag must carry its team and keep the file's comment:\n%s", stored)
	}
}

func TestSingleFileCreateIsCheckedAgainstTheRequestedTeam(t *testing.T) {
	repo := singleFileRepo()
	srv, sealer := singleFileServer(t, repo, teamRules())
	warmCache(t, srv, sealer, payer(), "production")

	body := `{"key":"sneaky","team":"growth","type":"boolean","enabled":true,` +
		`"variations":[{"name":"on","value":"true"}],"default":"on","fileSha":"sha-shared"}`
	rec := request(t, srv, sealer, payer(), http.MethodPost, "/api/environments/production/flags", body)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403: %s", rec.Code, rec.Body)
	}
	if strings.Contains(repo.files["production/flags.goff.yaml"], "sneaky") {
		t.Error("a forbidden create was written")
	}
}

func TestSingleFileMovingAFlagToAnotherTeamNeedsRightsOnBoth(t *testing.T) {
	repo := singleFileRepo()
	srv, _ := singleFileServer(t, repo, teamRules())

	moveTo := func(team string) func(f *goff.Flag) {
		return func(f *goff.Flag) { f.Metadata = map[string]any{"team": team} }
	}
	_, err := srv.svc.Save(t.Context(), *payer(), SaveRequest{
		Environment: "production", Key: "checkout", File: "production/flags.goff.yaml",
		Action: permissions.EditRules, Summary: "moved", Mutate: moveTo("growth"),
	})
	if err != ErrForbidden {
		t.Fatalf("moving a flag into a team you cannot write must be forbidden, got %v", err)
	}

	_, err = srv.svc.Save(t.Context(), *payer(), SaveRequest{
		Environment: "production", Key: "banner", File: "production/flags.goff.yaml",
		Action: permissions.EditRules, Summary: "taken", Mutate: moveTo("payments"),
	})
	if err != ErrForbidden {
		t.Fatalf("pulling another team's flag into yours must be forbidden, got %v", err)
	}
	if repo.files["production/flags.goff.yaml"] != sharedFile {
		t.Error("a forbidden move was written")
	}

	if _, err := srv.svc.Save(t.Context(), *admin(), SaveRequest{
		Environment: "production", Key: "banner", File: "production/flags.goff.yaml",
		Action: permissions.EditRules, Summary: "moved", Mutate: moveTo("payments"),
	}); err != nil {
		t.Fatalf("an admin may move flags between teams: %v", err)
	}
}

func TestSingleFileNewTeamCreatesNoFile(t *testing.T) {
	repo := singleFileRepo()
	srv, sealer := singleFileServer(t, repo, adminRules())

	rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/environments/production/teams", `{"name":"billing"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if len(repo.files) != 1 {
		t.Errorf("a team is only a label in the single-file layout, but files are now %d", len(repo.files))
	}

	rec = request(t, srv, sealer, payer(), http.MethodPost, "/api/environments/production/teams", `{"name":"billing"}`)
	if rec.Code != http.StatusForbidden {
		t.Errorf("creating a team you could not write must still be forbidden, got %d", rec.Code)
	}
}

func TestSingleFileNewEnvironmentIgnoresTheRequestedFileName(t *testing.T) {
	repo := singleFileRepo()
	srv, sealer := singleFileServer(t, repo, adminRules())

	rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/environments", `{"name":"staging","file":"payments"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if _, ok := repo.files["staging/flags.goff.yaml"]; !ok {
		t.Errorf("want staging/flags.goff.yaml, files are %v", keysOfMap(repo.files))
	}
}

func keysOfMap(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestSingleFileMeReportsTheLayout(t *testing.T) {
	srv, sealer := singleFileServer(t, singleFileRepo(), adminRules())
	rec := request(t, srv, sealer, admin(), http.MethodGet, "/api/me", "")
	if !strings.Contains(rec.Body.String(), `"layout":"single-file"`) {
		t.Errorf("/api/me should report the layout, got %s", rec.Body)
	}
}

func TestSingleFileEmptyEnvironmentStillOffersCreate(t *testing.T) {
	repo := singleFileRepo()
	repo.files["production/flags.goff.yaml"] = ""
	srv, sealer := singleFileServer(t, repo, teamRules())

	if list := listFor(t, srv, sealer, payer()); !list.CanCreate || len(list.Teams) != 0 {
		t.Errorf("an empty environment has no teams but must still allow create, got canCreate=%v teams=%+v", list.CanCreate, list.Teams)
	}

	viewer := &auth.Session{Subject: "okta|v", Groups: []string{"viewers"}}
	srv, sealer = singleFileServer(t, repo, append(teamRules(), permissions.Rule{Group: "viewers", Allow: []string{"*"}, Actions: []string{"view"}}))
	if list := listFor(t, srv, sealer, viewer); list.CanCreate {
		t.Error("a view-only group must not be offered create")
	}
}

func historyMessages(t *testing.T, srv *Server, sealer *auth.Sealer, key string) []string {
	t.Helper()
	rec := request(t, srv, sealer, admin(), http.MethodGet, "/api/environments/production/flags/"+key+"/history", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var commits []storage.Commit
	_ = json.Unmarshal(rec.Body.Bytes(), &commits)
	out := make([]string, 0, len(commits))
	for _, c := range commits {
		out = append(out, c.Message)
	}
	return out
}

func TestSharedFileHistoryShowsOnlyTheFlagsOwnChanges(t *testing.T) {
	repo := singleFileRepo()
	repo.commits = []string{
		"[production] flags/new-checkout: enabled",
		"[production] flags/checkout: disabled",
		"[production] flags/banner: rule \"checkout page\" updated",
		"[production] flags/old-checkout: renamed to checkout",
		"[production] flags/checkout-v2: created",
		"tidy checkout and banner by hand",
		"object version abc",
	}
	srv, sealer := singleFileServer(t, repo, adminRules())

	got := historyMessages(t, srv, sealer, "checkout")
	want := []string{
		"[production] flags/checkout: disabled",
		"[production] flags/old-checkout: renamed to checkout",
		"tidy checkout and banner by hand",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("history =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestSharedFileHistoryIgnoresTheFileAndEnvironmentNames(t *testing.T) {
	repo := singleFileRepo()
	repo.commits = []string{
		"[production] flags/checkout: disabled",
		"[production] flags/banner: enabled",
	}
	srv, sealer := singleFileServer(t, repo, adminRules())
	repo.files["production/flags.goff.yaml"] += "flags:\n  variations:\n    on: true\n  defaultRule:\n    variation: \"on\"\n  metadata:\n    team: growth\n" +
		"production:\n  variations:\n    on: true\n  defaultRule:\n    variation: \"on\"\n  metadata:\n    team: growth\n"

	for _, key := range []string{"flags", "production"} {
		if got := historyMessages(t, srv, sealer, key); len(got) != 0 {
			t.Errorf("%s history should be empty, got %q", key, got)
		}
	}
}

func TestQuietFlagHistoryReadsPastOtherFlagsChanges(t *testing.T) {
	repo := singleFileRepo()
	for i := 0; i < 50; i++ {
		repo.commits = append(repo.commits, fmt.Sprintf("[production] flags/banner: change %d", i))
	}
	repo.commits = append(repo.commits, "[production] flags/checkout: created")
	srv, sealer := singleFileServer(t, repo, adminRules())

	got := historyMessages(t, srv, sealer, "checkout")
	if len(got) != 1 || got[0] != "[production] flags/checkout: created" {
		t.Errorf("history = %q", got)
	}
	if strings.Join(repo.pages, ",") != fmt.Sprintf("%d,%d", DefaultHistory, MaxHistoryScan) {
		t.Errorf("pages requested = %v", repo.pages)
	}
}

func TestBusyFlagHistoryStopsAtOneRead(t *testing.T) {
	repo := singleFileRepo()
	for i := 0; i < 40; i++ {
		repo.commits = append(repo.commits, fmt.Sprintf("[production] flags/checkout: change %d", i))
	}
	srv, sealer := singleFileServer(t, repo, adminRules())

	if got := historyMessages(t, srv, sealer, "checkout"); len(got) != DefaultHistory {
		t.Errorf("got %d commits, want %d", len(got), DefaultHistory)
	}
	if len(repo.pages) != 1 {
		t.Errorf("pages requested = %v", repo.pages)
	}
}
