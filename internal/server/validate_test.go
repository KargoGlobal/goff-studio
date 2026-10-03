package server

import (
	"net/http"
	"strings"
	"testing"
)

func TestOnlyDeclaredTeamsAreAccepted(t *testing.T) {
	srv, _ := testServer(t, newRepo(), adminRules())
	for _, name := range []string{"a\nbackdoor:\n#", "a\u2028b", "../production/payments", "nope", "flags"} {
		if err := srv.svc.validTeam(name); err == nil {
			t.Errorf("validTeam accepted undeclared team %q", name)
		}
	}
	for _, name := range []string{"", "payments", " growth "} {
		if err := srv.svc.validTeam(name); err != nil {
			t.Errorf("validTeam rejected %q: %v", name, err)
		}
	}
}

func TestValidEnvironmentRefusesPathTricks(t *testing.T) {
	for _, bad := range []string{"", "..", "../../etc", "a/b", `a\b`, ".hidden", "with space", "a\nb"} {
		if err := validEnvironment(bad); err == nil {
			t.Errorf("validEnvironment accepted %q", bad)
		}
	}
	for _, good := range []string{"production", "staging", "pre-prod", "env2"} {
		if err := validEnvironment(good); err != nil {
			t.Errorf("validEnvironment rejected %q: %v", good, err)
		}
	}
}

func TestValidPercentagesBoundsEachShare(t *testing.T) {
	bad := []map[string]float64{
		{"on": -50, "off": 150},
		{"on": -1},
		{"on": 101},
		{"on": 0, "off": 0},
		{"a\nb": 50, "off": 50},
	}
	for _, p := range bad {
		if err := validPercentages(p); err == nil {
			t.Errorf("validPercentages accepted %v", p)
		}
	}

	good := []map[string]float64{
		{"on": 50, "off": 50},
		{"on": 100},
		{"on": 0, "off": 100},
		{"on": 33.3, "off": 66.7},
	}
	for _, p := range good {
		if err := validPercentages(p); err != nil {
			t.Errorf("validPercentages rejected %v: %v", p, err)
		}
	}
}

func TestBoundedLimitKeepsBackendsSafe(t *testing.T) {
	cases := map[int]int{
		0:          DefaultHistory,
		-1:         DefaultHistory,
		5:          5,
		MaxHistory: MaxHistory,
		2147483648: MaxHistory,
		4294967296: MaxHistory,
	}
	for in, want := range cases {
		if got := boundedLimit(in); got != want {
			t.Errorf("boundedLimit(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestValidQueryBoundsLength(t *testing.T) {
	if err := validQuery(strings.Repeat("a", MaxQueryLength+1)); err == nil {
		t.Error("an unbounded query should be refused")
	}
	if err := validQuery("(tier eq \"gold\")"); err != nil {
		t.Errorf("a normal query was refused: %v", err)
	}
	if err := validQuery("(tier eq \"go\nld\")"); err == nil {
		t.Error("a query with a line break should be refused")
	}
}

func TestValidNameBoundsLength(t *testing.T) {
	if err := validName("rule name", strings.Repeat("a", MaxNameLength+1)); err == nil {
		t.Error("an over-long name should be refused")
	}
	if err := validName("rule name", ""); err == nil {
		t.Error("an empty name should be refused")
	}
}

func TestCreateEnvironmentIgnoresAClientSuppliedSeedFile(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())
	before := repo.files["production/payments.goff.yaml"]

	rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/environments",
		`{"name":"tmp","file":"../production/payments"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if _, ok := repo.files["tmp/flags.goff.yaml"]; !ok {
		t.Errorf("want tmp/flags.goff.yaml, files are %v", keysOfMap(repo.files))
	}
	if repo.files["production/payments.goff.yaml"] != before {
		t.Error("a supplied seed file must not touch another environment")
	}
}

func TestReadHandlersRefuseATraversingEnvironment(t *testing.T) {
	srv, sealer := testServer(t, newRepo(), adminRules())

	for _, target := range []string{
		"/api/environments/..%2f..%2fetc/flags",
		"/api/environments/../flags",
	} {
		rec := request(t, srv, sealer, admin(), http.MethodGet, target, "")
		if rec.Code == http.StatusOK {
			t.Errorf("%s returned 200: %s", target, rec.Body)
		}
	}
}

func TestRolloutRefusesPercentagesOutOfRange(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())
	before := repo.files["production/payments.goff.yaml"]

	rec := request(t, srv, sealer, admin(), http.MethodPost,
		"/api/environments/production/flags/new-checkout/rollout",
		`{"percentage":{"on":-50,"off":150}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400: %s", rec.Code, rec.Body)
	}
	if repo.files["production/payments.goff.yaml"] != before {
		t.Error("an out-of-range rollout must not be written")
	}
}

func TestRolloutRefusesAnUnknownVariation(t *testing.T) {
	srv, sealer := testServer(t, newRepo(), adminRules())

	rec := request(t, srv, sealer, admin(), http.MethodPost,
		"/api/environments/production/flags/new-checkout/rollout",
		`{"percentage":{"nope":100}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400: %s", rec.Code, rec.Body)
	}
}

func TestRolloutRefusesAnUnknownRule(t *testing.T) {
	srv, sealer := testServer(t, newRepo(), adminRules())

	rec := request(t, srv, sealer, admin(), http.MethodPost,
		"/api/environments/production/flags/new-checkout/rollout",
		`{"ruleName":"ghost","percentage":{"on":50,"off":50}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a silent no-op is worse than an error; status %d: %s", rec.Code, rec.Body)
	}
}

func TestSaveRuleRefusesAnUnknownRule(t *testing.T) {
	srv, sealer := testServer(t, newRepo(), adminRules())

	rec := request(t, srv, sealer, admin(), http.MethodPost,
		"/api/environments/production/flags/new-checkout/rule",
		`{"ruleName":"ghost","query":"(a eq \"b\")"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400: %s", rec.Code, rec.Body)
	}
}

func TestOversizedBodyIsRefused(t *testing.T) {
	srv, sealer := testServer(t, newRepo(), adminRules())

	huge := `{"percentage":{"on":50,"off":50},"pad":"` + strings.Repeat("a", MaxBodyBytes+1) + `"}`
	rec := request(t, srv, sealer, admin(), http.MethodPost,
		"/api/environments/production/flags/new-checkout/rollout", huge)
	if rec.Code == http.StatusOK {
		t.Errorf("an oversized body was accepted: %d", rec.Code)
	}
}

func TestHistoryLimitIsBounded(t *testing.T) {
	srv, sealer := testServer(t, newRepo(), adminRules())

	for _, limit := range []string{"4294967296", "-1", "999999"} {
		rec := request(t, srv, sealer, admin(), http.MethodGet,
			"/api/environments/production/flags/new-checkout/history?limit="+limit, "")
		if rec.Code != http.StatusOK {
			t.Errorf("limit=%s returned %d: %s", limit, rec.Code, rec.Body)
		}
	}
}

func TestInvalidFlagDataIsABadRequestNotAServerError(t *testing.T) {
	srv, sealer := testServer(t, newRepo(), adminRules())

	rec := request(t, srv, sealer, admin(), http.MethodPut,
		"/api/environments/production/flags/new-checkout/variations",
		`{"type":"boolean","variations":[{"name":"on","value":"true"}],"default":"missing"}`)
	if rec.Code >= 500 {
		t.Errorf("bad input should not be a 5xx; got %d: %s", rec.Code, rec.Body)
	}
}
