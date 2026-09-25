package server

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/go-feature-flag/studio/internal/auth"
	"github.com/go-feature-flag/studio/internal/goff"
	"github.com/go-feature-flag/studio/internal/permissions"
)

func fileSHA(t *testing.T, srv *Server, sealer *auth.Sealer, sess *auth.Session, env, key string) string {
	t.Helper()

	rec := request(t, srv, sealer, sess, http.MethodGet, "/api/environments/"+env+"/flags", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list failed: %d: %s", rec.Code, rec.Body)
	}

	var list ListResult
	decode(t, rec, &list)
	for _, f := range list.Flags {
		if f.Key == key {
			return f.FileSHA
		}
	}
	return ""
}

func warmCache(t *testing.T, srv *Server, sealer *auth.Sealer, sess *auth.Session, env string) {
	t.Helper()
	if rec := request(t, srv, sealer, sess, http.MethodGet, "/api/environments/"+env+"/flags", ""); rec.Code != http.StatusOK {
		t.Fatalf("list failed: %d: %s", rec.Code, rec.Body)
	}
}

func mustLoad(t *testing.T, repo *repoState, path string) []goff.Flag {
	t.Helper()

	repo.mu.Lock()
	content := repo.files[path]
	repo.mu.Unlock()

	a := goff.New()
	if err := a.Validate([]byte(content)); err != nil {
		t.Fatalf("committed file is not loadable by GOFF: %v\n%s", err, content)
	}
	flags, broken, err := a.Parse(path, []byte(content))
	if err != nil {
		t.Fatalf("parsing committed file: %v", err)
	}
	if len(broken) > 0 {
		t.Fatalf("committed file has broken flags: %+v", broken)
	}
	return flags
}

func keysOf(flags []goff.Flag) []string {
	out := make([]string, 0, len(flags))
	for _, f := range flags {
		out = append(out, f.Key)
	}
	return out
}

func TestCreateFlagCommitsAndValidates(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())
	warmCache(t, srv, sealer, admin(), "production")

	body := `{"key":"dark-mode","team":"growth","type":"boolean",
		"variations":[{"name":"on","value":"true"},{"name":"off","value":"false"}],
		"default":"off","enabled":true,"fileSha":"sha-growth"}`
	rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/environments/production/flags", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d, want 201: %s", rec.Code, rec.Body)
	}

	flags := mustLoad(t, repo, "production/growth.goff.yaml")
	found := false
	for _, f := range flags {
		if f.Key != "dark-mode" {
			continue
		}
		found = true
		if f.Type != goff.TypeBool {
			t.Errorf("type = %q", f.Type)
		}
		if !f.Enabled {
			t.Error("flag should be enabled")
		}
		if f.Default.Variation != "off" {
			t.Errorf("default = %q", f.Default.Variation)
		}
		if len(f.Variations) != 2 {
			t.Errorf("variations = %+v", f.Variations)
		}
	}
	if !found {
		t.Fatalf("new flag missing, got %v", keysOf(flags))
	}

	if got := keysOf(flags); len(got) != 2 {
		t.Errorf("sibling flag lost, keys = %v", got)
	}

	msg := repo.puts[0]["message"].(string)
	if !strings.Contains(msg, "[production] growth/dark-mode: created") {
		t.Errorf("commit message = %q", msg)
	}
}

func TestCreateCoercesEachType(t *testing.T) {
	cases := []struct {
		name  string
		kind  string
		vars  string
		def   string
		check func(t *testing.T, f goff.Flag)
	}{
		{
			name: "string", kind: "string", def: "control",
			vars: `[{"name":"control","value":"blue"},{"name":"variant","value":"green"}]`,
			check: func(t *testing.T, f goff.Flag) {
				if f.Type != goff.TypeString || f.Variations[0].Value != "blue" {
					t.Errorf("type=%q variations=%+v", f.Type, f.Variations)
				}
			},
		},
		{
			name: "number", kind: "number", def: "low",
			vars: `[{"name":"low","value":"1"},{"name":"high","value":"99.5"}]`,
			check: func(t *testing.T, f goff.Flag) {
				if f.Type != goff.TypeNumber {
					t.Errorf("type = %q", f.Type)
				}
				byName := map[string]string{}
				for _, v := range f.Variations {
					byName[v.Name] = fmt.Sprintf("%v", v.Value)
				}
				if byName["low"] != "1" {
					t.Errorf("low = %s", byName["low"])
				}
				if byName["high"] != "99.5" {
					t.Errorf("high = %s", byName["high"])
				}
			},
		},
		{
			name: "json", kind: "json", def: "empty",
			vars: `[{"name":"empty","value":"{}"},{"name":"full","value":"{\"a\":[1,2]}"}]`,
			check: func(t *testing.T, f goff.Flag) {
				if f.Type != goff.TypeJSON {
					t.Errorf("type = %q", f.Type)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := newRepo()
			srv, sealer := testServer(t, repo, adminRules())
			warmCache(t, srv, sealer, admin(), "production")

			body := `{"key":"typed","team":"growth","type":"` + tc.kind +
				`","variations":` + tc.vars + `,"default":"` + tc.def + `","enabled":true,"fileSha":"sha-growth"}`
			rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/environments/production/flags", body)
			if rec.Code != http.StatusCreated {
				t.Fatalf("status %d: %s", rec.Code, rec.Body)
			}

			for _, f := range mustLoad(t, repo, "production/growth.goff.yaml") {
				if f.Key == "typed" {
					tc.check(t, f)
				}
			}
		})
	}
}

func TestCreateRejectsMistypedVariationValue(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())

	body := `{"key":"nope","team":"growth","type":"number",
		"variations":[{"name":"a","value":"not-a-number"}],"default":"a","enabled":true}`
	rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/environments/production/flags", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400: %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "not a number") {
		t.Errorf("error should explain the coercion failure, got %s", rec.Body)
	}
	if len(repo.puts) != 0 {
		t.Error("nothing should have been committed")
	}
}

func TestCreateRequiresCreatePermission(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())

	body := `{"key":"sneaky","team":"growth","type":"boolean",
		"variations":[{"name":"on","value":"true"}],"default":"on","enabled":true}`
	rec := request(t, srv, sealer, marketer(), http.MethodPost, "/api/environments/production/flags", body)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("marketing has toggle+rollout only, want 403, got %d: %s", rec.Code, rec.Body)
	}
	if len(repo.puts) != 0 {
		t.Error("nothing should have been committed")
	}
}

func TestCreateDeniedOnAFileTheUserCannotWrite(t *testing.T) {
	repo := newRepo()
	rules := []permissions.Rule{
		{Group: "growth-only", Allow: []string{"growth"}, Actions: []string{"create"}},
	}
	srv, sealer := testServer(t, repo, rules)
	sess := &auth.Session{Subject: "okta|g", Email: "g@acme.com", Groups: []string{"growth-only"}}

	body := `{"key":"wherever","team":"payments","type":"boolean",
		"variations":[{"name":"on","value":"true"}],"default":"on","enabled":true}`
	rec := request(t, srv, sealer, sess, http.MethodPost, "/api/environments/production/flags", body)
	if rec.Code != http.StatusForbidden {
		t.Errorf("create must be scoped per file, got %d: %s", rec.Code, rec.Body)
	}
	if len(repo.puts) != 0 {
		t.Error("nothing should have been committed")
	}
}

