package server

import (
	"net/http"
	"strings"
	"testing"

	"github.com/go-feature-flag/studio/internal/goff"
)

func TestValidTeamRejectsYAMLInjection(t *testing.T) {
	// A newline in a team name lands in the seed file and becomes a real top-level key,
	// bypassing validKey entirely. Verified against goff.Parse before this check existed.
	payloads := []string{
		"a\nbackdoor:\n#",
		"a\nb",
		"a\rb",
		"a\x00b",
		"a\vb",
		"a\u2028b",
	}

	for _, name := range payloads {
		t.Run(strings.ReplaceAll(name, "\n", "\\n"), func(t *testing.T) {
			if err := validTeam(name); err == nil {
				t.Fatalf("validTeam accepted %q, which can inject YAML", name)
			}
		})
	}
}

func TestInjectedTeamNameWouldHaveCreatedAFlag(t *testing.T) {
	seed := "# Feature flags owned by " + "a\nbackdoor:\n#" + " in production.\n"
	flags, _, err := goff.New().Parse("x.yaml", []byte(seed))
	if err != nil {
		t.Skipf("payload no longer parses: %v", err)
	}
	if len(flags) == 0 {
		t.Skip("payload no longer yields a flag")
	}
	if err := validTeam("a\nbackdoor:\n#"); err == nil {
		t.Errorf("this payload parses to flag %q, so it must be rejected", flags[0].Key)
	}
}

func TestSeedFileNameRefusesTraversalAndNesting(t *testing.T) {
	for _, bad := range []string{
		"../production/payments",
		"../../etc/passwd.yaml",
		"sub/dir/flags",
		"..",
		".",
		".hidden",
		`back\slash`,
		"with space",
		"line\nbreak",
		strings.Repeat("a", MaxNameLength+1),
	} {
		t.Run(bad, func(t *testing.T) {
			if _, err := seedFileName(bad); err == nil {
				t.Errorf("seedFileName accepted %q", bad)
			}
		})
	}
}

func TestSeedFileNameNormalisesToOneExtension(t *testing.T) {
	cases := map[string]string{
		"":                  "flags.goff.yaml",
		"payments":          "payments.goff.yaml",
		"payments.yaml":     "payments.goff.yaml",
		"payments.yml":      "payments.goff.yaml",
		"payments.goff.yml": "payments.goff.yaml",
		"  payments  ":      "payments.goff.yaml",
	}

	for in, want := range cases {
		got, err := seedFileName(in)
		if err != nil {
			t.Errorf("seedFileName(%q) errored: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("seedFileName(%q) = %q, want %q", in, got, want)
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

func TestCreateEnvironmentRefusesATraversingSeedFile(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())
	before := repo.files["production/payments.goff.yaml"]

	rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/environments",
		`{"name":"tmp","file":"../production/payments"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400: %s", rec.Code, rec.Body)
	}
	if repo.files["production/payments.goff.yaml"] != before {
		t.Error("a traversing seed file must not touch another environment")
	}
	for path := range repo.files {
		if strings.Contains(path, "..") {
			t.Errorf("a path with .. was written: %s", path)
		}
	}
}

func TestCreateTeamRefusesAnInjectingName(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())

	rec := request(t, srv, sealer, admin(), http.MethodPost,
		"/api/environments/production/teams", "{\"name\":\"a\\nbackdoor:\\n#\"}")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400: %s", rec.Code, rec.Body)
	}
	for _, content := range repo.files {
		if strings.Contains(content, "backdoor") {
			t.Errorf("an injected key reached a file:\n%s", content)
		}
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
