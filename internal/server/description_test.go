package server

import (
	"net/http"
	"strings"
	"testing"

	"github.com/go-feature-flag/studio/internal/goff"
)

const descriptionPath = "/api/environments/production/flags/new-checkout/description"

func TestDescriptionIsStoredInMetadataAndCommitted(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())

	rec := request(t, srv, sealer, admin(), http.MethodPut, descriptionPath,
		`{"description":"  Gates the new checkout flow.  ","fileSha":"sha-payments"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	flag := flagNamed(t, mustLoad(t, repo, "production/payments.goff.yaml"), "new-checkout")
	if flag.Metadata["description"] != "Gates the new checkout flow." {
		t.Errorf("metadata = %+v", flag.Metadata)
	}
	stored := repo.files["production/payments.goff.yaml"]
	for _, want := range []string{"# Payment flags", `version: "3.1.0"`, `query: (tier eq "gold") or (account_id eq "42")`} {
		if !strings.Contains(stored, want) {
			t.Errorf("lost %q:\n%s", want, stored)
		}
	}
	if msg := repo.puts[0]["message"].(string); !strings.HasPrefix(msg, "[production] payments/new-checkout: updated the description") {
		t.Errorf("commit message = %q", msg)
	}
}

func TestEmptyDescriptionRemovesTheKey(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())

	request(t, srv, sealer, admin(), http.MethodPut, descriptionPath, `{"description":"temp","fileSha":"sha-payments"}`)
	rec := request(t, srv, sealer, admin(), http.MethodPut, descriptionPath, `{"description":"   "}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	flag := flagNamed(t, mustLoad(t, repo, "production/payments.goff.yaml"), "new-checkout")
	if _, ok := flag.Metadata["description"]; ok {
		t.Errorf("description should be gone, metadata = %+v", flag.Metadata)
	}
	if msg := repo.puts[1]["message"].(string); !strings.Contains(msg, "removed the description") {
		t.Errorf("commit message = %q", msg)
	}
}

func TestDescriptionNeedsEditRights(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())

	rec := request(t, srv, sealer, marketer(), http.MethodPut,
		"/api/environments/production/flags/banner-test/description", `{"description":"nope"}`)
	if rec.Code != http.StatusForbidden && rec.Code != http.StatusNotFound {
		t.Errorf("toggle/rollout rights should not allow editing the description, got %d", rec.Code)
	}
	if len(repo.puts) != 0 {
		t.Error("nothing should have been committed")
	}
}

func TestDescriptionRejectsLineBreaksAndOverlongText(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())

	for _, body := range []string{
		`{"description":"line one\nline two"}`,
		`{"description":"` + strings.Repeat("a", MaxDescription+1) + `"}`,
	} {
		rec := request(t, srv, sealer, admin(), http.MethodPut, descriptionPath, body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("want 400, got %d: %s", rec.Code, rec.Body)
		}
	}
	if len(repo.puts) != 0 {
		t.Error("nothing should have been committed")
	}
}

func TestDescriptionDiffPreviewsWithoutWriting(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())

	rec := request(t, srv, sealer, admin(), http.MethodPost,
		"/api/environments/production/flags/new-checkout/diff", `{"change":"description","description":"Gates checkout."}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if body := rec.Body.String(); !strings.Contains(body, "Update the description of new-checkout") || !strings.Contains(body, "+    description: Gates checkout.") {
		t.Errorf("diff = %s", body)
	}
	if len(repo.puts) != 0 {
		t.Error("a diff must not commit")
	}
}

func TestCreateStoresTheDescription(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())
	warmCache(t, srv, sealer, admin(), "production")

	body := `{"key":"dark-mode","team":"growth","type":"boolean","description":"Dark theme for the dashboard.",
		"variations":[{"name":"on","value":"true"},{"name":"off","value":"false"}],
		"default":"off","enabled":true,"fileSha":"sha-growth"}`
	if rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/environments/production/flags", body); rec.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	flag := flagNamed(t, mustLoad(t, repo, "production/growth.goff.yaml"), "dark-mode")
	if flag.Metadata["description"] != "Dark theme for the dashboard." || flag.Metadata["team"] != "growth" {
		t.Errorf("metadata = %+v", flag.Metadata)
	}
}

func flagNamed(t *testing.T, flags []goff.Flag, key string) goff.Flag {
	t.Helper()
	for _, f := range flags {
		if f.Key == key {
			return f
		}
	}
	t.Fatalf("flag %q missing, got %v", key, keysOf(flags))
	return goff.Flag{}
}

func TestCreateRejectsALeadingLineBreakInTheDescription(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())
	warmCache(t, srv, sealer, admin(), "production")

	body := `{"key":"dark-mode","team":"growth","type":"boolean","description":"\nDark theme.",
		"variations":[{"name":"on","value":"true"},{"name":"off","value":"false"}],
		"default":"off","enabled":true,"fileSha":"sha-growth"}`
	if rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/environments/production/flags", body); rec.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d: %s", rec.Code, rec.Body)
	}
	if len(repo.puts) != 0 {
		t.Error("nothing should have been committed")
	}
}

func TestDescriptionLimitCountsCharactersNotBytes(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())

	accented := strings.Repeat("é", MaxDescription)
	rec := request(t, srv, sealer, admin(), http.MethodPut, descriptionPath, `{"description":"`+accented+`"}`)
	if rec.Code != http.StatusOK {
		t.Errorf("%d accented characters should fit, got %d: %s", MaxDescription, rec.Code, rec.Body)
	}
	rec = request(t, srv, sealer, admin(), http.MethodPut, descriptionPath, `{"description":"`+accented+`é"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("one past the limit should be refused, got %d", rec.Code)
	}
}