func TestCreateRejectsDuplicateKeyInTheSameFile(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())

	body := `{"key":"banner-test","team":"growth","type":"boolean",
		"variations":[{"name":"on","value":"true"}],"default":"on","enabled":true}`
	rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/environments/production/flags", body)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status %d, want 409: %s", rec.Code, rec.Body)
	}
	if len(repo.puts) != 0 {
		t.Error("nothing should have been committed")
	}
}

func TestCreateRejectsDuplicateKeyInAnotherFile(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())

	body := `{"key":"new-checkout","team":"growth","type":"boolean",
		"variations":[{"name":"on","value":"true"}],"default":"on","enabled":true}`
	rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/environments/production/flags", body)
	if rec.Code != http.StatusConflict {
		t.Fatalf("a key already used in payments must be refused for growth, got %d: %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "unique within an environment") {
		t.Errorf("error should explain the uniqueness rule, got %s", rec.Body)
	}
	if len(repo.puts) != 0 {
		t.Error("nothing should have been committed")
	}
}

func TestCreateRejectsDuplicateHiddenBehindViewPermissions(t *testing.T) {
	repo := newRepo()
	rules := []permissions.Rule{
		{Group: "growth-only", Allow: []string{"growth"}},
	}
	srv, sealer := testServer(t, repo, rules)
	sess := &auth.Session{Subject: "okta|g", Email: "g@acme.com", Groups: []string{"growth-only"}}

	body := `{"key":"new-checkout","team":"growth","type":"boolean",
		"variations":[{"name":"on","value":"true"}],"default":"on","enabled":true}`
	rec := request(t, srv, sealer, sess, http.MethodPost, "/api/environments/production/flags", body)
	if rec.Code != http.StatusConflict {
		t.Fatalf("a key the user cannot see must still block a create, got %d: %s", rec.Code, rec.Body)
	}
	if len(repo.puts) != 0 {
		t.Error("nothing should have been committed")
	}
}

func TestCreateRejectsInvalidKeys(t *testing.T) {
	cases := map[string]string{
		"empty":           "",
		"only whitespace": "   ",
		"inner space":     "dark mode",
		"tab":             "dark\tmode",
		"newline":         "dark\\nmode",
	}

	for name, key := range cases {
		t.Run(name, func(t *testing.T) {
			repo := newRepo()
			srv, sealer := testServer(t, repo, adminRules())

			body := `{"key":"` + key + `","team":"growth","type":"boolean",
				"variations":[{"name":"on","value":"true"}],"default":"on","enabled":true}`
			rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/environments/production/flags", body)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("key %q should be refused, got %d: %s", key, rec.Code, rec.Body)
			}
			if len(repo.puts) != 0 {
				t.Error("nothing should have been committed")
			}
		})
	}
}

func TestCreateAllowsKeysGOFFItselfAccepts(t *testing.T) {
	for _, key := range []string{"a.b.c", "UPPER_case", "with-dash", "123numeric"} {
		t.Run(key, func(t *testing.T) {
			repo := newRepo()
			srv, sealer := testServer(t, repo, adminRules())
			warmCache(t, srv, sealer, admin(), "production")

			body := `{"key":"` + key + `","team":"growth","type":"boolean",
				"variations":[{"name":"on","value":"true"},{"name":"off","value":"false"}],
				"default":"on","enabled":true,"fileSha":"sha-growth"}`
			rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/environments/production/flags", body)
			if rec.Code != http.StatusCreated {
				t.Fatalf("GOFF accepts %q so studio should too, got %d: %s", key, rec.Code, rec.Body)
			}

			found := false
			for _, f := range mustLoad(t, repo, "production/growth.goff.yaml") {
				if f.Key == key {
					found = true
				}
			}
			if !found {
				t.Errorf("%q did not round-trip", key)
			}
		})
	}
}

func TestCreateRequiresAtLeastOneVariation(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())

	body := `{"key":"empty","team":"growth","type":"boolean",
		"variations":[],"default":"on","enabled":true}`
	rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/environments/production/flags", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400: %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "at least one variation") {
		t.Errorf("error = %s", rec.Body)
	}
	if len(repo.puts) != 0 {
		t.Error("nothing should have been committed")
	}
}

func TestCreateRejectsMissingOrUnknownDefaultVariation(t *testing.T) {
	for name, def := range map[string]string{"missing": "", "unknown": "maybe"} {
		t.Run(name, func(t *testing.T) {
			repo := newRepo()
			srv, sealer := testServer(t, repo, adminRules())

			body := `{"key":"defaultless","team":"growth","type":"boolean",
				"variations":[{"name":"on","value":"true"},{"name":"off","value":"false"}],
				"default":"` + def + `","enabled":true}`
			rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/environments/production/flags", body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("default %q should be refused, got %d: %s", def, rec.Code, rec.Body)
			}
			if len(repo.puts) != 0 {
				t.Error("nothing should have been committed")
			}
		})
	}
}

func TestCreateRejectsUnknownFile(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())

	body := `{"key":"stray","team":"nope","type":"boolean",
		"variations":[{"name":"on","value":"true"}],"default":"on","enabled":true}`
	rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/environments/production/flags", body)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status %d, want 400: %s", rec.Code, rec.Body)
	}
	if len(repo.puts) != 0 {
		t.Error("nothing should have been committed")
	}
}

func TestCreateRejectsUnknownType(t *testing.T) {
	srv, sealer := testServer(t, newRepo(), adminRules())

	body := `{"key":"weird","team":"growth","type":"datetime",
		"variations":[{"name":"on","value":"true"}],"default":"on","enabled":true}`
	rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/environments/production/flags", body)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status %d, want 400: %s", rec.Code, rec.Body)
	}
}

func TestCreatePreservesCommentsAndSiblings(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())
	warmCache(t, srv, sealer, admin(), "production")

	body := `{"key":"second-payment","team":"payments","type":"boolean",
		"variations":[{"name":"on","value":"true"},{"name":"off","value":"false"}],
		"default":"off","enabled":true,"fileSha":"sha-payments"}`
	if rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/environments/production/flags", body); rec.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	stored := repo.files["production/payments.goff.yaml"]
	if !strings.Contains(stored, "# Payment flags") {
		t.Errorf("comment lost:\n%s", stored)
	}
	if !strings.Contains(stored, `version: "3.1.0"`) {
		t.Errorf("sibling's preserved field lost:\n%s", stored)
	}
	if !strings.Contains(stored, "gold-cohort") {
		t.Errorf("sibling's targeting lost:\n%s", stored)
	}

	if got := keysOf(mustLoad(t, repo, "production/payments.goff.yaml")); len(got) != 2 {
		t.Errorf("keys = %v", got)
	}
}

func TestCreateWithAnUnknownFileSHAIsRejected(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())
	warmCache(t, srv, sealer, admin(), "production")

	body := `{"key":"dark-mode","team":"growth","type":"boolean",
		"variations":[{"name":"on","value":"true"}],"default":"on","enabled":true,
		"fileSha":"a-sha-studio-never-served"}`
	rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/environments/production/flags", body)
	if rec.Code != http.StatusConflict {
		t.Fatalf("an unrecognised fileSha must fail closed, got %d: %s", rec.Code, rec.Body)
	}
	if len(repo.puts) != 0 {
		t.Error("nothing should have been committed")
	}
}

