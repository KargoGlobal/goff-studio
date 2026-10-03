package server

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-feature-flag/studio/internal/auth"
	"github.com/go-feature-flag/studio/internal/permissions"
)

type meBody struct {
	Environments          []Environment `json:"environments"`
	CanCreateEnvironments bool          `json:"canCreateEnvironments"`
	HasTeams              bool          `json:"hasTeams"`
}

func me(t *testing.T, srv *Server, sealer *auth.Sealer, sess *auth.Session) meBody {
	t.Helper()
	rec := request(t, srv, sealer, sess, http.MethodGet, "/api/me", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("/api/me status %d: %s", rec.Code, rec.Body)
	}
	var out meBody
	decode(t, rec, &out)
	return out
}

func (r *repoState) listingCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.listings
}

func names(envs []Environment) []string {
	out := make([]string, 0, len(envs))
	for _, e := range envs {
		out = append(out, e.Name)
	}
	return out
}

func sameNames(got []Environment, want ...string) bool {
	return strings.Join(names(got), ",") == strings.Join(want, ",")
}

func TestEnvironmentsComeFromFoldersHoldingFlagFiles(t *testing.T) {
	repo := newRepo()
	repo.files["staging/payments.goff.yaml"] = paymentsFile
	repo.files["dev/growth.yml"] = growthFile
	repo.files["docs/README.md"] = "# not flags\n"
	repo.files[".github/workflows/ci.yaml"] = "on: push\n"
	repo.files["production/eu/nested.goff.yaml"] = growthFile
	srv, sealer := testServer(t, repo, adminRules())

	got := me(t, srv, sealer, admin()).Environments
	if !sameNames(got, "dev", "production", "staging") {
		t.Errorf("environments = %v, want dev, production, staging: sorted, no docs/ (no flag files), no dot folders", names(got))
	}
}

func TestEnvironmentNamesAreTheFolderNamesUnchanged(t *testing.T) {
	repo := newRepo()
	repo.files["QA-eu/flags.goff.yaml"] = growthFile
	srv, sealer := testServer(t, repo, adminRules())

	got := me(t, srv, sealer, admin()).Environments
	if !sameNames(got, "QA-eu", "production") {
		t.Errorf("environments = %v, want the folder names exactly as stored", names(got))
	}
}

func TestProtectedComesFromConfig(t *testing.T) {
	repo := newRepo()
	repo.files["staging/payments.goff.yaml"] = paymentsFile
	srv, sealer := testServer(t, repo, adminRules())

	for _, env := range me(t, srv, sealer, admin()).Environments {
		want := env.Name == "production"
		if env.Protected != want {
			t.Errorf("%s protected = %v, want %v", env.Name, env.Protected, want)
		}
	}
}

func TestNoEnvironmentsIsAnEmptyListNotAnError(t *testing.T) {
	repo := newRepo()
	repo.files = map[string]string{}
	repo.shas = map[string]string{}
	srv, sealer := testServer(t, repo, adminRules())

	rec := request(t, srv, sealer, admin(), http.MethodGet, "/api/me", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("an empty repository must not break /api/me, got %d: %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `"environments":[]`) {
		t.Errorf("environments must serialise as [] so the UI can show its empty state:\n%s", rec.Body)
	}
}

func TestCreatedEnvironmentAppearsInTheNextMe(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())

	if got := me(t, srv, sealer, admin()).Environments; !sameNames(got, "production") {
		t.Fatalf("environments before = %v", names(got))
	}

	rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/environments", `{"name":"qa"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status %d: %s", rec.Code, rec.Body)
	}

	if got := me(t, srv, sealer, admin()).Environments; !sameNames(got, "production", "qa") {
		t.Errorf("environments after create = %v, want qa to show up straight away", names(got))
	}
}

func TestCreatingAnExistingEnvironmentIsRefused(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())

	rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/environments", `{"name":"production"}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "already exists") {
		t.Errorf("status %d: %s", rec.Code, rec.Body)
	}
	if len(repo.puts) != 0 {
		t.Error("nothing should have been committed")
	}
}

func TestEnvironmentListIsCachedAndCreateInvalidatesIt(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())
	clock := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	srv.svc.now = func() time.Time { return clock }

	me(t, srv, sealer, admin())
	first := repo.listingCount()
	if first == 0 {
		t.Fatal("the first /api/me should list storage")
	}

	me(t, srv, sealer, admin())
	me(t, srv, sealer, marketer())
	if repo.listingCount() != first {
		t.Errorf("listings = %d after repeat /api/me, want %d: page loads must be served from the cache", repo.listingCount(), first)
	}

	repo.mu.Lock()
	repo.files["outside/flags.goff.yaml"] = growthFile
	repo.mu.Unlock()
	if got := me(t, srv, sealer, admin()).Environments; !sameNames(got, "production") {
		t.Errorf("a folder made outside Studio should wait for the TTL, got %v", names(got))
	}

	clock = clock.Add(environmentCacheTTL)
	if got := me(t, srv, sealer, admin()).Environments; !sameNames(got, "outside", "production") {
		t.Errorf("after the TTL the list should be re-read, got %v", names(got))
	}

	before := repo.listingCount()
	me(t, srv, sealer, admin())
	if repo.listingCount() != before {
		t.Fatal("expected a cache hit")
	}
	if rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/environments", `{"name":"qa"}`); rec.Code != http.StatusCreated {
		t.Fatalf("create status %d: %s", rec.Code, rec.Body)
	}
	afterCreate := repo.listingCount()
	if got := me(t, srv, sealer, admin()).Environments; !sameNames(got, "outside", "production", "qa") {
		t.Errorf("environments = %v", names(got))
	}
	if repo.listingCount() == afterCreate {
		t.Error("creating an environment must invalidate the cache so the next /api/me re-reads storage")
	}
}

func TestMeFiltersDiscoveredEnvironmentsByPermission(t *testing.T) {
	repo := newRepo()
	repo.files["dev/growth.goff.yaml"] = growthFile
	srv, sealer := testServer(t, repo, adminRules())

	if got := me(t, srv, sealer, marketer()).Environments; !sameNames(got, "production") {
		t.Errorf("marketing may only view production, got %v", names(got))
	}
}

func TestCanCreateEnvironments(t *testing.T) {
	rules := []permissions.Rule{
		{Group: "flags-admins", Teams: []string{"*"}},
		{Group: "marketing", Teams: []string{"growth"}, Environments: []string{"production"}, Actions: []string{"create"}},
	}
	srv, sealer := testServer(t, newRepo(), rules)

	if !me(t, srv, sealer, admin()).CanCreateEnvironments {
		t.Error("an unrestricted create rule should allow creating environments")
	}
	if me(t, srv, sealer, marketer()).CanCreateEnvironments {
		t.Error("a create rule limited to production is not a grant to create new environments")
	}

	rec := request(t, srv, sealer, marketer(), http.MethodPost, "/api/environments", `{"name":"qa"}`)
	if rec.Code != http.StatusForbidden {
		t.Errorf("the server check must still refuse, got %d: %s", rec.Code, rec.Body)
	}
}
