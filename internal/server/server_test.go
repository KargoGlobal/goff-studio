package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/go-feature-flag/studio/internal/auth"
	"github.com/go-feature-flag/studio/internal/config"
	"github.com/go-feature-flag/studio/internal/githubapp"
	"github.com/go-feature-flag/studio/internal/permissions"
	"github.com/go-feature-flag/studio/internal/storage"
)

const paymentsFile = `# Payment flags
new-checkout:
  variations:
    on: true
    off: false
  targeting:
    - name: gold-cohort
      query: (tier eq "gold") or (account_id eq "42")
      percentage:
        on: 20
        off: 80
  defaultRule:
    variation: "off"
  version: "3.1.0"
`

const growthFile = `banner-test:
  variations:
    on: true
    off: false
  defaultRule:
    variation: "on"
`

const timedFile = `timed:
  variations:
    on: true
    off: false
  defaultRule:
    variation: "off"
  experimentation:
    start: 2026-10-01T00:00:00Z
    end: 2026-11-01T00:00:00Z
`

const rampFile = `ramped:
  variations:
    on: true
    off: false
  targeting:
    - name: ramp
      query: (region eq "us")
      progressiveRollout:
        initial:
          variation: "off"
          percentage: 0
          date: 2026-10-01T00:00:00Z
        end:
          variation: "on"
          percentage: 100
          date: 2026-11-01T00:00:00Z
  defaultRule:
    variation: "off"
`

type repoState struct {
	mu    sync.Mutex
	files map[string]string
	shas  map[string]string
	puts  []map[string]any
}

func newRepo() *repoState {
	return &repoState{
		files: map[string]string{
			"production/payments.goff.yaml": paymentsFile,
			"production/growth.goff.yaml":   growthFile,
		},
		shas: map[string]string{
			"production/payments.goff.yaml": "sha-payments",
			"production/growth.goff.yaml":   "sha-growth",
		},
	}
}

func (r *repoState) server(t *testing.T) *httptest.Server {
	t.Helper()

	handler := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		defer r.mu.Unlock()

		path := strings.TrimPrefix(req.URL.Path, "/repos/acme/flags/contents/")

		if strings.HasPrefix(req.URL.Path, "/repos/acme/flags/commits") {
			_, _ = w.Write([]byte(`[{"sha":"c1","commit":{"message":"[production] payments/new-checkout: disabled","author":{"name":"Jane","email":"jane@x.com","date":"2026-09-22T10:00:00Z"}}}]`))
			return
		}

		if req.Method == http.MethodGet {
			if body, ok := r.files[path]; ok {
				_ = json.NewEncoder(w).Encode(map[string]string{
					"content": base64.StdEncoding.EncodeToString([]byte(body)),
					"sha":     r.shas[path],
				})
				return
			}
			var entries []map[string]string
			for name := range r.files {
				if strings.HasPrefix(name, path+"/") {
					entries = append(entries, map[string]string{
						"name": strings.TrimPrefix(name, path+"/"),
						"path": name,
						"type": "file",
					})
				}
			}
			if entries == nil {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"message":"Not Found"}`))
				return
			}
			_ = json.NewEncoder(w).Encode(entries)
			return
		}

		var payload map[string]any
		_ = json.NewDecoder(req.Body).Decode(&payload)
		// GitHub treats an absent sha as "create only"; it conflicts if the file already exists.
		if sha, sent := payload["sha"]; sent {
			if sha != r.shas[path] {
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(`{"message":"sha mismatch"}`))
				return
			}
		} else if _, exists := r.files[path]; exists {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"message":"path already exists"}`))
			return
		}
		r.puts = append(r.puts, payload)
		decoded, _ := base64.StdEncoding.DecodeString(payload["content"].(string))
		r.files[path] = string(decoded)
		r.shas[path] = r.shas[path] + "-next"
		_ = json.NewEncoder(w).Encode(map[string]any{"commit": map[string]string{"sha": "new-commit"}})
	})

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