func TestCreateRebasesOverAnUnrelatedEdit(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())
	warmCache(t, srv, sealer, admin(), "production")

	repo.mu.Lock()
	repo.files["production/growth.goff.yaml"] = growthFile + `someone-elses:
  variations:
    on: true
    off: false
  defaultRule:
    variation: "on"
`
	repo.shas["production/growth.goff.yaml"] = "sha-moved-on"
	repo.mu.Unlock()

	body := `{"key":"dark-mode","team":"growth","type":"boolean",
		"variations":[{"name":"on","value":"true"},{"name":"off","value":"false"}],
		"default":"off","enabled":true,"fileSha":"sha-growth"}`
	rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/environments/production/flags", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("a create should rebase over an unrelated flag, got %d: %s", rec.Code, rec.Body)
	}

	got := keysOf(mustLoad(t, repo, "production/growth.goff.yaml"))
	if len(got) != 3 {
		t.Errorf("both flags should survive, keys = %v", got)
	}
}

func TestCreateLosesToAConcurrentCreateOfTheSameKey(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())
	warmCache(t, srv, sealer, admin(), "production")

	repo.mu.Lock()
	repo.files["production/growth.goff.yaml"] = growthFile + `dark-mode:
  variations:
    yes: true
    no: false
  defaultRule:
    variation: "yes"
`
	repo.shas["production/growth.goff.yaml"] = "sha-someone-else"
	repo.mu.Unlock()

	body := `{"key":"dark-mode","team":"growth","type":"boolean",
		"variations":[{"name":"on","value":"true"},{"name":"off","value":"false"}],
		"default":"off","enabled":true,"fileSha":"sha-growth"}`
	rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/environments/production/flags", body)
	if rec.Code != http.StatusConflict {
		t.Fatalf("a racing create of the same key must not clobber, got %d: %s", rec.Code, rec.Body)
	}
	if len(repo.puts) != 0 {
		t.Error("nothing should have been committed")
	}

	stored := repo.files["production/growth.goff.yaml"]
	if !strings.Contains(stored, "yes: true") {
		t.Errorf("the other writer's flag was overwritten:\n%s", stored)
	}
}

func TestDeleteFlagRemovesOnlyThatFlag(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())
	sha := fileSHA(t, srv, sealer, admin(), "production", "new-checkout")

	rec := request(t, srv, sealer, admin(), http.MethodDelete,
		"/api/environments/production/flags/new-checkout?fileSha="+sha, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	stored := repo.files["production/payments.goff.yaml"]
	if strings.Contains(stored, "new-checkout") {
		t.Errorf("flag not removed:\n%s", stored)
	}
	if !strings.Contains(stored, "# Payment flags") {
		t.Errorf("comment lost:\n%s", stored)
	}
	if repo.files["production/growth.goff.yaml"] != growthFile {
		t.Error("the other file was modified")
	}

	msg := repo.puts[0]["message"].(string)
	if !strings.Contains(msg, "[production] payments/new-checkout: deleted") {
		t.Errorf("commit message = %q", msg)
	}
}

func TestDeleteLeavesSiblingFlagsAndCommentsIntact(t *testing.T) {
	repo := newRepo()
	repo.files["production/payments.goff.yaml"] = paymentsFile + `
# keep me
other-payment:
  variations:
    on: true
    off: false
  defaultRule:
    variation: "on"
`
	srv, sealer := testServer(t, repo, adminRules())
	sha := fileSHA(t, srv, sealer, admin(), "production", "new-checkout")

	rec := request(t, srv, sealer, admin(), http.MethodDelete,
		"/api/environments/production/flags/new-checkout?fileSha="+sha, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	stored := repo.files["production/payments.goff.yaml"]
	for _, want := range []string{"# keep me", "other-payment:", "# Payment flags"} {
		if !strings.Contains(stored, want) {
			t.Errorf("%q lost:\n%s", want, stored)
		}
	}

	if got := keysOf(mustLoad(t, repo, "production/payments.goff.yaml")); len(got) != 1 || got[0] != "other-payment" {
		t.Errorf("keys = %v", got)
	}
}

func TestDeleteRequiresDeletePermission(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())
	sha := fileSHA(t, srv, sealer, marketer(), "production", "banner-test")

	rec := request(t, srv, sealer, marketer(), http.MethodDelete,
		"/api/environments/production/flags/banner-test?fileSha="+sha, "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("marketing has toggle+rollout only, want 403, got %d: %s", rec.Code, rec.Body)
	}
	if len(repo.puts) != 0 {
		t.Error("nothing should have been committed")
	}
	if repo.files["production/growth.goff.yaml"] != growthFile {
		t.Error("the file was modified")
	}
}

func TestDeleteNonexistentFlagIs404(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())

	rec := request(t, srv, sealer, admin(), http.MethodDelete,
		"/api/environments/production/flags/never-existed", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404: %s", rec.Code, rec.Body)
	}
	if len(repo.puts) != 0 {
		t.Error("nothing should have been committed")
	}
}

func TestDeleteWithAStaleSHAOnTheSameFlagConflicts(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())
	warmCache(t, srv, sealer, admin(), "production")

	repo.mu.Lock()
	repo.files["production/payments.goff.yaml"] = strings.Replace(paymentsFile, `variation: "off"`, `variation: "on"`, 1)
	repo.shas["production/payments.goff.yaml"] = "sha-someone-else"
	repo.mu.Unlock()

	rec := request(t, srv, sealer, admin(), http.MethodDelete,
		"/api/environments/production/flags/new-checkout?fileSha=sha-payments", "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("deleting a flag someone just edited must conflict, got %d: %s", rec.Code, rec.Body)
	}
	if len(repo.puts) != 0 {
		t.Error("nothing should have been committed")
	}
}

func TestDeleteWithAnUnknownFileSHAIsRejected(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())
	warmCache(t, srv, sealer, admin(), "production")

	rec := request(t, srv, sealer, admin(), http.MethodDelete,
		"/api/environments/production/flags/new-checkout?fileSha=never-served", "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("an unrecognised fileSha must fail closed, got %d: %s", rec.Code, rec.Body)
	}
	if len(repo.puts) != 0 {
		t.Error("nothing should have been committed")
	}
}

func TestVariationsEditCommits(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())
	sha := fileSHA(t, srv, sealer, admin(), "production", "banner-test")

	body := `{"type":"boolean","variations":[{"name":"on","value":"true"},{"name":"off","value":"false"},
		{"name":"holdout","value":"false"}],"default":"on","fileSha":"` + sha + `"}`
	rec := request(t, srv, sealer, admin(), http.MethodPut,
		"/api/environments/production/flags/banner-test/variations", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	for _, f := range mustLoad(t, repo, "production/growth.goff.yaml") {
		if f.Key != "banner-test" {
			continue
		}
		if len(f.Variations) != 3 {
			t.Errorf("variations = %+v", f.Variations)
		}
	}

	msg := repo.puts[0]["message"].(string)
	if !strings.Contains(msg, "[production] growth/banner-test: updated variations") {
		t.Errorf("commit message = %q", msg)
	}
}

func TestVariationsRequiresEditVariationsPermission(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())
	sha := fileSHA(t, srv, sealer, marketer(), "production", "banner-test")

	body := `{"type":"boolean","variations":[{"name":"on","value":"true"},{"name":"off","value":"false"}],
		"default":"on","fileSha":"` + sha + `"}`
	rec := request(t, srv, sealer, marketer(), http.MethodPut,
		"/api/environments/production/flags/banner-test/variations", body)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("marketing has toggle+rollout only, want 403, got %d: %s", rec.Code, rec.Body)
	}
	if len(repo.puts) != 0 {
		t.Error("nothing should have been committed")
	}
}

