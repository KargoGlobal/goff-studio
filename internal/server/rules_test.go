package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/go-feature-flag/studio/internal/auth"
)

func shaFor(t *testing.T, srv *Server, sealer *auth.Sealer, key string) string {
	t.Helper()
	rec := request(t, srv, sealer, admin(), http.MethodGet, "/api/environments/production/flags", "")
	var list ListResult
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	for _, f := range list.Flags {
		if f.Key == key {
			return f.FileSHA
		}
	}
	t.Fatalf("flag %s not found", key)
	return ""
}

func rulesOf(t *testing.T, srv *Server, sealer *auth.Sealer, key string) []Rule {
	t.Helper()
	rec := request(t, srv, sealer, admin(), http.MethodGet, "/api/environments/production/flags/"+key, "")
	var view FlagView
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	out := make([]Rule, 0, len(view.Rules))
	for _, r := range view.Rules {
		out = append(out, Rule{Name: r.Name, Query: r.Query, Disabled: r.Disabled})
	}
	return out
}

type Rule struct {
	Name     string
	Query    string
	Disabled bool
}

func TestAddRuleAppendsByDefault(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())

	sha := shaFor(t, srv, sealer, "new-checkout")
	body := `{"name":"beta-users","query":"(beta eq true)","outcome":{"variation":"on"},"fileSha":"` + sha + `"}`
	rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/environments/production/flags/new-checkout/rules", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	rules := rulesOf(t, srv, sealer, "new-checkout")
	if len(rules) != 2 || rules[1].Name != "beta-users" {
		t.Fatalf("rules = %+v, want beta-users appended last", rules)
	}

	stored := repo.files["production/payments.goff.yaml"]
	if !strings.Contains(stored, "beta-users") || !strings.Contains(stored, "(beta eq true)") {
		t.Errorf("rule not committed:\n%s", stored)
	}
	if !strings.Contains(stored, "# Payment flags") {
		t.Error("comment lost")
	}
	if !strings.Contains(repo.puts[0]["message"].(string), "added rule beta-users") {
		t.Errorf("commit message = %q", repo.puts[0]["message"])
	}
}

func TestAddRuleAtPosition(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())

	sha := shaFor(t, srv, sealer, "new-checkout")
	body := `{"name":"first","query":"(a eq \"b\")","outcome":{"variation":"on"},"position":0,"fileSha":"` + sha + `"}`
	if rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/environments/production/flags/new-checkout/rules", body); rec.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	rules := rulesOf(t, srv, sealer, "new-checkout")
	if rules[0].Name != "first" {
		t.Errorf("position 0 should land first, got %+v", rules)
	}
	_ = repo
}

func TestAddRuleToAFlagWithNoRules(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())

	sha := shaFor(t, srv, sealer, "banner-test")
	body := `{"name":"only-rule","query":"(plan eq \"pro\")","outcome":{"variation":"on"},"fileSha":"` + sha + `"}`
	rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/environments/production/flags/banner-test/rules", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("adding the first rule to a rule-less flag failed: %d %s", rec.Code, rec.Body)
	}

	if !strings.Contains(repo.files["production/growth.goff.yaml"], "only-rule") {
		t.Errorf("rule not committed:\n%s", repo.files["production/growth.goff.yaml"])
	}
}

func TestAddRuleRejectsDuplicateName(t *testing.T) {
	srv, sealer := testServer(t, newRepo(), adminRules())
	sha := shaFor(t, srv, sealer, "new-checkout")

	body := `{"name":"gold-cohort","query":"(a eq \"b\")","outcome":{"variation":"on"},"fileSha":"` + sha + `"}`
	rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/environments/production/flags/new-checkout/rules", body)
	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409: %s", rec.Code, rec.Body)
	}
}

func TestAddRuleRejectsUnknownVariation(t *testing.T) {
	srv, sealer := testServer(t, newRepo(), adminRules())
	sha := shaFor(t, srv, sealer, "new-checkout")

	body := `{"name":"ghosty","query":"(a eq \"b\")","outcome":{"variation":"nope"},"fileSha":"` + sha + `"}`
	rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/environments/production/flags/new-checkout/rules", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "nope") {
		t.Errorf("error should name the bad variation: %s", rec.Body)
	}
}

func TestAddRuleRequiresPermission(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())

	body := `{"name":"x","query":"(a eq \"b\")","outcome":{"variation":"on"}}`
	rec := request(t, srv, sealer, marketer(), http.MethodPost, "/api/environments/production/flags/banner-test/rules", body)
	if rec.Code != http.StatusForbidden {
		t.Errorf("marketing has no edit_rules, got %d: %s", rec.Code, rec.Body)
	}
	if len(repo.puts) != 0 {
		t.Error("nothing should have been committed")
	}
}

func TestDeleteRule(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())
	sha := shaFor(t, srv, sealer, "new-checkout")

	rec := request(t, srv, sealer, admin(), http.MethodDelete,
		"/api/environments/production/flags/new-checkout/rules/gold-cohort?fileSha="+sha, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	stored := repo.files["production/payments.goff.yaml"]
	if strings.Contains(stored, "gold-cohort") {
		t.Errorf("rule not removed:\n%s", stored)
	}
	if !strings.Contains(stored, "new-checkout:") || !strings.Contains(stored, `version: "3.1.0"`) {
		t.Errorf("deleting a rule damaged the flag:\n%s", stored)
	}
	if !strings.Contains(repo.puts[0]["message"].(string), "deleted rule gold-cohort") {
		t.Errorf("commit message = %q", repo.puts[0]["message"])
	}
}