func testServer(t *testing.T, repo *repoState, rules []permissions.Rule) (*Server, *auth.Sealer) {
	t.Helper()

	gh := repo.server(t)
	cfg := &config.Config{
		Server: config.Server{SessionSecret: "0123456789abcdef0123"},
		GitHub: config.GitHub{Owner: "acme", Repo: "flags", Branch: "main"},
		Environments: []config.Environment{
			{Name: "dev", Display: "Dev", Order: 1},
			{Name: "production", Display: "Production", Protected: true, Order: 2},
		},
		PollSeconds: 30,
	}

	perms, err := permissions.New(rules)
	if err != nil {
		t.Fatal(err)
	}

	client := githubapp.New(githubapp.Config{
		APIBase: gh.URL, Owner: "acme", Repo: "flags", Branch: "main",
	}, githubapp.StaticToken("tok"), gh.Client())

	sealer, err := auth.NewSealer(cfg.Server.SessionSecret, false)
	if err != nil {
		t.Fatal(err)
	}

	svc := NewService(cfg, storage.NewGitHubBackend(client), perms)
	return New(cfg, svc, nil, sealer, emptyFS{}), sealer
}

type emptyFS struct{}

func (emptyFS) Open(name string) (fs.File, error) { return nil, fs.ErrNotExist }

func request(t *testing.T, srv *Server, sealer *auth.Sealer, sess *auth.Session, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()

	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, target, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, target, nil)
	}

	if sess != nil {
		rec := httptest.NewRecorder()
		if err := sealer.Write(rec, *sess); err != nil {
			t.Fatal(err)
		}
		req.AddCookie(rec.Result().Cookies()[0])
	}

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func admin() *auth.Session {
	return &auth.Session{Subject: "okta|admin", Name: "Ada Admin", Email: "ada@acme.com", Groups: []string{"flags-admins"}}
}

func marketer() *auth.Session {
	return &auth.Session{Subject: "okta|mkt", Name: "Mo Marketer", Email: "mo@acme.com", Groups: []string{"marketing"}}
}

func adminRules() []permissions.Rule {
	return []permissions.Rule{
		{Group: "flags-admins", Allow: []string{"*"}},
		{Group: "marketing", Allow: []string{"growth"}, Environments: []string{"production"}, Actions: []string{"toggle", "rollout"}},
	}
}

func TestUnauthenticatedRequestsAreRejected(t *testing.T) {
	srv, sealer := testServer(t, newRepo(), adminRules())

	for _, target := range []string{
		"/api/me",
		"/api/environments/production/flags",
		"/api/environments/production/flags/new-checkout",
	} {
		rec := request(t, srv, sealer, nil, http.MethodGet, target, "")
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s returned %d, want 401", target, rec.Code)
		}
	}
}

func TestListReturnsFlagsWithSummariesAndActions(t *testing.T) {
	srv, sealer := testServer(t, newRepo(), adminRules())

	rec := request(t, srv, sealer, admin(), http.MethodGet, "/api/environments/production/flags", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	var out ListResult
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Flags) != 2 {
		t.Fatalf("want 2 flags, got %d", len(out.Flags))
	}

	var checkout *FlagView
	for i := range out.Flags {
		if out.Flags[i].Key == "new-checkout" {
			checkout = &out.Flags[i]
		}
	}
	if checkout == nil {
		t.Fatal("new-checkout missing")
	}
	if !strings.Contains(checkout.Summary, "tier equals gold") {
		t.Errorf("summary = %q", checkout.Summary)
	}
	if checkout.FileSHA == "" {
		t.Error("file sha must be returned so the client can detect conflicts")
	}
	if len(checkout.Actions) != len(permissions.AllActions) {
		t.Errorf("admin should get every action, got %v", checkout.Actions)
	}
}