func TestVariationsRenameThatBreaksARuleIsRejected(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())
	sha := fileSHA(t, srv, sealer, admin(), "production", "new-checkout")

	body := `{"type":"boolean","variations":[{"name":"yes","value":"true"},{"name":"no","value":"false"}],
		"default":"no","fileSha":"` + sha + `"}`
	rec := request(t, srv, sealer, admin(), http.MethodPut,
		"/api/environments/production/flags/new-checkout/variations", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("renaming a referenced variation must be refused, got %d: %s", rec.Code, rec.Body)
	}

	var payload struct {
		Error string `json:"error"`
	}
	decode(t, rec, &payload)

	if !strings.Contains(payload.Error, `variation "off"`) {
		t.Errorf("error must name the offending variation, got %q", payload.Error)
	}
	if !strings.Contains(payload.Error, "gold-cohort") {
		t.Errorf("error must name what references it, got %q", payload.Error)
	}
	if len(repo.puts) != 0 {
		t.Error("nothing should have been committed")
	}
	if repo.files["production/payments.goff.yaml"] != paymentsFile {
		t.Error("the file must be untouched")
	}
}

func TestVariationsRemovalReferencedByDefaultRuleIsRejected(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())
	sha := fileSHA(t, srv, sealer, admin(), "production", "banner-test")

	body := `{"type":"boolean","variations":[{"name":"off","value":"false"}],"fileSha":"` + sha + `"}`
	rec := request(t, srv, sealer, admin(), http.MethodPut,
		"/api/environments/production/flags/banner-test/variations", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("removing the default variation must be refused, got %d: %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "default rule") {
		t.Errorf("error should name the default rule, got %s", rec.Body)
	}
	if len(repo.puts) != 0 {
		t.Error("nothing should have been committed")
	}
}

func TestVariationsRenameIsAllowedWhenTheDefaultMovesToo(t *testing.T) {
	repo := newRepo()
	repo.files["production/growth.goff.yaml"] = `simple:
  variations:
    on: true
    off: false
  defaultRule:
    variation: "on"
`
	srv, sealer := testServer(t, repo, adminRules())
	sha := fileSHA(t, srv, sealer, admin(), "production", "simple")

	body := `{"type":"boolean","variations":[{"name":"enabled","value":"true"},{"name":"disabled","value":"false"}],
		"default":"enabled","fileSha":"` + sha + `"}`
	rec := request(t, srv, sealer, admin(), http.MethodPut,
		"/api/environments/production/flags/simple/variations", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("a rename with the default moved along must succeed, got %d: %s", rec.Code, rec.Body)
	}

	for _, f := range mustLoad(t, repo, "production/growth.goff.yaml") {
		if f.Key != "simple" {
			continue
		}
		if f.Default.Variation != "enabled" {
			t.Errorf("default = %q", f.Default.Variation)
		}
		if len(f.Variations) != 2 || f.Variations[0].Name == "on" {
			t.Errorf("variations = %+v", f.Variations)
		}
	}
}

func TestVariationsRejectsDuplicateAndNamelessVariations(t *testing.T) {
	cases := map[string]string{
		"duplicate": `[{"name":"on","value":"true"},{"name":"on","value":"false"}]`,
		"nameless":  `[{"name":"","value":"true"},{"name":"off","value":"false"}]`,
		"empty":     `[]`,
	}

	for name, vars := range cases {
		t.Run(name, func(t *testing.T) {
			repo := newRepo()
			srv, sealer := testServer(t, repo, adminRules())
			sha := fileSHA(t, srv, sealer, admin(), "production", "banner-test")

			body := `{"type":"boolean","variations":` + vars + `,"default":"on","fileSha":"` + sha + `"}`
			rec := request(t, srv, sealer, admin(), http.MethodPut,
				"/api/environments/production/flags/banner-test/variations", body)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status %d, want 400: %s", rec.Code, rec.Body)
			}
			if len(repo.puts) != 0 {
				t.Error("nothing should have been committed")
			}
		})
	}
}

func TestVariationsOnAMissingFlagIs404(t *testing.T) {
	srv, sealer := testServer(t, newRepo(), adminRules())

	body := `{"type":"boolean","variations":[{"name":"on","value":"true"}],"default":"on"}`
	rec := request(t, srv, sealer, admin(), http.MethodPut,
		"/api/environments/production/flags/ghost/variations", body)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status %d, want 404: %s", rec.Code, rec.Body)
	}
}

func TestVariationsPreservesCommentsAndRules(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())
	sha := fileSHA(t, srv, sealer, admin(), "production", "new-checkout")

	body := `{"type":"boolean","variations":[{"name":"on","value":"true"},{"name":"off","value":"false"},
		{"name":"maybe","value":"true"}],"default":"off","fileSha":"` + sha + `"}`
	rec := request(t, srv, sealer, admin(), http.MethodPut,
		"/api/environments/production/flags/new-checkout/variations", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	stored := repo.files["production/payments.goff.yaml"]
	for _, want := range []string{"# Payment flags", "gold-cohort", `version: "3.1.0"`, "maybe"} {
		if !strings.Contains(stored, want) {
			t.Errorf("%q missing:\n%s", want, stored)
		}
	}
	mustLoad(t, repo, "production/payments.goff.yaml")
}

func TestVariationsWithAnUnknownFileSHAIsRejected(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())
	warmCache(t, srv, sealer, admin(), "production")

	body := `{"type":"boolean","variations":[{"name":"on","value":"true"},{"name":"off","value":"false"}],
		"default":"on","fileSha":"never-served"}`
	rec := request(t, srv, sealer, admin(), http.MethodPut,
		"/api/environments/production/flags/banner-test/variations", body)
	if rec.Code != http.StatusConflict {
		t.Fatalf("an unrecognised fileSha must fail closed, got %d: %s", rec.Code, rec.Body)
	}
	if len(repo.puts) != 0 {
		t.Error("nothing should have been committed")
	}
}

func TestDiffCreateShowsTheNewFlagWithoutCommitting(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())

	body := `{"change":"create","team":"growth","type":"boolean",
		"variations":[{"name":"on","value":"true"},{"name":"off","value":"false"}],
		"default":"off","enabled":true}`
	rec := request(t, srv, sealer, admin(), http.MethodPost,
		"/api/environments/production/flags/dark-mode/diff", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	var out DiffResult
	decode(t, rec, &out)
	if !strings.Contains(out.Description, "Create dark-mode") {
		t.Errorf("description = %q", out.Description)
	}
	if !strings.Contains(out.Diff, "dark-mode") {
		t.Errorf("diff should show the new flag:\n%s", out.Diff)
	}
	if len(repo.puts) != 0 {
		t.Error("previewing must not write")
	}
	if repo.files["production/growth.goff.yaml"] != growthFile {
		t.Error("the repo contents changed")
	}
}

func TestDiffCreateRejectsADuplicateKey(t *testing.T) {
	srv, sealer := testServer(t, newRepo(), adminRules())

	body := `{"change":"create","team":"growth","type":"boolean",
		"variations":[{"name":"on","value":"true"}],"default":"on","enabled":true}`
	rec := request(t, srv, sealer, admin(), http.MethodPost,
		"/api/environments/production/flags/new-checkout/diff", body)
	if rec.Code != http.StatusConflict {
		t.Errorf("status %d, want 409: %s", rec.Code, rec.Body)
	}
}

