package server

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-feature-flag/studio/internal/auth"
)

func TestMilestoneOneHappyPath(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())
	sess := admin()

	rec := request(t, srv, sealer, sess, http.MethodGet, "/api/me", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("me failed: %d", rec.Code)
	}

	rec = request(t, srv, sealer, sess, http.MethodGet, "/api/environments/production/flags", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list failed: %d %s", rec.Code, rec.Body)
	}

	var list ListResult
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}

	var target *FlagView
	for i := range list.Flags {
		if list.Flags[i].Key == "new-checkout" {
			target = &list.Flags[i]
		}
	}
	if target == nil {
		t.Fatal("new-checkout not in the list")
	}
	if !target.Enabled {
		t.Fatal("flag should start enabled")
	}

	body := `{"enabled":false,"fileSha":"` + target.FileSHA + `"}`
	rec = request(t, srv, sealer, sess, http.MethodPost, "/api/environments/production/flags/new-checkout/state", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("toggle failed: %d %s", rec.Code, rec.Body)
	}

	stored := repo.files["production/payments.goff.yaml"]
	if !strings.Contains(stored, "disable: true") {
		t.Fatalf("flag not disabled in the committed file:\n%s", stored)
	}

	commitMsg := repo.puts[0]["message"].(string)
	if !strings.Contains(commitMsg, "GOFF-Studio-User: ada@acme.com") {
		t.Errorf("commit is not attributed to the Okta user:\n%s", commitMsg)
	}

	rec = request(t, srv, sealer, sess, http.MethodGet, "/api/environments/production/flags/new-checkout", "")
	var after FlagView
	_ = json.Unmarshal(rec.Body.Bytes(), &after)
	if after.Enabled {
		t.Error("flag should read back as disabled")
	}
	if after.Summary != "Off for everyone" {
		t.Errorf("summary = %q", after.Summary)
	}

	preview := `{"targetingKey":"user-42","attributes":{"tier":"gold"}}`
	rec = request(t, srv, sealer, sess, http.MethodPost, "/api/environments/production/flags/new-checkout/preview", preview)
	if rec.Code != http.StatusOK {
		t.Fatalf("preview failed: %d %s", rec.Code, rec.Body)
	}
	var evaluated struct {
		Value  any    `json:"value"`
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &evaluated)
	if evaluated.Reason != "DISABLED" {
		t.Errorf("a disabled flag should evaluate as DISABLED, got %q", evaluated.Reason)
	}

	if !strings.Contains(stored, `version: "3.1.0"`) {
		t.Error("version field was dropped by the round trip")
	}
	if !strings.Contains(stored, "# Payment flags") {
		t.Error("comment was dropped by the round trip")
	}
	if repo.files["production/growth.goff.yaml"] != growthFile {
		t.Error("the untouched file changed")
	}
}

func TestCommittedFileIsLoadableByGoff(t *testing.T) {
	repo := newRepo()
	srv, sealer := testServer(t, repo, adminRules())
	sess := admin()

	rec := request(t, srv, sealer, sess, http.MethodGet, "/api/environments/production/flags", "")
	var list ListResult
	_ = json.Unmarshal(rec.Body.Bytes(), &list)

	var sha string
	for _, f := range list.Flags {
		if f.Key == "new-checkout" {
			sha = f.FileSHA
		}
	}

	body := `{"enabled":false,"fileSha":"` + sha + `"}`
	if rec := request(t, srv, sealer, sess, http.MethodPost, "/api/environments/production/flags/new-checkout/state", body); rec.Code != http.StatusOK {
		t.Fatalf("toggle failed: %s", rec.Body)
	}

	committed := repo.files["production/payments.goff.yaml"]
	if err := srv.svc.adapter.Validate([]byte(committed)); err != nil {
		t.Errorf("studio committed a file GO Feature Flag cannot load: %v\n%s", err, committed)
	}
}

func TestSessionCookieCarriesNoSecrets(t *testing.T) {
	_, sealer := testServer(t, newRepo(), adminRules())

	sess := auth.Session{
		Subject: "okta|00u1abc",
		Email:   "ada@acme.com",
		Groups:  []string{"flags-admins", "payments-team"},
	}

	rec := httptest.NewRecorder()
	if err := sealer.Write(rec, sess); err != nil {
		t.Fatal(err)
	}
	cookie := rec.Result().Cookies()[0]

	decoded, _ := base64.RawURLEncoding.DecodeString(cookie.Value)
	plain := string(decoded)
	for _, secret := range []string{"flags-admins", "payments-team", "ada@acme.com", "okta|00u1abc"} {
		if strings.Contains(plain, secret) {
			t.Errorf("%q is readable in the cookie", secret)
		}
	}
}
