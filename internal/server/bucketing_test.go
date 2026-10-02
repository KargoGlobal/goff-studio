package server

import (
	"net/http"
	"strings"
	"testing"
)

const bucketingPath = "/api/environments/production/flags/new-checkout/bucketing-key"

func TestBucketingKeyIsSavedAndCommitted(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())

	rec := request(t, srv, sealer, admin(), http.MethodPut, bucketingPath,
		`{"bucketingKey":"  account.id ","fileSha":"sha-payments"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	flag := flagNamed(t, mustLoad(t, repo, "production/payments.goff.yaml"), "new-checkout")
	if flag.BucketingKey != "account.id" {
		t.Errorf("bucketingKey = %q", flag.BucketingKey)
	}
	stored := repo.files["production/payments.goff.yaml"]
	for _, want := range []string{"# Payment flags", `version: "3.1.0"`, "bucketingKey: account.id"} {
		if !strings.Contains(stored, want) {
			t.Errorf("missing %q:\n%s", want, stored)
		}
	}
	if msg := repo.puts[0]["message"].(string); !strings.HasPrefix(msg, "[production] payments/new-checkout: split by account.id") {
		t.Errorf("commit message = %q", msg)
	}
}

func TestEmptyBucketingKeyRemovesTheField(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())

	request(t, srv, sealer, admin(), http.MethodPut, bucketingPath, `{"bucketingKey":"account.id","fileSha":"sha-payments"}`)
	rec := request(t, srv, sealer, admin(), http.MethodPut, bucketingPath, `{"bucketingKey":" "}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if got := repo.files["production/payments.goff.yaml"]; strings.Contains(got, "bucketingKey") {
		t.Errorf("bucketingKey should be gone:\n%s", got)
	}
	if !strings.Contains(repo.puts[1]["message"].(string), "split by evaluation") {
		t.Errorf("commit message = %q", repo.puts[1]["message"])
	}
}

func TestBucketingKeyNeedsEditRights(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())

	rec := request(t, srv, sealer, marketer(), http.MethodPut,
		"/api/environments/production/flags/banner-test/bucketing-key", `{"bucketingKey":"account.id"}`)
	if rec.Code != http.StatusForbidden && rec.Code != http.StatusNotFound {
		t.Errorf("toggle/rollout rights should not allow changing how a flag splits, got %d", rec.Code)
	}
	if len(repo.puts) != 0 {
		t.Error("nothing should have been committed")
	}
}

func TestBucketingKeyRejectsBadAttributes(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())

	for _, bad := range []string{"account id", "a\\u0007b", ".account", "account.", "a..b", strings.Repeat("a", MaxNameLength+1)} {
		rec := request(t, srv, sealer, admin(), http.MethodPut, bucketingPath, `{"bucketingKey":"`+bad+`"}`)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%q: status %d, want 400", bad, rec.Code)
		}
	}
	if len(repo.puts) != 0 {
		t.Error("nothing should have been committed")
	}
}

func TestBucketingKeyDiffDoesNotWrite(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())
	before := repo.files["production/payments.goff.yaml"]

	rec := request(t, srv, sealer, admin(), http.MethodPost,
		"/api/environments/production/flags/new-checkout/diff", `{"change":"bucketingKey","bucketingKey":"account.id"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var got DiffResult
	decode(t, rec, &got)
	if !strings.Contains(got.Diff, "+  bucketingKey: account.id") {
		t.Errorf("the diff should add the field: %q", got.Diff)
	}
	if repo.files["production/payments.goff.yaml"] != before {
		t.Error("a diff must not write")
	}
}

func TestCompareAndPromoteCarryTheBucketingKey(t *testing.T) {
	repo := twoEnvRepo()
	repo.files["dev/payments.goff.yaml"] = devPaymentsFile + "  bucketingKey: account.id\n"
	srv, sealer := testServer(t, repo, adminRules())

	rec := request(t, srv, sealer, admin(), http.MethodGet, compareURL("new-checkout", "dev", "production"), "")
	var cmp CompareResult
	decode(t, rec, &cmp)
	if !strings.Contains(strings.Join(cmp.Differs, ","), FieldBucketingKey) {
		t.Errorf("a different bucketing key is drift: %v", cmp.Differs)
	}

	rec = request(t, srv, sealer, admin(), http.MethodPost, "/api/flags/new-checkout/promote",
		promoteBodyJSON("dev", "production", FieldBucketingKey))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	stored := repo.files["production/payments.goff.yaml"]
	if !strings.Contains(stored, "bucketingKey: account.id") || !strings.Contains(stored, `version: "3.1.0"`) {
		t.Errorf("promotion should copy only the bucketing key:\n%s", stored)
	}
}