func TestDiffCreateRequiresCreatePermission(t *testing.T) {
	srv, sealer := testServer(t, newRepo(), adminRules())

	body := `{"change":"create","team":"growth","type":"boolean",
		"variations":[{"name":"on","value":"true"}],"default":"on","enabled":true}`
	rec := request(t, srv, sealer, marketer(), http.MethodPost,
		"/api/environments/production/flags/dark-mode/diff", body)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status %d, want 403: %s", rec.Code, rec.Body)
	}
}

func TestDiffDeleteShowsTheRemovalWithoutCommitting(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())

	rec := request(t, srv, sealer, admin(), http.MethodPost,
		"/api/environments/production/flags/new-checkout/diff", `{"change":"delete"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	var out DiffResult
	decode(t, rec, &out)
	if !strings.Contains(out.Description, "Delete new-checkout") {
		t.Errorf("description = %q", out.Description)
	}
	if !strings.Contains(out.Diff, "-new-checkout:") {
		t.Errorf("diff should show the removal:\n%s", out.Diff)
	}
	if len(repo.puts) != 0 {
		t.Error("previewing must not write")
	}
}

func TestDiffDeleteRequiresDeletePermission(t *testing.T) {
	srv, sealer := testServer(t, newRepo(), adminRules())

	rec := request(t, srv, sealer, marketer(), http.MethodPost,
		"/api/environments/production/flags/banner-test/diff", `{"change":"delete"}`)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status %d, want 403: %s", rec.Code, rec.Body)
	}
}

func TestDiffVariationsShowsTheChangeWithoutCommitting(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())

	body := `{"change":"variations","type":"boolean","variations":[{"name":"on","value":"true"},
		{"name":"off","value":"false"},{"name":"holdout","value":"false"}],"default":"on"}`
	rec := request(t, srv, sealer, admin(), http.MethodPost,
		"/api/environments/production/flags/banner-test/diff", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	var out DiffResult
	decode(t, rec, &out)
	if !strings.Contains(out.Description, "holdout") {
		t.Errorf("description = %q", out.Description)
	}
	if !strings.Contains(out.Diff, "holdout") {
		t.Errorf("diff should show the new variation:\n%s", out.Diff)
	}
	if len(repo.puts) != 0 {
		t.Error("previewing must not write")
	}
}

func TestDiffVariationsRejectsABrokenReference(t *testing.T) {
	srv, sealer := testServer(t, newRepo(), adminRules())

	body := `{"change":"variations","type":"boolean","variations":[{"name":"yes","value":"true"},
		{"name":"no","value":"false"}],"default":"no"}`
	rec := request(t, srv, sealer, admin(), http.MethodPost,
		"/api/environments/production/flags/new-checkout/diff", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400: %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "gold-cohort") {
		t.Errorf("error should name the rule, got %s", rec.Body)
	}
}

func TestDiffVariationsRequiresEditVariationsPermission(t *testing.T) {
	srv, sealer := testServer(t, newRepo(), adminRules())

	body := `{"change":"variations","type":"boolean","variations":[{"name":"on","value":"true"},
		{"name":"off","value":"false"}],"default":"on"}`
	rec := request(t, srv, sealer, marketer(), http.MethodPost,
		"/api/environments/production/flags/banner-test/diff", body)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status %d, want 403: %s", rec.Code, rec.Body)
	}
}

func TestCreateThenDeleteRoundTripsToTheOriginalFile(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())
	warmCache(t, srv, sealer, admin(), "production")

	body := `{"key":"temp","team":"growth","type":"boolean",
		"variations":[{"name":"on","value":"true"},{"name":"off","value":"false"}],
		"default":"off","enabled":true,"fileSha":"sha-growth"}`
	if rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/environments/production/flags", body); rec.Code != http.StatusCreated {
		t.Fatalf("create failed: %s", rec.Body)
	}

	sha := fileSHA(t, srv, sealer, admin(), "production", "temp")
	rec := request(t, srv, sealer, admin(), http.MethodDelete,
		"/api/environments/production/flags/temp?fileSha="+sha, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("delete failed: %s", rec.Body)
	}

	if got := keysOf(mustLoad(t, repo, "production/growth.goff.yaml")); len(got) != 1 || got[0] != "banner-test" {
		t.Errorf("keys = %v", got)
	}
}

func TestEveryWriteEndpointLeavesTheFileLoadable(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())
	warmCache(t, srv, sealer, admin(), "production")

	create := `{"key":"audit","team":"growth","type":"string",
		"variations":[{"name":"a","value":"alpha"},{"name":"b","value":"beta"}],
		"default":"a","enabled":true,"fileSha":"sha-growth"}`
	if rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/environments/production/flags", create); rec.Code != http.StatusCreated {
		t.Fatalf("create: %s", rec.Body)
	}
	mustLoad(t, repo, "production/growth.goff.yaml")

	sha := fileSHA(t, srv, sealer, admin(), "production", "audit")
	vary := `{"type":"string","variations":[{"name":"a","value":"alpha"},{"name":"b","value":"beta"},
		{"name":"c","value":"gamma"}],"default":"c","fileSha":"` + sha + `"}`
	if rec := request(t, srv, sealer, admin(), http.MethodPut,
		"/api/environments/production/flags/audit/variations", vary); rec.Code != http.StatusOK {
		t.Fatalf("variations: %s", rec.Body)
	}
	mustLoad(t, repo, "production/growth.goff.yaml")

	sha = fileSHA(t, srv, sealer, admin(), "production", "audit")
	if rec := request(t, srv, sealer, admin(), http.MethodDelete,
		"/api/environments/production/flags/audit?fileSha="+sha, ""); rec.Code != http.StatusOK {
		t.Fatalf("delete: %s", rec.Body)
	}
	mustLoad(t, repo, "production/growth.goff.yaml")
}

func TestWriteEndpointsRejectAnonymousCallers(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())

	cases := []struct {
		method, target, body string
	}{
		{http.MethodPost, "/api/environments/production/flags", `{"key":"x","team":"growth","type":"boolean","variations":[{"name":"on","value":"true"}],"default":"on"}`},
		{http.MethodDelete, "/api/environments/production/flags/banner-test", ""},
		{http.MethodPut, "/api/environments/production/flags/banner-test/variations", `{"type":"boolean","variations":[{"name":"on","value":"true"}],"default":"on"}`},
	}

	for _, tc := range cases {
		rec := request(t, srv, sealer, nil, tc.method, tc.target, tc.body)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s returned %d, want 401", tc.method, tc.target, rec.Code)
		}
	}
	if len(repo.puts) != 0 {
		t.Error("nothing should have been committed")
	}
}

func TestCreateIsRefusedInAnEnvironmentTheUserCannotSee(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())

	body := `{"key":"dev-only","team":"growth","type":"boolean",
		"variations":[{"name":"on","value":"true"}],"default":"on","enabled":true}`
	rec := request(t, srv, sealer, marketer(), http.MethodPost, "/api/environments/dev/flags", body)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status %d, want 403: %s", rec.Code, rec.Body)
	}
	if len(repo.puts) != 0 {
		t.Error("nothing should have been committed")
	}
}

func TestDeleteResponseCarriesThePollHint(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())
	sha := fileSHA(t, srv, sealer, admin(), "production", "banner-test")

	rec := request(t, srv, sealer, admin(), http.MethodDelete,
		"/api/environments/production/flags/banner-test?fileSha="+sha, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	var result SaveResult
	decode(t, rec, &result)
	if !strings.Contains(result.Message, "30 seconds") {
		t.Errorf("message = %q", result.Message)
	}
	if result.Commit != "new-commit" {
		t.Errorf("commit = %q", result.Commit)
	}
}

func TestCreateBodyMustBeValidJSON(t *testing.T) {
	srv, sealer := testServer(t, newRepo(), adminRules())

	for _, target := range []string{
		"/api/environments/production/flags",
	} {
		rec := request(t, srv, sealer, admin(), http.MethodPost, target, `{not json`)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s returned %d, want 400", target, rec.Code)
		}
	}

	rec := request(t, srv, sealer, admin(), http.MethodPut,
		"/api/environments/production/flags/banner-test/variations", `{not json`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("variations returned %d, want 400", rec.Code)
	}
}

func TestEmptyEnvironmentListsAsArraysNotNull(t *testing.T) {
	repo := newRepo()
	repo.files["staging/payments.goff.yaml"] = "# nothing yet\n"
	repo.shas["staging/payments.goff.yaml"] = "sha-staging"

	srv, sealer := testServer(t, repo, adminRules())

	rec := request(t, srv, sealer, admin(), http.MethodGet, "/api/environments/staging/flags", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	body := rec.Body.String()
	if strings.Contains(body, `"flags":null`) {
		t.Errorf("an empty environment must serialise flags as [], or the UI crashes on .map():\n%s", body)
	}
	if strings.Contains(body, `"broken":null`) {
		t.Errorf("broken must never be null:\n%s", body)
	}
}

func shaOf(t *testing.T, srv *Server, sealer *auth.Sealer, key string) string {
	t.Helper()
	rec := request(t, srv, sealer, admin(), http.MethodGet, "/api/environments/production/flags", "")
	var list ListResult
	decode(t, rec, &list)
	for _, f := range list.Flags {
		if f.Key == key {
			return f.FileSHA
		}
	}
	return ""
}

func TestCreateWritesTheTeamToMetadataAndDerivesTheFile(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())
	sha := shaOf(t, srv, sealer, "banner-test")

	body := `{"key":"growth-flag","team":"growth","type":"boolean",` +
		`"enabled":true,"variations":[{"name":"on","value":"true"},{"name":"off","value":"false"}],` +
		`"default":"off","fileSha":"` + sha + `"}`

	rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/environments/production/flags", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	stored := repo.files["production/growth.goff.yaml"]
	if !strings.Contains(stored, "team: growth") {
		t.Errorf("the team must be written to metadata, never left implied by the path:\n%s", stored)
	}

	rec = request(t, srv, sealer, admin(), http.MethodGet, "/api/environments/production/flags/growth-flag", "")
	var view FlagView
	decode(t, rec, &view)
	if view.Team != "growth" {
		t.Errorf("team = %q, want growth", view.Team)
	}
	if view.File != "production/growth.goff.yaml" {
		t.Errorf("file = %q, want it derived from the team", view.File)
	}
}

func TestCreateRejectsAMissingTeam(t *testing.T) {
	srv, sealer := testServer(t, newRepo(), adminRules())
	sha := shaOf(t, srv, sealer, "banner-test")

	body := `{"key":"plain-flag","type":"boolean","enabled":true,` +
		`"variations":[{"name":"on","value":"true"}],"default":"on","fileSha":"` + sha + `"}`

	rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/environments/production/flags", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "team is required") {
		t.Errorf("the error should say a team is required, got %s", rec.Body)
	}
}

func TestCreateRejectsATeamThatHasNoFileYet(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())
	sha := shaOf(t, srv, sealer, "banner-test")

	body := `{"key":"billing-flag","team":"billing","type":"boolean","enabled":true,` +
		`"variations":[{"name":"on","value":"true"}],"default":"on","fileSha":"` + sha + `"}`

	rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/environments/production/flags", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if _, ok := repo.files["production/billing.goff.yaml"]; ok {
		t.Error("a create must never invent a file; that is what CreateTeam is for")
	}
}

func TestCreateTeamAddsAFileYouCanThenCreateFlagsIn(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())

	rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/environments/production/teams", `{"name":"billing"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	seeded, ok := repo.files["production/billing.goff.yaml"]
	if !ok {
		t.Fatal("a new team must create its file")
	}
	if !strings.HasPrefix(seeded, "#") {
		t.Errorf("the seed should be a comment so the file is valid but empty:\n%s", seeded)
	}

	rec = request(t, srv, sealer, admin(), http.MethodGet, "/api/environments/production/flags", "")
	var list ListResult
	decode(t, rec, &list)
	found := false
	for _, team := range list.Teams {
		if team.Name == "billing" && team.File == "production/billing.goff.yaml" {
			found = true
		}
	}
	if !found {
		t.Errorf("the new team must appear as a destination, got %+v", list.Teams)
	}

	body := `{"key":"billing-flag","team":"billing","type":"boolean","enabled":true,` +
		`"variations":[{"name":"on","value":"true"}],"default":"on"}`
	if rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/environments/production/flags", body); rec.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if !strings.Contains(repo.files["production/billing.goff.yaml"], "team: billing") {
		t.Error("the flag should land in the new team file with its label")
	}
}

func TestCreateTeamRejectsDuplicatesAndBadNames(t *testing.T) {
	srv, sealer := testServer(t, newRepo(), adminRules())

	for _, name := range []string{"growth", "", "  ", "with space", "sub/dir", ".hidden"} {
		body := `{"name":"` + name + `"}`
		rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/environments/production/teams", body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("name %q: status %d, want 400: %s", name, rec.Code, rec.Body)
		}
	}
}

func TestCreateTeamNeedsCreatePermissionOnTheDerivedFile(t *testing.T) {
	rules := []permissions.Rule{{
		Group:        "flags-admins",
		Allow:        []string{"growth"},
		Environments: []string{"production"},
	}}

	repo := newRepo()
	srv, sealer := testServer(t, repo, rules)

	rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/environments/production/teams", `{"name":"billing"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403: %s", rec.Code, rec.Body)
	}
	if _, ok := repo.files["production/billing.goff.yaml"]; ok {
		t.Error("a forbidden create must not write anything")
	}
}

