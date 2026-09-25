package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-feature-flag/studio/internal/goff"
	"github.com/go-feature-flag/studio/internal/permissions"
)

func promoteBodyJSON(from, to string, fields ...string) string {
	b, _ := json.Marshal(map[string]any{"from": from, "to": to, "fields": fields})
	return string(b)
}

func TestPromoteCopiesRulesAndDefault(t *testing.T) {
	repo := twoEnvRepo()
	srv, sealer := testServer(t, repo, adminRules())

	rec := request(t, srv, sealer, admin(), http.MethodPost,
		"/api/flags/new-checkout/promote",
		promoteBodyJSON("dev", "production", FieldRules, FieldDefault))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	stored := repo.files["production/payments.goff.yaml"]
	if !strings.Contains(stored, `"on": 50`) {
		t.Errorf("dev's 50/50 split did not land:\n%s", stored)
	}
	if !strings.Contains(stored, `variation: "on"`) {
		t.Errorf("dev's default did not land:\n%s", stored)
	}
	if !strings.Contains(stored, `version: "3.1.0"`) {
		t.Errorf("promotion must not disturb preserved fields:\n%s", stored)
	}
}

func TestPromoteLeavesAnExistingFlagsStateAlone(t *testing.T) {
	repo := twoEnvRepo()
	repo.files["dev/payments.goff.yaml"] = devPaymentsFile + "  disable: true\n"
	srv, sealer := testServer(t, repo, adminRules())

	before := repo.files["production/payments.goff.yaml"]
	if strings.Contains(before, "disable") {
		t.Fatalf("fixture should start enabled:\n%s", before)
	}

	rec := request(t, srv, sealer, admin(), http.MethodPost,
		"/api/flags/new-checkout/promote",
		promoteBodyJSON("dev", "production", FieldRules))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	stored := repo.files["production/payments.goff.yaml"]
	if strings.Contains(stored, "disable: true") {
		t.Errorf("promoting targeting must not turn a live flag off:\n%s", stored)
	}
}

func TestPromoteCopiesStateOnlyWhenAsked(t *testing.T) {
	repo := twoEnvRepo()
	repo.files["dev/payments.goff.yaml"] = devPaymentsFile + "  disable: true\n"
	srv, sealer := testServer(t, repo, adminRules())

	rec := request(t, srv, sealer, admin(), http.MethodPost,
		"/api/flags/new-checkout/promote",
		promoteBodyJSON("dev", "production", FieldEnabled))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	stored := repo.files["production/payments.goff.yaml"]
	if !strings.Contains(stored, "disable: true") {
		t.Errorf("an explicit enabled promotion should apply:\n%s", stored)
	}
}

func TestPromoteCreatesAMissingFlagTurnedOff(t *testing.T) {
	repo := twoEnvRepo()
	repo.files["dev/growth.goff.yaml"] = growthFile
	repo.shas["dev/growth.goff.yaml"] = "sha-dev-growth"
	delete(repo.files, "production/growth.goff.yaml")
	repo.files["production/growth.goff.yaml"] = "{}\n"
	srv, sealer := testServer(t, repo, adminRules())

	rec := request(t, srv, sealer, admin(), http.MethodPost,
		"/api/flags/banner-test/promote",
		promoteBodyJSON("dev", "production", FieldRules, FieldVariations, FieldDefault))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	stored := repo.files["production/growth.goff.yaml"]
	if !strings.Contains(stored, "banner-test") {
		t.Fatalf("the flag was not created:\n%s", stored)
	}
	if !strings.Contains(stored, "disable: true") {
		t.Errorf("a flag arriving in a new environment must be created off:\n%s", stored)
	}
}

// A ramp that finished long ago is fully rolled out, so freezing must land on its end split.
func TestPromoteFreezesAFinishedRampAtItsEndAllocation(t *testing.T) {
	repo := twoEnvRepo()
	past := strings.NewReplacer(
		"2026-10-01T00:00:00Z", "2020-01-01T00:00:00Z",
		"2026-11-01T00:00:00Z", "2020-02-01T00:00:00Z",
		"percentage: 100", "percentage: 60",
	).Replace(rampFile)
	repo.files["dev/platform.goff.yaml"] = past
	repo.shas["dev/platform.goff.yaml"] = "sha-dev-platform"
	repo.files["production/platform.goff.yaml"] = rampFile
	repo.shas["production/platform.goff.yaml"] = "sha-prod-platform"
	srv, sealer := testServer(t, repo, adminRules())

	rec := request(t, srv, sealer, admin(), http.MethodPost,
		"/api/flags/ramped/promote",
		promoteBodyJSON("dev", "production", FieldRules))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	stored := repo.files["production/platform.goff.yaml"]
	if strings.Contains(stored, "progressiveRollout") {
		t.Errorf("a ramp's source dates are meaningless in the target, it should be frozen:\n%s", stored)
	}
	if !strings.Contains(stored, `"on": 60`) || !strings.Contains(stored, `"off": 40`) {
		t.Errorf("a finished ramp should freeze at its end allocation:\n%s", stored)
	}
}

