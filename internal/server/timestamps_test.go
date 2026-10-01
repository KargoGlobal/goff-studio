package server

import (
	"net/http"
	"strings"
	"testing"
)

const stampedNow = `"2030-06-15T12:00:00Z"`

func TestCreateStampsCreatedAndUpdated(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())
	warmCache(t, srv, sealer, admin(), "production")

	body := `{"key":"fresh","team":"payments","type":"boolean","enabled":true,` +
		`"variations":[{"name":"on","value":"true"},{"name":"off","value":"false"}],"default":"off","fileSha":"sha-payments"}`
	rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/environments/production/flags", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	stored := repo.files["production/payments.goff.yaml"]
	for _, want := range []string{"createdAt: " + stampedNow, "updatedAt: " + stampedNow} {
		if !strings.Contains(stored, want) {
			t.Errorf("missing %s in:\n%s", want, stored)
		}
	}
}

func TestEditStampsUpdatedAndKeepsCreated(t *testing.T) {
	repo := newRepo()
	repo.files["production/payments.goff.yaml"] = paymentsFile +
		"  metadata:\n    createdAt: \"2029-01-01T00:00:00Z\"\n    updatedAt: \"2029-01-01T00:00:00Z\"\n"
	srv, sealer := testServer(t, repo, adminRules())

	rec := request(t, srv, sealer, admin(), http.MethodPost,
		"/api/environments/production/flags/new-checkout/state", `{"enabled":false,"fileSha":"sha-payments"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	stored := repo.files["production/payments.goff.yaml"]
	if !strings.Contains(stored, `createdAt: "2029-01-01T00:00:00Z"`) {
		t.Errorf("an edit must keep createdAt:\n%s", stored)
	}
	if !strings.Contains(stored, "updatedAt: "+stampedNow) || strings.Count(stored, "updatedAt") != 1 {
		t.Errorf("an edit must replace updatedAt in place:\n%s", stored)
	}
}

func TestEditOfAnUnstampedFlagAddsOnlyUpdated(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())

	rec := request(t, srv, sealer, admin(), http.MethodPost,
		"/api/environments/production/flags/new-checkout/state", `{"enabled":false,"fileSha":"sha-payments"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	stored := repo.files["production/payments.goff.yaml"]
	if strings.Contains(stored, "createdAt") {
		t.Errorf("Studio cannot know when an existing flag was created, so it must not guess:\n%s", stored)
	}
	if !strings.Contains(stored, "updatedAt: "+stampedNow) {
		t.Errorf("missing updatedAt:\n%s", stored)
	}
}

func TestRenameStampsUpdated(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())
	warmCache(t, srv, sealer, admin(), "production")

	rec := request(t, srv, sealer, admin(), http.MethodPost,
		"/api/environments/production/flags/new-checkout/key", `{"key":"renamed-checkout","fileSha":"sha-payments"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if stored := repo.files["production/payments.goff.yaml"]; !strings.Contains(stored, "updatedAt: "+stampedNow) {
		t.Errorf("a rename is an edit and must stamp updatedAt:\n%s", stored)
	}
}

func TestPromoteKeepsTheTargetsOwnTimestamps(t *testing.T) {
	repo := twoEnvRepo()
	repo.files["dev/payments.goff.yaml"] = devPaymentsFile +
		"  metadata:\n    createdAt: \"2020-01-01T00:00:00Z\"\n    owner: jane\n"
	srv, sealer := testServer(t, repo, adminRules())

	rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/flags/new-checkout/promote",
		promoteBodyJSON("dev", "production", FieldMetadata))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	stored := repo.files["production/payments.goff.yaml"]
	if strings.Contains(stored, "2020-01-01") {
		t.Errorf("dev's createdAt must not be copied to production:\n%s", stored)
	}
	if !strings.Contains(stored, "owner: jane") || !strings.Contains(stored, "updatedAt: "+stampedNow) {
		t.Errorf("other metadata should be copied and the target stamped:\n%s", stored)
	}
}

func TestTimestampsAreNotReportedAsDrift(t *testing.T) {
	if !sameMetadata(
		map[string]any{"team": "a", "createdAt": "x", "updatedAt": "y", "owner": "jane"},
		map[string]any{"team": "b", "updatedAt": "z", "owner": "jane"},
	) {
		t.Error("Studio's own timestamps differ per environment and must not count as drift")
	}
}