func TestDeleteMissingRule(t *testing.T) {
	srv, sealer := testServer(t, newRepo(), adminRules())
	rec := request(t, srv, sealer, admin(), http.MethodDelete,
		"/api/environments/production/flags/new-checkout/rules/ghost", "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestReorderRules(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())

	sha := shaFor(t, srv, sealer, "new-checkout")
	add := `{"name":"second","query":"(b eq \"c\")","outcome":{"variation":"off"},"fileSha":"` + sha + `"}`
	if rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/environments/production/flags/new-checkout/rules", add); rec.Code != http.StatusCreated {
		t.Fatalf("setup add failed: %s", rec.Body)
	}

	sha = shaFor(t, srv, sealer, "new-checkout")
	body := `{"order":["second","gold-cohort"],"fileSha":"` + sha + `"}`
	rec := request(t, srv, sealer, admin(), http.MethodPut, "/api/environments/production/flags/new-checkout/rules/order", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	rules := rulesOf(t, srv, sealer, "new-checkout")
	if rules[0].Name != "second" || rules[1].Name != "gold-cohort" {
		t.Errorf("order not applied: %+v", rules)
	}

	stored := repo.files["production/payments.goff.yaml"]
	if !strings.Contains(stored, `(tier eq "gold") or (account_id eq "42")`) {
		t.Errorf("reordering must preserve each rule's full content:\n%s", stored)
	}
	if !strings.Contains(stored, "percentage:") {
		t.Errorf("the reordered rule lost its outcome:\n%s", stored)
	}
}

func TestReorderRejectsNonPermutations(t *testing.T) {
	srv, sealer := testServer(t, newRepo(), adminRules())
	sha := shaFor(t, srv, sealer, "new-checkout")

	cases := map[string]string{
		"missing a rule":  `{"order":[],"fileSha":"` + sha + `"}`,
		"unknown rule":    `{"order":["ghost"],"fileSha":"` + sha + `"}`,
		"duplicated rule": `{"order":["gold-cohort","gold-cohort"],"fileSha":"` + sha + `"}`,
	}

	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			rec := request(t, srv, sealer, admin(), http.MethodPut, "/api/environments/production/flags/new-checkout/rules/order", body)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400: %s", rec.Code, rec.Body)
			}
		})
	}
}

func TestEditRuleOutcomeSwitchesToVariation(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())
	sha := shaFor(t, srv, sealer, "new-checkout")

	body := `{"ruleName":"gold-cohort","outcome":{"variation":"on"},"fileSha":"` + sha + `"}`
	rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/environments/production/flags/new-checkout/rule", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	stored := repo.files["production/payments.goff.yaml"]
	if strings.Contains(stored, "percentage:") {
		t.Errorf("switching to a single variation should drop the percentage split:\n%s", stored)
	}
	if !strings.Contains(stored, `variation: "on"`) {
		t.Errorf("new outcome not written:\n%s", stored)
	}
	if !strings.Contains(stored, `(tier eq "gold") or (account_id eq "42")`) {
		t.Errorf("omitting query must leave the condition unchanged:\n%s", stored)
	}
}

func TestEditRuleCanDisableIt(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())
	sha := shaFor(t, srv, sealer, "new-checkout")

	body := `{"ruleName":"gold-cohort","disabled":true,"fileSha":"` + sha + `"}`
	if rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/environments/production/flags/new-checkout/rule", body); rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	if !strings.Contains(repo.files["production/payments.goff.yaml"], "disable: true") {
		t.Errorf("rule not disabled:\n%s", repo.files["production/payments.goff.yaml"])
	}

	rules := rulesOf(t, srv, sealer, "new-checkout")
	if !rules[0].Disabled {
		t.Error("disabled state should read back")
	}
}

func TestEditRuleRejectsEmptyChange(t *testing.T) {
	srv, sealer := testServer(t, newRepo(), adminRules())
	rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/environments/production/flags/new-checkout/rule", `{"ruleName":"gold-cohort"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestRuleWritesLeaveSiblingFileAlone(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())
	before := repo.files["production/growth.goff.yaml"]

	sha := shaFor(t, srv, sealer, "new-checkout")
	body := `{"name":"x","query":"(a eq \"b\")","outcome":{"variation":"on"},"fileSha":"` + sha + `"}`
	if rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/environments/production/flags/new-checkout/rules", body); rec.Code != http.StatusCreated {
		t.Fatal(rec.Body)
	}

	if repo.files["production/growth.goff.yaml"] != before {
		t.Error("the untouched file changed")
	}
}

func TestCommittedFileStaysLoadableAfterRuleWrites(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())

	sha := shaFor(t, srv, sealer, "new-checkout")
	add := `{"name":"extra","query":"(z eq \"y\")","outcome":{"percentage":{"on":30,"off":70}},"fileSha":"` + sha + `"}`
	if rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/environments/production/flags/new-checkout/rules", add); rec.Code != http.StatusCreated {
		t.Fatal(rec.Body)
	}

	if err := srv.svc.adapter.Validate([]byte(repo.files["production/payments.goff.yaml"])); err != nil {
		t.Errorf("committed a file GO Feature Flag cannot load: %v", err)
	}
}