func TestListHidesFilesTheUserCannotSee(t *testing.T) {
	srv, sealer := testServer(t, newRepo(), adminRules())

	rec := request(t, srv, sealer, marketer(), http.MethodGet, "/api/environments/production/flags", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	var out ListResult
	_ = json.Unmarshal(rec.Body.Bytes(), &out)

	for _, f := range out.Flags {
		if f.Key == "new-checkout" {
			t.Error("marketing must not see payments flags")
		}
	}
	if len(out.Flags) != 1 || out.Flags[0].Key != "banner-test" {
		t.Errorf("flags = %+v", out.Flags)
	}
}

func TestMarketingCannotSeeUnpermittedEnvironment(t *testing.T) {
	srv, sealer := testServer(t, newRepo(), adminRules())

	rec := request(t, srv, sealer, marketer(), http.MethodGet, "/api/environments/dev/flags", "")
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestToggleCommitsWithIdentityTrailers(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())

	body := `{"enabled":false,"fileSha":"sha-payments"}`
	rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/environments/production/flags/new-checkout/state", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	var result SaveResult
	_ = json.Unmarshal(rec.Body.Bytes(), &result)
	if result.Commit != "new-commit" {
		t.Errorf("commit = %q", result.Commit)
	}
	if !strings.Contains(result.Message, "30 seconds") {
		t.Errorf("message should carry the configured poll interval, got %q", result.Message)
	}

	if len(repo.puts) != 1 {
		t.Fatalf("want one commit, got %d", len(repo.puts))
	}
	msg := repo.puts[0]["message"].(string)
	for _, want := range []string{
		"[production] payments/new-checkout: disabled",
		"GOFF-Studio-User: ada@acme.com",
		"GOFF-Studio-User-Id: okta|admin",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("commit message missing %q:\n%s", want, msg)
		}
	}

	stored := repo.files["production/payments.goff.yaml"]
	if !strings.Contains(stored, "disable: true") {
		t.Errorf("flag not disabled:\n%s", stored)
	}
	if !strings.Contains(stored, "# Payment flags") {
		t.Error("comment lost")
	}
	if !strings.Contains(stored, `version: "3.1.0"`) {
		t.Errorf("version field not preserved:\n%s", stored)
	}
	if strings.Contains(repo.files["production/growth.goff.yaml"], "disable") {
		t.Error("the other file was modified")
	}
}

func TestToggleDeniedWithoutPermission(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())

	body := `{"enabled":false,"fileSha":"sha-payments"}`
	rec := request(t, srv, sealer, marketer(), http.MethodPost, "/api/environments/production/flags/new-checkout/state", body)
	if rec.Code != http.StatusNotFound && rec.Code != http.StatusForbidden {
		t.Errorf("marketing toggling a payments flag should be refused, got %d: %s", rec.Code, rec.Body)
	}
	if len(repo.puts) != 0 {
		t.Error("nothing should have been committed")
	}
}

func TestRolloutRequiresRolloutPermission(t *testing.T) {
	repo := newRepo()
	rules := []permissions.Rule{
		{Group: "viewers", Allow: []string{"*"}, Actions: []string{"view"}},
	}
	srv, sealer := testServer(t, repo, rules)

	viewer := &auth.Session{Subject: "okta|v", Email: "v@acme.com", Groups: []string{"viewers"}}
	body := `{"percentage":{"on":50,"off":50},"fileSha":"sha-growth"}`
	rec := request(t, srv, sealer, viewer, http.MethodPost, "/api/environments/production/flags/banner-test/rollout", body)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403: %s", rec.Code, rec.Body)
	}
	if len(repo.puts) != 0 {
		t.Error("nothing should have been committed")
	}
}

func TestRolloutUpdatesDefaultPercentage(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())

	body := `{"percentage":{"on":30,"off":70},"fileSha":"sha-growth"}`
	rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/environments/production/flags/banner-test/rollout", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	stored := repo.files["production/growth.goff.yaml"]
	if !strings.Contains(stored, "percentage:") {
		t.Errorf("percentage not written:\n%s", stored)
	}
	msg := repo.puts[0]["message"].(string)
	if !strings.Contains(msg, "default rollout") {
		t.Errorf("commit message = %q", msg)
	}
}

func TestPreviewEvaluatesWithTheRealEngine(t *testing.T) {
	srv, sealer := testServer(t, newRepo(), adminRules())

	body := `{"targetingKey":"user-1","attributes":{"tier":"bronze"}}`
	rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/environments/production/flags/new-checkout/preview", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	var result struct {
		Value  any    `json:"value"`
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &result)
	if result.Value != false {
		t.Errorf("bronze user should get the default, got %v", result.Value)
	}
	if result.Reason != "DEFAULT" {
		t.Errorf("reason = %q", result.Reason)
	}

	match := `{"targetingKey":"user-1","attributes":{"tier":"gold"}}`
	rec = request(t, srv, sealer, admin(), http.MethodPost, "/api/environments/production/flags/new-checkout/preview", match)
	_ = json.Unmarshal(rec.Body.Bytes(), &result)
	if result.Reason != "TARGETING_MATCH_SPLIT" {
		t.Errorf("gold user should hit the rule, got reason %q", result.Reason)
	}
}