// The bug CodeRabbit caught: a ramp still in progress must not fast-forward to 100%.
func TestPromoteFreezesAnInFlightRampWhereItHasReached(t *testing.T) {
	repo := twoEnvRepo()
	half := strings.NewReplacer(
		"2026-10-01T00:00:00Z", time.Now().Add(-1*time.Hour).UTC().Format(time.RFC3339),
		"2026-11-01T00:00:00Z", time.Now().Add(1*time.Hour).UTC().Format(time.RFC3339),
	).Replace(rampFile)
	repo.files["dev/platform.goff.yaml"] = half
	repo.shas["dev/platform.goff.yaml"] = "sha-dev-platform"
	repo.files["production/platform.goff.yaml"] = rampFile
	repo.shas["production/platform.goff.yaml"] = "sha-prod-platform"
	srv, sealer := testServer(t, repo, adminRules())

	rec := request(t, srv, sealer, admin(), http.MethodPost,
		"/api/flags/ramped/promote",
		promoteBodyJSON("dev", "production", FieldRules))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	stored := repo.files["production/platform.goff.yaml"]
	if strings.Contains(stored, `"on": 100`) {
		t.Errorf("a half-elapsed 0-to-100 ramp must not fast-forward production:\n%s", stored)
	}
	if !strings.Contains(stored, `"on": 5`) {
		t.Errorf("a half-elapsed ramp should sit near 50%%:\n%s", stored)
	}
}

func TestFrozenOutcomeHandlesOneVariationOnBothSteps(t *testing.T) {
	p := goff.ProgressiveRollout{
		Initial: goff.RolloutStep{Variation: "on", Percentage: 10, Date: "2020-01-01T00:00:00Z"},
		End:     goff.RolloutStep{Variation: "on", Percentage: 90, Date: "2020-02-01T00:00:00Z"},
	}
	got := frozenOutcome(p, time.Now()).Percentage
	if len(got) != 1 || got["on"] != 90 {
		t.Errorf("one variation on both steps should not collide or zero out: %v", got)
	}
}

func TestReachedPercentageClampsToTheWindow(t *testing.T) {
	p := goff.ProgressiveRollout{
		Initial: goff.RolloutStep{Variation: "off", Percentage: 0, Date: "2026-10-01T00:00:00Z"},
		End:     goff.RolloutStep{Variation: "on", Percentage: 100, Date: "2026-11-01T00:00:00Z"},
	}

	before := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	if got := reachedPercentage(p, before); got != 0 {
		t.Errorf("before the ramp starts it should sit at the initial percentage, got %v", got)
	}

	after := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	if got := reachedPercentage(p, after); got != 100 {
		t.Errorf("after the ramp ends it should sit at the end percentage, got %v", got)
	}

	mid := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	if got := reachedPercentage(p, mid); got < 45 || got > 55 {
		t.Errorf("halfway through it should sit near 50, got %v", got)
	}

	unparseable := goff.ProgressiveRollout{
		Initial: goff.RolloutStep{Variation: "off", Percentage: 0, Date: "whenever"},
		End:     goff.RolloutStep{Variation: "on", Percentage: 100, Date: "later"},
	}
	if got := reachedPercentage(unparseable, time.Now()); got != 100 {
		t.Errorf("an unparseable ramp should fall back to its end percentage, got %v", got)
	}
}

func TestPromoteRefusesRulesServingAVariationTheTargetLacks(t *testing.T) {
	repo := twoEnvRepo()
	repo.files["dev/payments.goff.yaml"] = `new-checkout:
  variations:
    on: true
    off: false
    holdback: false
  targeting:
    - name: gold-cohort
      query: (tier eq "gold")
      variation: holdback
  defaultRule:
    variation: "off"
`
	srv, sealer := testServer(t, repo, adminRules())
	before := repo.files["production/payments.goff.yaml"]

	rec := request(t, srv, sealer, admin(), http.MethodPost,
		"/api/flags/new-checkout/promote",
		promoteBodyJSON("dev", "production", FieldRules))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400: %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "holdback") {
		t.Errorf("the error should name the missing variation: %s", rec.Body)
	}
	if repo.files["production/payments.goff.yaml"] != before {
		t.Error("a refused promotion must not write")
	}
}