func TestRenameKeepsPositionCommentsAndUnmodelledFields(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())
	sha := shaOf(t, srv, sealer, "new-checkout")

	body := `{"key":"checkout-v2","fileSha":"` + sha + `"}`
	rec := request(t, srv, sealer, admin(), http.MethodPost,
		"/api/environments/production/flags/new-checkout/key", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	stored := repo.files["production/payments.goff.yaml"]
	if !strings.Contains(stored, "checkout-v2:") {
		t.Errorf("new key missing:\n%s", stored)
	}
	if strings.Contains(stored, "new-checkout:") {
		t.Errorf("old key still present:\n%s", stored)
	}
	if !strings.Contains(stored, "# Payment flags") {
		t.Error("the file's leading comment must survive a rename")
	}
	if !strings.Contains(stored, `version: "3.1.0"`) {
		t.Error("a field Studio does not model must survive a rename")
	}
	if !strings.Contains(stored, "gold-cohort") {
		t.Error("targeting rules must survive a rename")
	}

	rec = request(t, srv, sealer, admin(), http.MethodGet,
		"/api/environments/production/flags/checkout-v2", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("the renamed flag should be readable, got %d: %s", rec.Code, rec.Body)
	}
}

func TestRenameRefusesAKeyUsedInAnotherFile(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())
	before := repo.files["production/growth.goff.yaml"]
	sha := shaOf(t, srv, sealer, "banner-test")

	body := `{"key":"new-checkout","fileSha":"` + sha + `"}`
	rec := request(t, srv, sealer, admin(), http.MethodPost,
		"/api/environments/production/flags/banner-test/key", body)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status %d, want 409: %s", rec.Code, rec.Body)
	}
	if repo.files["production/growth.goff.yaml"] != before {
		t.Error("a refused rename must not touch the file")
	}
}