func TestHistoryFiltersToTheFlag(t *testing.T) {
	srv, sealer := testServer(t, newRepo(), adminRules())

	rec := request(t, srv, sealer, admin(), http.MethodGet, "/api/environments/production/flags/new-checkout/history", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	var commits []githubapp.Commit
	_ = json.Unmarshal(rec.Body.Bytes(), &commits)
	if len(commits) != 1 || commits[0].Author != "Jane" {
		t.Errorf("commits = %+v", commits)
	}
}

func TestMeReportsOnlyVisibleEnvironments(t *testing.T) {
	srv, sealer := testServer(t, newRepo(), adminRules())

	rec := request(t, srv, sealer, marketer(), http.MethodGet, "/api/me", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}

	var out struct {
		Name         string               `json:"name"`
		Environments []config.Environment `json:"environments"`
		PollSeconds  int                  `json:"pollSeconds"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)

	if out.Name != "Mo Marketer" {
		t.Errorf("name = %q", out.Name)
	}
	if len(out.Environments) != 1 || out.Environments[0].Name != "production" {
		t.Errorf("environments = %+v", out.Environments)
	}
	if !out.Environments[0].Protected {
		t.Error("production should be marked protected so the UI can warn")
	}
	if out.PollSeconds != 30 {
		t.Errorf("pollSeconds = %d", out.PollSeconds)
	}
}

func TestStaleSHAOnADifferentFlagRetriesSilently(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())

	if rec := request(t, srv, sealer, admin(), http.MethodGet, "/api/environments/production/flags", ""); rec.Code != http.StatusOK {
		t.Fatalf("initial list failed: %d", rec.Code)
	}

	repo.mu.Lock()
	repo.files["production/payments.goff.yaml"] = paymentsFile + `unrelated:
  variations:
    on: true
  defaultRule:
    variation: "on"
`
	repo.shas["production/payments.goff.yaml"] = "sha-moved-on"
	repo.mu.Unlock()

	body := `{"enabled":false,"fileSha":"sha-payments"}`
	rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/environments/production/flags/new-checkout/state", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("a change to another flag should retry, got %d: %s", rec.Code, rec.Body)
	}

	var result SaveResult
	_ = json.Unmarshal(rec.Body.Bytes(), &result)
	if !result.Retried {
		t.Error("result should report the retry")
	}

	stored := repo.files["production/payments.goff.yaml"]
	if !strings.Contains(stored, "disable: true") {
		t.Error("the user's change was lost")
	}
	if !strings.Contains(stored, "unrelated:") {
		t.Error("the other user's flag was clobbered")
	}
}

func TestStaleSHAOnTheSameFlagReturns409(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())

	if rec := request(t, srv, sealer, admin(), http.MethodGet, "/api/environments/production/flags", ""); rec.Code != http.StatusOK {
		t.Fatalf("initial list failed: %d", rec.Code)
	}

	repo.mu.Lock()
	repo.files["production/payments.goff.yaml"] = strings.Replace(paymentsFile, `        on: 20
        off: 80`, `        on: 55
        off: 45`, 1)
	repo.shas["production/payments.goff.yaml"] = "sha-someone-else"
	repo.mu.Unlock()

	body := `{"enabled":false,"fileSha":"sha-payments"}`
	rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/environments/production/flags/new-checkout/state", body)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "someone else") {
		t.Errorf("error should be human readable, got %s", rec.Body)
	}
	if len(repo.puts) != 0 {
		t.Error("nothing should have been committed")
	}
}

func TestDuplicateFlagKeyAcrossFilesIsReported(t *testing.T) {
	repo := newRepo()
	repo.files["production/growth.goff.yaml"] = growthFile + `new-checkout:
  variations:
    on: true
  defaultRule:
    variation: "on"
`
	srv, sealer := testServer(t, repo, adminRules())

	rec := request(t, srv, sealer, admin(), http.MethodGet, "/api/environments/production/flags", "")
	var out ListResult
	_ = json.Unmarshal(rec.Body.Bytes(), &out)

	found := false
	for _, b := range out.Broken {
		if b.Key == "new-checkout" && strings.Contains(b.Reason, "unique within an environment") {
			found = true
		}
	}
	if !found {
		t.Errorf("a duplicate key across files must be reported, got %+v", out.Broken)
	}
}

func TestHealthz(t *testing.T) {
	srv, _ := testServer(t, newRepo(), adminRules())
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("healthz = %d", rec.Code)
	}
}

var _ = context.Background

func TestEnvironmentJSONUsesLowercaseKeys(t *testing.T) {
	srv, sealer := testServer(t, newRepo(), adminRules())

	rec := request(t, srv, sealer, admin(), http.MethodGet, "/api/me", "")
	body := rec.Body.String()

	for _, want := range []string{`"name"`, `"display"`, `"protected"`, `"order"`} {
		if !strings.Contains(body, want) {
			t.Errorf("environment json missing %s, the frontend types depend on it:\n%s", want, body)
		}
	}
	for _, unwanted := range []string{`"Name"`, `"Display"`, `"Protected"`} {
		if strings.Contains(body, unwanted) {
			t.Errorf("environment json leaks the Go field name %s:\n%s", unwanted, body)
		}
	}
}

func TestUnknownFileSHAIsRejectedRatherThanTrusted(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())

	if rec := request(t, srv, sealer, admin(), http.MethodGet, "/api/environments/production/flags", ""); rec.Code != http.StatusOK {
		t.Fatalf("list failed: %d", rec.Code)
	}

	body := `{"enabled":false,"fileSha":"a-sha-never-served"}`
	rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/environments/production/flags/new-checkout/state", body)

	if rec.Code != http.StatusConflict {
		t.Fatalf("an unrecognised fileSha must fail closed, got %d: %s", rec.Code, rec.Body)
	}
	if len(repo.puts) != 0 {
		t.Error("nothing should have been committed")
	}
}

func TestSaveRuleCommitsTheNewQuery(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())

	rec := request(t, srv, sealer, admin(), http.MethodGet, "/api/environments/production/flags", "")
	var list ListResult
	decode(t, rec, &list)

	var sha string
	for _, f := range list.Flags {
		if f.Key == "new-checkout" {
			sha = f.FileSHA
		}
	}

	body := `{"ruleName":"gold-cohort","query":"(plan eq \"pro\") or (plan eq \"enterprise\")","fileSha":"` + sha + `"}`
	rec = request(t, srv, sealer, admin(), http.MethodPost, "/api/environments/production/flags/new-checkout/rule", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	stored := repo.files["production/payments.goff.yaml"]
	if !strings.Contains(stored, `(plan eq "pro") or (plan eq "enterprise")`) {
		t.Errorf("new query not committed:\n%s", stored)
	}
	if strings.Contains(stored, "tier eq") {
		t.Error("old query survived")
	}
	if !strings.Contains(stored, "percentage:") {
		t.Error("the rule outcome should be untouched when no outcome is sent")
	}

	msg := repo.puts[0]["message"].(string)
	if !strings.Contains(msg, "updated targeting for gold-cohort") {
		t.Errorf("commit message = %q", msg)
	}
}

func TestSaveRuleRequiresEditRulesPermission(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())

	rec := request(t, srv, sealer, marketer(), http.MethodGet, "/api/environments/production/flags", "")
	var list ListResult
	decode(t, rec, &list)

	var sha string
	for _, f := range list.Flags {
		if f.Key == "banner-test" {
			sha = f.FileSHA
		}
	}

	body := `{"ruleName":"anything","query":"(a eq \"b\")","fileSha":"` + sha + `"}`
	rec = request(t, srv, sealer, marketer(), http.MethodPost, "/api/environments/production/flags/banner-test/rule", body)
	if rec.Code != http.StatusForbidden {
		t.Errorf("marketing has toggle+rollout only, so edit_rules must be refused; got %d: %s", rec.Code, rec.Body)
	}
	if len(repo.puts) != 0 {
		t.Error("nothing should have been committed")
	}
}

func TestSaveRuleRejectsEmptyQuery(t *testing.T) {
	srv, sealer := testServer(t, newRepo(), adminRules())
	body := `{"ruleName":"gold-cohort","query":"   "}`
	rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/environments/production/flags/new-checkout/rule", body)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestSavedBuilderQueryStaysEditable(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())

	rec := request(t, srv, sealer, admin(), http.MethodGet, "/api/environments/production/flags", "")
	var list ListResult
	decode(t, rec, &list)
	var sha string
	for _, f := range list.Flags {
		if f.Key == "new-checkout" {
			sha = f.FileSHA
		}
	}

	nested := `((tier eq \"gold\") or (tier eq \"platinum\")) and (region eq \"us\")`
	body := `{"ruleName":"gold-cohort","query":"` + nested + `","fileSha":"` + sha + `"}`
	if rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/environments/production/flags/new-checkout/rule", body); rec.Code != http.StatusOK {
		t.Fatalf("save failed: %s", rec.Body)
	}

	rec = request(t, srv, sealer, admin(), http.MethodGet, "/api/environments/production/flags/new-checkout", "")
	var after FlagView
	decode(t, rec, &after)

	rule := after.Rules[0]
	if rule.Advanced {
		t.Error("a nested builder query must come back editable, not flagged advanced")
	}
	if rule.Condition == nil || rule.Condition.Op != "and" || len(rule.Condition.Children) != 2 {
		t.Errorf("condition tree did not rehydrate: %+v", rule.Condition)
	}
}

func TestAttributesEndpointSuggestsWhatIsInUse(t *testing.T) {
	srv, sealer := testServer(t, newRepo(), adminRules())

	rec := request(t, srv, sealer, admin(), http.MethodGet, "/api/environments/production/attributes", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	var attrs []string
	decode(t, rec, &attrs)

	found := map[string]bool{}
	for _, a := range attrs {
		found[a] = true
	}
	if !found["tier"] || !found["account_id"] {
		t.Errorf("attributes = %v, want tier and account_id harvested from existing rules", attrs)
	}
}

func TestMeAdvertisesCapabilities(t *testing.T) {
	srv, sealer := testServer(t, newRepo(), adminRules())

	rec := request(t, srv, sealer, admin(), http.MethodGet, "/api/me", "")
	var out struct {
		Capabilities storage.Capabilities `json:"capabilities"`
	}
	decode(t, rec, &out)

	if !out.Capabilities.History {
		t.Error("the git backend supports history, so the UI should show the panel")
	}
	if !out.Capabilities.Attribution || !out.Capabilities.Review {
		t.Errorf("capabilities = %+v", out.Capabilities)
	}
}

func TestListReportsTeamsAsFilesWithoutExtraFetches(t *testing.T) {
	repo := newRepo()
	repo.files["production/payments.goff.yaml"] = strings.Replace(paymentsFile,
		"  version: \"3.1.0\"",
		"  version: \"3.1.0\"\n  metadata:\n    team: billing", 1)

	srv, sealer := testServer(t, repo, adminRules())

	rec := request(t, srv, sealer, admin(), http.MethodGet, "/api/environments/production/flags", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	var out ListResult
	decode(t, rec, &out)

	want := []TeamOption{
		{Name: "growth", File: "production/growth.goff.yaml"},
		{Name: "payments", File: "production/payments.goff.yaml"},
	}
	if len(out.Teams) != len(want) {
		t.Fatalf("teams = %v, want one per file, sorted", out.Teams)
	}
	for i, w := range want {
		if out.Teams[i] != w {
			t.Errorf("teams[%d] = %+v, want %+v", i, out.Teams[i], w)
		}
	}

	// The destination list comes from filenames; the label still comes from metadata alone.
	for _, f := range out.Flags {
		if f.Key == "new-checkout" && f.Team != "billing" {
			t.Errorf("a declared team must be reported, got %q", f.Team)
		}
		if f.Key == "banner-test" && f.Team != "" {
			t.Errorf("growth.goff.yaml must not imply a growth team, got %q", f.Team)
		}
	}
}

func TestTeamsOmitFilesYouCannotCreateIn(t *testing.T) {
	rules := []permissions.Rule{{
		Group:        "flags-admins",
		Allow:        []string{"growth"},
		Environments: []string{"production"},
	}, {
		Group:        "flags-admins",
		Allow:        []string{"payments"},
		Environments: []string{"production"},
		Actions:      []string{"view"},
	}}

	srv, sealer := testServer(t, newRepo(), rules)
	rec := request(t, srv, sealer, admin(), http.MethodGet, "/api/environments/production/flags", "")

	var out ListResult
	decode(t, rec, &out)

	if len(out.Teams) != 1 || out.Teams[0].Name != "growth" {
		t.Errorf("teams = %+v, want only the file the user may create in", out.Teams)
	}
	if len(out.Flags) != 2 {
		t.Errorf("view-only files must still be listed, got %d flags", len(out.Flags))
	}
}

func TestTeamsIsAnEmptyArrayNotNull(t *testing.T) {
	srv, sealer := testServer(t, newRepo(), adminRules())
	rec := request(t, srv, sealer, admin(), http.MethodGet, "/api/environments/production/flags", "")
	if strings.Contains(rec.Body.String(), `"teams":null`) {
		t.Error("teams must serialise as [] so the UI can map over it")
	}
}
