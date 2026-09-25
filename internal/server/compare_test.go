package server

import (
	"net/http"
	"strings"
	"testing"

	"github.com/go-feature-flag/studio/internal/permissions"
)

const devPaymentsFile = `new-checkout:
  variations:
    on: true
    off: false
  targeting:
    - name: gold-cohort
      query: (tier eq "gold")
      percentage:
        on: 50
        off: 50
  defaultRule:
    variation: "on"
`

func twoEnvRepo() *repoState {
	repo := newRepo()
	repo.files["dev/payments.goff.yaml"] = devPaymentsFile
	repo.shas["dev/payments.goff.yaml"] = "sha-dev-payments"
	return repo
}

func compareURL(key, from, to string) string {
	return "/api/flags/" + key + "/compare?from=" + from + "&to=" + to
}

func TestCompareReportsDriftPerField(t *testing.T) {
	srv, sealer := testServer(t, twoEnvRepo(), adminRules())

	rec := request(t, srv, sealer, admin(), http.MethodGet,
		compareURL("new-checkout", "dev", "production"), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	var got CompareResult
	decode(t, rec, &got)

	if !got.From.Present || !got.To.Present {
		t.Fatalf("both sides should be present: %+v", got)
	}
	if got.From.Environment != "dev" || got.To.Environment != "production" {
		t.Errorf("sides are the wrong way round: %+v", got)
	}

	fields := strings.Join(got.Differs, ",")
	for _, want := range []string{FieldDefault, FieldRules} {
		if !strings.Contains(fields, want) {
			t.Errorf("expected %s in differs, got %v", want, got.Differs)
		}
	}
	if strings.Contains(fields, FieldVariations) {
		t.Errorf("variations are identical, they must not read as drift: %v", got.Differs)
	}
	if got.Diff == "" {
		t.Error("a side-by-side diff should be returned")
	}
}

func TestCompareFindsNoDriftForAnIdenticalFlag(t *testing.T) {
	repo := twoEnvRepo()
	repo.files["dev/payments.goff.yaml"] = paymentsFile
	srv, sealer := testServer(t, repo, adminRules())

	rec := request(t, srv, sealer, admin(), http.MethodGet,
		compareURL("new-checkout", "dev", "production"), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	var got CompareResult
	decode(t, rec, &got)
	if len(got.Differs) != 0 {
		t.Errorf("the same flag in both environments must show no drift, got %v", got.Differs)
	}
	if got.Diff != "" {
		t.Errorf("an identical flag needs no diff, got %q", got.Diff)
	}
}

func TestCompareTreatsAMissingFlagAsAState(t *testing.T) {
	srv, sealer := testServer(t, twoEnvRepo(), adminRules())

	rec := request(t, srv, sealer, admin(), http.MethodGet,
		compareURL("banner-test", "production", "dev"), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("a flag missing on one side is a state, not an error: %d %s", rec.Code, rec.Body)
	}

	var got CompareResult
	decode(t, rec, &got)
	if !got.From.Present {
		t.Error("banner-test exists in production")
	}
	if got.To.Present {
		t.Error("banner-test does not exist in dev")
	}
	if len(got.Differs) == 0 {
		t.Error("a flag absent from the target is a difference")
	}
}

func TestCompareIgnoresTheTeamLabelInMetadata(t *testing.T) {
	repo := twoEnvRepo()
	repo.files["dev/payments.goff.yaml"] = strings.Replace(paymentsFile,
		"team: payments", "team: platform", 1)
	srv, sealer := testServer(t, repo, adminRules())

	rec := request(t, srv, sealer, admin(), http.MethodGet,
		compareURL("new-checkout", "dev", "production"), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	var got CompareResult
	decode(t, rec, &got)
	if strings.Contains(strings.Join(got.Differs, ","), FieldMetadata) {
		t.Errorf("team is Studio's own file label, it must not count as drift: %v", got.Differs)
	}
}

func TestCompareRejectsTheSameEnvironmentTwice(t *testing.T) {
	srv, sealer := testServer(t, twoEnvRepo(), adminRules())

	rec := request(t, srv, sealer, admin(), http.MethodGet,
		compareURL("new-checkout", "production", "production"), "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400: %s", rec.Code, rec.Body)
	}
}

func TestCompare404sWhenTheKeyExistsNowhere(t *testing.T) {
	srv, sealer := testServer(t, twoEnvRepo(), adminRules())

	rec := request(t, srv, sealer, admin(), http.MethodGet,
		compareURL("no-such-flag", "dev", "production"), "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404: %s", rec.Code, rec.Body)
	}
}

func TestCompareRefusesAnEnvironmentTheUserCannotView(t *testing.T) {
	rules := []permissions.Rule{{
		Group:        "flags-admins",
		Allow:        []string{"*"},
		Environments: []string{"dev"},
		Actions:      []string{"view"},
	}}

	srv, sealer := testServer(t, twoEnvRepo(), rules)

	rec := request(t, srv, sealer, admin(), http.MethodGet,
		compareURL("new-checkout", "dev", "production"), "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("a dev-only session must not learn about production: %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "gold-cohort") {
		t.Error("production flag detail leaked into a forbidden response")
	}
}

func TestCompareDoesNotCallEditVariationsAlonePromotable(t *testing.T) {
	rules := []permissions.Rule{{
		Group:   "flags-admins",
		Allow:   []string{"*"},
		Actions: []string{"view", "edit_variations"},
	}}

	srv, sealer := testServer(t, twoEnvRepo(), rules)

	rec := request(t, srv, sealer, admin(), http.MethodGet,
		compareURL("new-checkout", "dev", "production"), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	var got CompareResult
	decode(t, rec, &got)
	if got.To.Writable {
		t.Error("promotion saves with edit_rules, so edit_variations alone must not enable it")
	}
}

func TestCompareCallsAMissingTargetWritableWhenTheUserMayCreate(t *testing.T) {
	repo := twoEnvRepo()
	repo.files["dev/growth.goff.yaml"] = growthFile
	repo.shas["dev/growth.goff.yaml"] = "sha-dev-growth"
	delete(repo.files, "production/growth.goff.yaml")
	repo.files["production/growth.goff.yaml"] = "{}\n"
	srv, sealer := testServer(t, repo, adminRules())

	rec := request(t, srv, sealer, admin(), http.MethodGet,
		compareURL("banner-test", "dev", "production"), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	var got CompareResult
	decode(t, rec, &got)
	if got.To.Present {
		t.Fatalf("banner-test should be absent from production: %+v", got.To)
	}
	if !got.To.Writable {
		t.Error("promotion creates a missing flag, so create permission should enable it")
	}
	if got.To.Team != "growth" {
		t.Errorf("a missing target should borrow the source team to name its file, got %q", got.To.Team)
	}
}

func TestCompareMarksWhetherTheTargetIsWritable(t *testing.T) {
	rules := []permissions.Rule{{
		Group:   "flags-admins",
		Allow:   []string{"*"},
		Actions: []string{"view"},
	}}

	srv, sealer := testServer(t, twoEnvRepo(), rules)

	rec := request(t, srv, sealer, admin(), http.MethodGet,
		compareURL("new-checkout", "dev", "production"), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	var got CompareResult
	decode(t, rec, &got)
	if got.To.Writable {
		t.Error("a view-only session must not be told the target is writable")
	}
}