func TestRenameRejectsBadKeys(t *testing.T) {
	srv, sealer := testServer(t, newRepo(), adminRules())

	for _, key := range []string{"", "   ", "with space", "banner-test"} {
		body := `{"key":"` + key + `"}`
		rec := request(t, srv, sealer, admin(), http.MethodPost,
			"/api/environments/production/flags/banner-test/key", body)
		if rec.Code != http.StatusBadRequest && rec.Code != http.StatusConflict {
			t.Errorf("key %q: status %d, want 400 or 409: %s", key, rec.Code, rec.Body)
		}
	}
}

func TestRenameNeedsBothCreateAndDelete(t *testing.T) {
	rules := []permissions.Rule{{
		Group:        "flags-admins",
		Allow:        []string{"growth"},
		Environments: []string{"production"},
		Actions:      []string{"view", "create"},
	}}

	repo := newRepo()
	srv, sealer := testServer(t, repo, rules)
	before := repo.files["production/growth.goff.yaml"]

	rec := request(t, srv, sealer, admin(), http.MethodPost,
		"/api/environments/production/flags/banner-test/key", `{"key":"banner-v2"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("create without delete must not rename, got %d: %s", rec.Code, rec.Body)
	}
	if repo.files["production/growth.goff.yaml"] != before {
		t.Error("a forbidden rename must not write")
	}
}

func TestRenameRejectsAStaleFileSHA(t *testing.T) {
	srv, sealer := testServer(t, newRepo(), adminRules())

	body := `{"key":"banner-v2","fileSha":"not-the-current-sha"}`
	rec := request(t, srv, sealer, admin(), http.MethodPost,
		"/api/environments/production/flags/banner-test/key", body)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status %d, want 409: %s", rec.Code, rec.Body)
	}
}

func rampRepo() *repoState {
	repo := newRepo()
	repo.files["production/growth.goff.yaml"] = rampFile
	return repo
}

func TestProgressiveRolloutIsReadable(t *testing.T) {
	srv, sealer := testServer(t, rampRepo(), adminRules())

	rec := request(t, srv, sealer, admin(), http.MethodGet, "/api/environments/production/flags/ramped", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	var view FlagView
	decode(t, rec, &view)
	if len(view.Rules) != 1 || view.Rules[0].Progressive == nil {
		t.Fatalf("a progressive rollout must reach the UI: %+v", view.Rules)
	}

	got := view.Rules[0].Progressive
	if got.Initial.Variation != "off" || got.Initial.Percentage != 0 {
		t.Errorf("initial = %+v", got.Initial)
	}
	if got.End.Variation != "on" || got.End.Percentage != 100 {
		t.Errorf("end = %+v", got.End)
	}
	if got.Initial.Date != "2026-10-01T00:00:00Z" {
		t.Errorf("initial date = %q, want RFC3339 UTC", got.Initial.Date)
	}
}

func TestProgressiveRolloutCanBeEdited(t *testing.T) {
	repo := rampRepo()
	srv, sealer := testServer(t, repo, adminRules())

	body := `{"ruleName":"ramp","initial":{"variation":"off","percentage":10,"date":"2027-01-01T00:00:00Z"},` +
		`"end":{"variation":"on","percentage":90,"date":"2027-03-01T00:00:00Z"}}`

	rec := request(t, srv, sealer, admin(), http.MethodPost,
		"/api/environments/production/flags/ramped/progressive", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	stored := repo.files["production/growth.goff.yaml"]
	for _, want := range []string{"percentage: 10", "percentage: 90", "2027-01-01T00:00:00Z", "2027-03-01T00:00:00Z"} {
		if !strings.Contains(stored, want) {
			t.Errorf("missing %q in:\n%s", want, stored)
		}
	}
	if strings.Contains(stored, "2026-10-01") {
		t.Error("the old dates should be gone")
	}
	if !strings.Contains(stored, `query: (region eq "us")`) {
		t.Error("the rule query must survive a rollout edit")
	}
}

func TestProgressiveRolloutCanBeRemoved(t *testing.T) {
	repo := rampRepo()
	srv, sealer := testServer(t, repo, adminRules())

	rec := request(t, srv, sealer, admin(), http.MethodPost,
		"/api/environments/production/flags/ramped/progressive",
		`{"ruleName":"ramp","clear":true,"variation":"on"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	stored := repo.files["production/growth.goff.yaml"]
	if strings.Contains(stored, "progressiveRollout") {
		t.Errorf("the rollout should be gone:\n%s", stored)
	}
	if !strings.Contains(stored, `query: (region eq "us")`) {
		t.Error("clearing a rollout must not remove the rule")
	}
}

func TestProgressiveRolloutRejectsBadInput(t *testing.T) {
	srv, sealer := testServer(t, rampRepo(), adminRules())

	cases := map[string]string{
		"end before initial":  `{"ruleName":"ramp","initial":{"variation":"off","percentage":0,"date":"2027-03-01T00:00:00Z"},"end":{"variation":"on","percentage":100,"date":"2027-01-01T00:00:00Z"}}`,
		"unknown variation":   `{"ruleName":"ramp","initial":{"variation":"nope","percentage":0,"date":"2027-01-01T00:00:00Z"},"end":{"variation":"on","percentage":100,"date":"2027-03-01T00:00:00Z"}}`,
		"bad date":            `{"ruleName":"ramp","initial":{"variation":"off","percentage":0,"date":"tomorrow"},"end":{"variation":"on","percentage":100,"date":"2027-03-01T00:00:00Z"}}`,
		"percentage over 100": `{"ruleName":"ramp","initial":{"variation":"off","percentage":0,"date":"2027-01-01T00:00:00Z"},"end":{"variation":"on","percentage":500,"date":"2027-03-01T00:00:00Z"}}`,
		"missing end":         `{"ruleName":"ramp","initial":{"variation":"off","percentage":0,"date":"2027-01-01T00:00:00Z"}}`,
		"unknown rule":        `{"ruleName":"nope","initial":{"variation":"off","percentage":0,"date":"2027-01-01T00:00:00Z"},"end":{"variation":"on","percentage":100,"date":"2027-03-01T00:00:00Z"}}`,
	}

	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			rec := request(t, srv, sealer, admin(), http.MethodPost,
				"/api/environments/production/flags/ramped/progressive", body)
			if rec.Code != http.StatusBadRequest && rec.Code != http.StatusNotFound {
				t.Errorf("status %d, want 400 or 404: %s", rec.Code, rec.Body)
			}
		})
	}
}