func TestPromoteAllowsRulesWhenVariationsComeTogether(t *testing.T) {
	repo := twoEnvRepo()
	repo.files["dev/payments.goff.yaml"] = `new-checkout:
  variations:
    on: true
    off: false
    holdback: false
  targeting:
    - name: gold-cohort
      query: (tier eq "gold")
      variation: holdback
  defaultRule:
    variation: "off"
`
	srv, sealer := testServer(t, repo, adminRules())

	rec := request(t, srv, sealer, admin(), http.MethodPost,
		"/api/flags/new-checkout/promote",
		promoteBodyJSON("dev", "production", FieldRules, FieldVariations))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if !strings.Contains(repo.files["production/payments.goff.yaml"], "holdback") {
		t.Error("the new variation should have landed with the rules")
	}
}

func TestPromoteNeedsWritePermissionOnTheTarget(t *testing.T) {
	rules := []permissions.Rule{{
		Group:   "flags-admins",
		Allow:   []string{"*"},
		Actions: []string{"view"},
	}}

	repo := twoEnvRepo()
	srv, sealer := testServer(t, repo, rules)
	before := repo.files["production/payments.goff.yaml"]

	rec := request(t, srv, sealer, admin(), http.MethodPost,
		"/api/flags/new-checkout/promote",
		promoteBodyJSON("dev", "production", FieldRules))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403: %s", rec.Code, rec.Body)
	}
	if repo.files["production/payments.goff.yaml"] != before {
		t.Error("a forbidden promotion must not write")
	}
}

func TestPromoteRejectsBadRequests(t *testing.T) {
	srv, sealer := testServer(t, twoEnvRepo(), adminRules())

	cases := map[string]string{
		"same environment": promoteBodyJSON("dev", "dev", FieldRules),
		"no fields":        promoteBodyJSON("dev", "production"),
		"unknown field":    promoteBodyJSON("dev", "production", "bucketingKey"),
	}

	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			rec := request(t, srv, sealer, admin(), http.MethodPost,
				"/api/flags/new-checkout/promote", body)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status %d, want 400: %s", rec.Code, rec.Body)
			}
		})
	}
}

func TestPromote404sWhenTheSourceFlagIsMissing(t *testing.T) {
	srv, sealer := testServer(t, twoEnvRepo(), adminRules())

	rec := request(t, srv, sealer, admin(), http.MethodPost,
		"/api/flags/banner-test/promote",
		promoteBodyJSON("dev", "production", FieldRules))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404: %s", rec.Code, rec.Body)
	}
}

func TestPromoteDiffDoesNotWrite(t *testing.T) {
	repo := twoEnvRepo()
	srv, sealer := testServer(t, repo, adminRules())
	before := repo.files["production/payments.goff.yaml"]

	rec := request(t, srv, sealer, admin(), http.MethodPost,
		"/api/flags/new-checkout/promote/diff",
		promoteBodyJSON("dev", "production", FieldRules))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	var got DiffResult
	decode(t, rec, &got)
	if !strings.Contains(got.Description, "Promote new-checkout from dev to production") {
		t.Errorf("description should name the promotion: %q", got.Description)
	}
	if got.Diff == "" {
		t.Error("a promotion diff should show the change")
	}
	if repo.files["production/payments.goff.yaml"] != before {
		t.Error("a diff must not write")
	}
}

func TestPromoteRefusesADefaultServingAVariationTheTargetLacks(t *testing.T) {
	repo := twoEnvRepo()
	repo.files["dev/payments.goff.yaml"] = `new-checkout:
  variations:
    on: true
    off: false
    holdback: false
  defaultRule:
    variation: holdback
`
	srv, sealer := testServer(t, repo, adminRules())
	before := repo.files["production/payments.goff.yaml"]

	rec := request(t, srv, sealer, admin(), http.MethodPost,
		"/api/flags/new-checkout/promote",
		promoteBodyJSON("dev", "production", FieldDefault))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400: %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "holdback") {
		t.Errorf("the error should name the missing variation: %s", rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "default rule") {
		t.Errorf("the error should say it was the default: %s", rec.Body)
	}
	if repo.files["production/payments.goff.yaml"] != before {
		t.Error("a refused promotion must not write")
	}
}

func TestPromoteAllowsADefaultAlreadyPresentInTheTarget(t *testing.T) {
	repo := twoEnvRepo()
	srv, sealer := testServer(t, repo, adminRules())

	rec := request(t, srv, sealer, admin(), http.MethodPost,
		"/api/flags/new-checkout/promote",
		promoteBodyJSON("dev", "production", FieldDefault))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
}
