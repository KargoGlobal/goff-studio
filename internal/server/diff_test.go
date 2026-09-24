package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func decode(t *testing.T, rec *httptest.ResponseRecorder, out any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
		t.Fatalf("decoding response: %v\n%s", err, rec.Body)
	}
}

func TestUnifiedDiffShowsOnlyTheChange(t *testing.T) {
	before := `alpha:
  variations:
    on: true
    off: false
  defaultRule:
    variation: "off"
`
	after := `alpha:
  disable: true
  variations:
    on: true
    off: false
  defaultRule:
    variation: "off"
`

	diff := UnifiedDiff(before, after, 1)
	if !strings.Contains(diff, "+  disable: true") {
		t.Errorf("added line missing:\n%s", diff)
	}
	if strings.Count(diff, "\n+") > 1 {
		t.Errorf("only one line should be added:\n%s", diff)
	}
	if strings.Contains(diff, "-") && !strings.Contains(diff, "@@") {
		t.Errorf("nothing should be removed:\n%s", diff)
	}
}

func TestUnifiedDiffCollapsesUnchangedRegions(t *testing.T) {
	var lines []string
	for i := 0; i < 40; i++ {
		lines = append(lines, "line")
	}
	before := strings.Join(lines, "\n")
	after := before + "\nnew tail"

	diff := UnifiedDiff(before, after, 2)
	if !strings.Contains(diff, "unchanged lines") {
		t.Errorf("long unchanged runs should collapse:\n%s", diff)
	}
	if strings.Count(diff, "\n") > 10 {
		t.Errorf("diff should stay short:\n%s", diff)
	}
}

func TestUnifiedDiffEmptyWhenIdentical(t *testing.T) {
	if got := UnifiedDiff("same\n", "same\n", 3); got != "" {
		t.Errorf("identical input should produce no diff, got %q", got)
	}
}

func TestDiffEndpointReturnsDescriptionAndDiff(t *testing.T) {
	srv, sealer := testServer(t, newRepo(), adminRules())

	body := `{"change":"state","enabled":false}`
	rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/environments/production/flags/new-checkout/diff", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	var out struct {
		Description string `json:"description"`
		Diff        string `json:"diff"`
	}
	decode(t, rec, &out)

	if !strings.Contains(out.Description, "Turn new-checkout off") {
		t.Errorf("description should be plain language, got %q", out.Description)
	}
	if !strings.Contains(out.Diff, "disable: true") {
		t.Errorf("diff should show the change:\n%s", out.Diff)
	}
	if strings.Contains(out.Diff, "growth") {
		t.Error("diff should be scoped to the one file")
	}
}

func TestDiffEndpointDoesNotCommit(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())

	body := `{"change":"state","enabled":false}`
	if rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/environments/production/flags/new-checkout/diff", body); rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}

	if len(repo.puts) != 0 {
		t.Error("previewing a diff must not write anything")
	}
	if repo.files["production/payments.goff.yaml"] != paymentsFile {
		t.Error("the repo contents changed")
	}
}

func TestDiffRolloutDescribesTheSplit(t *testing.T) {
	srv, sealer := testServer(t, newRepo(), adminRules())

	body := `{"change":"rollout","ruleName":"gold-cohort","percentage":{"on":50,"off":50}}`
	rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/environments/production/flags/new-checkout/diff", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	var out struct {
		Description string `json:"description"`
		Diff        string `json:"diff"`
	}
	decode(t, rec, &out)

	if !strings.Contains(out.Description, "gold-cohort") || !strings.Contains(out.Description, "50%") {
		t.Errorf("description = %q", out.Description)
	}
	if !strings.Contains(out.Diff, "50") {
		t.Errorf("diff should show the new percentages:\n%s", out.Diff)
	}
}

func TestDiffRejectsUnknownChangeType(t *testing.T) {
	srv, sealer := testServer(t, newRepo(), adminRules())
	rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/environments/production/flags/new-checkout/diff", `{"change":"nonsense"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}