func TestProgressiveRolloutNeedsRolloutPermission(t *testing.T) {
	rules := []permissions.Rule{{
		Group:        "flags-admins",
		Allow:        []string{"growth"},
		Environments: []string{"production"},
		Actions:      []string{"view", "toggle"},
	}}

	repo := rampRepo()
	srv, sealer := testServer(t, repo, rules)
	before := repo.files["production/growth.goff.yaml"]

	body := `{"ruleName":"ramp","initial":{"variation":"off","percentage":10,"date":"2027-01-01T00:00:00Z"},` +
		`"end":{"variation":"on","percentage":90,"date":"2027-03-01T00:00:00Z"}}`

	rec := request(t, srv, sealer, admin(), http.MethodPost,
		"/api/environments/production/flags/ramped/progressive", body)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403: %s", rec.Code, rec.Body)
	}
	if repo.files["production/growth.goff.yaml"] != before {
		t.Error("a forbidden rollout edit must not write")
	}
}

func timedRepo() *repoState {
	repo := newRepo()
	repo.files["production/growth.goff.yaml"] = timedFile
	return repo
}

func TestExperimentationIsReadable(t *testing.T) {
	srv, sealer := testServer(t, timedRepo(), adminRules())

	rec := request(t, srv, sealer, admin(), http.MethodGet, "/api/environments/production/flags/timed", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	var view FlagView
	decode(t, rec, &view)
	if view.Experimentation == nil {
		t.Fatal("an experimentation window must reach the UI")
	}
	if view.Experimentation.Start != "2026-10-01T00:00:00Z" {
		t.Errorf("start = %q, want RFC3339 UTC", view.Experimentation.Start)
	}
	if view.Experimentation.End != "2026-11-01T00:00:00Z" {
		t.Errorf("end = %q, want RFC3339 UTC", view.Experimentation.End)
	}
	if strings.Contains(strings.Join(view.Preserved, ","), "experimentation") {
		t.Errorf("experimentation is editable, it must not be preserved: %v", view.Preserved)
	}
}

func TestExperimentationCanBeSet(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())

	body := `{"start":"2027-01-01T00:00:00Z","end":"2027-03-01T00:00:00Z"}`
	rec := request(t, srv, sealer, admin(), http.MethodPost,
		"/api/environments/production/flags/banner-test/experimentation", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	stored := repo.files["production/growth.goff.yaml"]
	for _, want := range []string{"experimentation:", "2027-01-01T00:00:00Z", "2027-03-01T00:00:00Z"} {
		if !strings.Contains(stored, want) {
			t.Errorf("missing %q in:\n%s", want, stored)
		}
	}
}

func TestExperimentationCanBeEdited(t *testing.T) {
	repo := timedRepo()
	srv, sealer := testServer(t, repo, adminRules())

	body := `{"start":"2027-01-01T00:00:00Z","end":"2027-03-01T00:00:00Z"}`
	rec := request(t, srv, sealer, admin(), http.MethodPost,
		"/api/environments/production/flags/timed/experimentation", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	stored := repo.files["production/growth.goff.yaml"]
	if strings.Contains(stored, "2026-10-01") {
		t.Errorf("the old window should be gone:\n%s", stored)
	}
	if !strings.Contains(stored, "2027-03-01T00:00:00Z") {
		t.Errorf("the new window did not land:\n%s", stored)
	}
}

func TestExperimentationAcceptsAnOpenEndedWindow(t *testing.T) {
	repo := timedRepo()
	srv, sealer := testServer(t, repo, adminRules())

	rec := request(t, srv, sealer, admin(), http.MethodPost,
		"/api/environments/production/flags/timed/experimentation",
		`{"end":"2027-03-01T00:00:00Z"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	stored := repo.files["production/growth.goff.yaml"]
	if strings.Contains(stored, "start:") {
		t.Errorf("an omitted start must not be written:\n%s", stored)
	}
	if !strings.Contains(stored, "end: 2027-03-01T00:00:00Z") {
		t.Errorf("end did not land:\n%s", stored)
	}
}

func TestExperimentationCanBeRemoved(t *testing.T) {
	repo := timedRepo()
	srv, sealer := testServer(t, repo, adminRules())

	rec := request(t, srv, sealer, admin(), http.MethodPost,
		"/api/environments/production/flags/timed/experimentation", `{"clear":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	stored := repo.files["production/growth.goff.yaml"]
	if strings.Contains(stored, "experimentation") {
		t.Errorf("the window should be gone:\n%s", stored)
	}
	if !strings.Contains(stored, "variation: \"off\"") {
		t.Errorf("clearing a window must not damage the flag:\n%s", stored)
	}
}

func TestExperimentationRejectsBadInput(t *testing.T) {
	srv, sealer := testServer(t, timedRepo(), adminRules())

	cases := map[string]string{
		"both empty":       `{}`,
		"end before start": `{"start":"2027-03-01T00:00:00Z","end":"2027-01-01T00:00:00Z"}`,
		"equal bounds":     `{"start":"2027-01-01T00:00:00Z","end":"2027-01-01T00:00:00Z"}`,
		"bad start":        `{"start":"tomorrow","end":"2027-03-01T00:00:00Z"}`,
		"bad end":          `{"start":"2027-01-01T00:00:00Z","end":"whenever"}`,
	}

	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			rec := request(t, srv, sealer, admin(), http.MethodPost,
				"/api/environments/production/flags/timed/experimentation", body)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status %d, want 400: %s", rec.Code, rec.Body)
			}
		})
	}
}

func TestExperimentationNeedsRolloutPermission(t *testing.T) {
	rules := []permissions.Rule{{
		Group:        "flags-admins",
		Allow:        []string{"growth"},
		Environments: []string{"production"},
		Actions:      []string{"view", "toggle"},
	}}

	repo := timedRepo()
	srv, sealer := testServer(t, repo, rules)
	before := repo.files["production/growth.goff.yaml"]

	rec := request(t, srv, sealer, admin(), http.MethodPost,
		"/api/environments/production/flags/timed/experimentation",
		`{"start":"2027-01-01T00:00:00Z","end":"2027-03-01T00:00:00Z"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403: %s", rec.Code, rec.Body)
	}
	if repo.files["production/growth.goff.yaml"] != before {
		t.Error("a forbidden window edit must not write")
	}
}

func TestExperimentationDiffDoesNotWrite(t *testing.T) {
	repo := timedRepo()
	srv, sealer := testServer(t, repo, adminRules())
	before := repo.files["production/growth.goff.yaml"]

	body := `{"change":"experimentation","experimentation":{"start":"2027-01-01T00:00:00Z","end":"2027-03-01T00:00:00Z"}}`
	rec := request(t, srv, sealer, admin(), http.MethodPost,
		"/api/environments/production/flags/timed/diff", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	var got DiffResult
	decode(t, rec, &got)
	if !strings.Contains(got.Diff, "2027-01-01T00:00:00Z") {
		t.Errorf("the diff should show the new window: %q", got.Diff)
	}
	if repo.files["production/growth.goff.yaml"] != before {
		t.Error("a diff must not write")
	}
}

func TestExperimentationClearDiffShowsRemoval(t *testing.T) {
	srv, sealer := testServer(t, timedRepo(), adminRules())

	rec := request(t, srv, sealer, admin(), http.MethodPost,
		"/api/environments/production/flags/timed/diff", `{"change":"experimentationClear"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	var got DiffResult
	decode(t, rec, &got)
	if !strings.Contains(got.Diff, "-  experimentation:") {
		t.Errorf("the diff should remove the window: %q", got.Diff)
	}
}
