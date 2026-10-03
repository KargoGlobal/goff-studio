package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-feature-flag/studio/internal/config"
	"github.com/go-feature-flag/studio/internal/permissions"
)

const (
	responderToken = "responder-secret-token"
	readerToken    = "reader-secret-token"
)

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func automationRules() []permissions.Rule {
	return []permissions.Rule{
		{Group: "flags-admins", Allow: []string{"*"}},
		{Group: "responders", Allow: []string{"*"}, Environments: []string{"production"}, Actions: []string{"toggle"}},
		{Group: "readers", Allow: []string{"payments"}, Actions: []string{"view"}},
	}
}

func withTokens(cfg *config.Config) {
	cfg.Server.BaseURL = "https://studio.example.com"
	cfg.APITokens = []config.APIToken{
		{Name: "incident-bot", SHA256: sha256Hex(responderToken), Groups: []string{"responders"}},
		{Name: "reader", SHA256: sha256Hex(readerToken), Groups: []string{"readers"}},
	}
}

func tokenRequest(t *testing.T, srv *Server, token, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, target, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, target, nil)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

type hookRecorder struct {
	mu       sync.Mutex
	bodies   [][]byte
	headers  []http.Header
	status   int
	attempts int
}

func (h *hookRecorder) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		h.mu.Lock()
		defer h.mu.Unlock()
		h.attempts++
		if h.status != 0 {
			w.WriteHeader(h.status)
			return
		}
		h.bodies = append(h.bodies, body)
		h.headers = append(h.headers, r.Header.Clone())
	}))
	t.Cleanup(srv.Close)
	return srv
}

func lastPutMessage(t *testing.T, repo *repoState) string {
	t.Helper()
	repo.mu.Lock()
	defer repo.mu.Unlock()
	if len(repo.puts) == 0 {
		t.Fatal("nothing was committed")
	}
	msg, _ := repo.puts[len(repo.puts)-1]["message"].(string)
	return msg
}

func TestTokenDisablesAFlagWithReasonAndReference(t *testing.T) {
	repo := newRepo()
	srv, _, _ := testServerWith(t, repo, automationRules(), withTokens)

	rec := tokenRequest(t, srv, responderToken, http.MethodPost, "/api/environments/production/flags/new-checkout/state",
		`{"enabled":false,"reason":"checkout 5xx rate above 2%","reference":"INC-42"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	for _, f := range mustLoad(t, repo, "production/payments.goff.yaml") {
		if f.Key == "new-checkout" && f.Enabled {
			t.Error("flag is still enabled")
		}
	}

	msg := lastPutMessage(t, repo)
	for _, want := range []string{
		"[production] payments/new-checkout: disabled",
		"Reason: checkout 5xx rate above 2%",
		"Reference: INC-42",
		"GOFF-Studio-User: incident-bot@api-token.invalid",
		"GOFF-Studio-User-Id: token:incident-bot",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("commit message lacks %q:\n%s", want, msg)
		}
	}
}

func TestDisablingAnAlreadyDisabledFlagWritesNothing(t *testing.T) {
	repo := newRepo()
	srv, _, _ := testServerWith(t, repo, automationRules(), withTokens)

	target := "/api/environments/production/flags/new-checkout/state"
	if rec := tokenRequest(t, srv, responderToken, http.MethodPost, target, `{"enabled":false}`); rec.Code != http.StatusOK {
		t.Fatalf("first disable: %d %s", rec.Code, rec.Body)
	}
	rec := tokenRequest(t, srv, responderToken, http.MethodPost, target, `{"enabled":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("second disable: %d %s", rec.Code, rec.Body)
	}
	var result SaveResult
	decode(t, rec, &result)
	if !result.Unchanged || result.Commit != "" {
		t.Errorf("a repeat disable should report unchanged, got %+v", result)
	}
	repo.mu.Lock()
	defer repo.mu.Unlock()
	if len(repo.puts) != 1 {
		t.Errorf("want exactly one commit, got %d", len(repo.puts))
	}
}

func TestTokenPermissionsAreItsGroups(t *testing.T) {
	repo := newRepo()
	srv, _, _ := testServerWith(t, repo, automationRules(), withTokens)

	rec := tokenRequest(t, srv, readerToken, http.MethodPost, "/api/environments/production/flags/new-checkout/state", `{"enabled":false}`)
	if rec.Code != http.StatusForbidden {
		t.Errorf("a view-only token toggled a flag: %d %s", rec.Code, rec.Body)
	}
	rec = tokenRequest(t, srv, readerToken, http.MethodGet, "/api/environments/production/flags/banner-test", "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("reader saw a flag outside its files: %d", rec.Code)
	}
	rec = tokenRequest(t, srv, readerToken, http.MethodGet, "/api/environments/production/flags/new-checkout", "")
	if rec.Code != http.StatusOK {
		t.Errorf("reader could not read its own flag: %d %s", rec.Code, rec.Body)
	}
	rec = tokenRequest(t, srv, responderToken, http.MethodPost, "/api/environments/production/flags/new-checkout/rollout",
		`{"percentage":{"on":50,"off":50}}`)
	if rec.Code != http.StatusForbidden {
		t.Errorf("a toggle-only token changed a rollout: %d %s", rec.Code, rec.Body)
	}

	repo.mu.Lock()
	defer repo.mu.Unlock()
	if len(repo.puts) != 0 {
		t.Errorf("nothing should have been written, got %d commits", len(repo.puts))
	}
}

func TestBadTokenIsRejectedEvenWithAValidCookie(t *testing.T) {
	srv, sealer, _ := testServerWith(t, newRepo(), automationRules(), withTokens)

	rec := tokenRequest(t, srv, "not-a-real-token", http.MethodGet, "/api/me", "")
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("unknown token: %d", rec.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req.Header.Set("Authorization", "Bearer not-a-real-token")
	cookie := httptest.NewRecorder()
	if err := sealer.Write(cookie, *admin()); err != nil {
		t.Fatal(err)
	}
	req.AddCookie(cookie.Result().Cookies()[0])
	out := httptest.NewRecorder()
	srv.Handler().ServeHTTP(out, req)
	if out.Code != http.StatusUnauthorized {
		t.Errorf("a bad bearer must not fall back to the cookie, got %d", out.Code)
	}
}

func TestReasonMustBeOneLine(t *testing.T) {
	repo := newRepo()
	srv, _, _ := testServerWith(t, repo, automationRules(), withTokens)

	rec := tokenRequest(t, srv, responderToken, http.MethodPost, "/api/environments/production/flags/new-checkout/state",
		`{"enabled":false,"reason":"line one\nGOFF-Studio-User: someone-else@example.com"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400: %s", rec.Code, rec.Body)
	}
	rec = tokenRequest(t, srv, responderToken, http.MethodPost, "/api/environments/production/flags/new-checkout/state",
		`{"enabled":false,"reason":"`+strings.Repeat("x", maxNoteReason+1)+`"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("over-long reason: status %d", rec.Code)
	}
}

func TestChangesAreNotified(t *testing.T) {
	jsonHook, slackHook, devOnly := &hookRecorder{}, &hookRecorder{}, &hookRecorder{}
	jsonSrv, slackSrv, devSrv := jsonHook.server(t), slackHook.server(t), devOnly.server(t)

	repo := newRepo()
	srv, _, svc := testServerWith(t, repo, automationRules(), func(cfg *config.Config) {
		withTokens(cfg)
		cfg.Notifications = []config.Notification{
			{URL: jsonSrv.URL, Format: config.NotifyJSON, Secret: "hook-secret"},
			{URL: slackSrv.URL, Format: config.NotifySlack, Environments: []string{"production"}},
			{URL: devSrv.URL, Format: config.NotifyJSON, Environments: []string{"dev"}},
		}
	})

	rec := tokenRequest(t, srv, responderToken, http.MethodPost, "/api/environments/production/flags/new-checkout/state",
		`{"enabled":false,"reason":"errors <spiking> & rising","reference":"INC-42"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	svc.notify.wait()

	if len(jsonHook.bodies) != 1 {
		t.Fatalf("json hook got %d deliveries", len(jsonHook.bodies))
	}
	var ev ChangeEvent
	if err := json.Unmarshal(jsonHook.bodies[0], &ev); err != nil {
		t.Fatal(err)
	}
	if ev.Event != EventFlagChanged || ev.Environment != "production" || ev.Team != "payments" || ev.Flag != "new-checkout" ||
		ev.Summary != "disabled" || ev.Reason != "errors <spiking> & rising" || ev.Reference != "INC-42" ||
		ev.Actor.ID != "token:incident-bot" || ev.Version != "new-commit" {
		t.Errorf("unexpected event: %+v", ev)
	}
	if ev.URL != "https://studio.example.com/env/production/flags/new-checkout" {
		t.Errorf("url = %q", ev.URL)
	}

	mac := hmac.New(sha256.New, []byte("hook-secret"))
	mac.Write(jsonHook.bodies[0])
	if got, want := jsonHook.headers[0].Get(signatureHeader), "sha256="+hex.EncodeToString(mac.Sum(nil)); got != want {
		t.Errorf("signature = %q, want %q", got, want)
	}

	if len(slackHook.bodies) != 1 {
		t.Fatalf("slack hook got %d deliveries", len(slackHook.bodies))
	}
	var msg map[string]string
	if err := json.Unmarshal(slackHook.bodies[0], &msg); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"*[production]*", "new-checkout", "disabled", "incident-bot (API token)", "errors &lt;spiking&gt; &amp; rising", "INC-42"} {
		if !strings.Contains(msg["text"], want) {
			t.Errorf("slack text lacks %q: %s", want, msg["text"])
		}
	}
	if slackHook.headers[0].Get(signatureHeader) != "" {
		t.Error("a hook without a secret should not be signed")
	}

	if devOnly.attempts != 0 {
		t.Error("a hook limited to dev was told about production")
	}
}

func TestAFailingHookDoesNotFailTheSave(t *testing.T) {
	broken := &hookRecorder{status: http.StatusBadRequest}
	brokenSrv := broken.server(t)

	repo := newRepo()
	srv, _, svc := testServerWith(t, repo, automationRules(), func(cfg *config.Config) {
		withTokens(cfg)
		cfg.Notifications = []config.Notification{{URL: brokenSrv.URL, Format: config.NotifyJSON}}
	})

	rec := tokenRequest(t, srv, responderToken, http.MethodPost, "/api/environments/production/flags/new-checkout/state", `{"enabled":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	svc.notify.wait()
	if broken.attempts != 1 {
		t.Errorf("a 4xx should not be retried, got %d attempts", broken.attempts)
	}
}

func TestRetriesAServerError(t *testing.T) {
	defer func(old time.Duration) { notifyRetryBackoff = old }(notifyRetryBackoff)
	notifyRetryBackoff = time.Millisecond

	flaky := &hookRecorder{status: http.StatusServiceUnavailable}
	flakySrv := flaky.server(t)

	repo := newRepo()
	srv, _, svc := testServerWith(t, repo, automationRules(), func(cfg *config.Config) {
		withTokens(cfg)
		cfg.Notifications = []config.Notification{{URL: flakySrv.URL, Format: config.NotifyJSON}}
	})
	if rec := tokenRequest(t, srv, responderToken, http.MethodPost, "/api/environments/production/flags/new-checkout/state", `{"enabled":false}`); rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	svc.notify.wait()
	if flaky.attempts != notifyAttempts {
		t.Errorf("want %d attempts, got %d", notifyAttempts, flaky.attempts)
	}
}

func TestUIEditsAreNotifiedToo(t *testing.T) {
	hook := &hookRecorder{}
	hookSrv := hook.server(t)

	repo := newRepo()
	srv, sealer, svc := testServerWith(t, repo, adminRules(), func(cfg *config.Config) {
		cfg.Notifications = []config.Notification{{URL: hookSrv.URL, Format: config.NotifyJSON}}
	})
	rec := request(t, srv, sealer, admin(), http.MethodPost, "/api/environments/production/flags/banner-test/rollout",
		`{"percentage":{"on":30,"off":70}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	svc.notify.wait()
	if len(hook.bodies) != 1 {
		t.Fatalf("got %d deliveries", len(hook.bodies))
	}
	var ev ChangeEvent
	_ = json.Unmarshal(hook.bodies[0], &ev)
	if ev.Actor.Email != "ada@acme.com" || ev.Flag != "banner-test" || ev.Reason != "" {
		t.Errorf("unexpected event: %+v", ev)
	}
}

func TestFlagStatusAcrossEnvironments(t *testing.T) {
	repo := newRepo()
	repo.files["dev/payments.goff.yaml"] = strings.Replace(paymentsFile, "  version:", "  disable: true\n  version:", 1)
	repo.shas["dev/payments.goff.yaml"] = "sha-dev"
	srv, _, _ := testServerWith(t, repo, automationRules(), withTokens)

	rec := tokenRequest(t, srv, responderToken, http.MethodGet, "/api/flags/new-checkout/status", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out struct {
		Environments []FlagStatus `json:"environments"`
	}
	decode(t, rec, &out)
	if len(out.Environments) != 1 || out.Environments[0].Environment != "production" {
		t.Fatalf("responders only see production, got %+v", out.Environments)
	}
	if got := out.Environments[0]; !got.Present || !got.Enabled || !got.CanToggle || !got.Protected || got.Team != "payments" {
		t.Errorf("unexpected status: %+v", got)
	}

	rec = tokenRequest(t, srv, readerToken, http.MethodGet, "/api/flags/new-checkout/status", "")
	decode(t, rec, &out)
	if len(out.Environments) != 2 {
		t.Fatalf("reader sees both environments, got %+v", out.Environments)
	}
	for _, e := range out.Environments {
		if e.CanToggle {
			t.Errorf("reader cannot toggle anything: %+v", e)
		}
		if e.Environment == "dev" && e.Enabled {
			t.Errorf("dev copy is disabled: %+v", e)
		}
	}
}

func TestSearchFindsByKeyTeamAndDescription(t *testing.T) {
	repo := newRepo()
	repo.files["production/growth.goff.yaml"] = growthFile + `promo-banner:
  variations:
    on: true
    off: false
  defaultRule:
    variation: "off"
  metadata:
    description: Shows the checkout promo
`
	srv, sealer, _ := testServerWith(t, repo, adminRules(), nil)

	search := func(q string) []SearchHit {
		t.Helper()
		rec := request(t, srv, sealer, admin(), http.MethodGet, "/api/flags?q="+q, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d: %s", rec.Code, rec.Body)
		}
		var out SearchResult
		decode(t, rec, &out)
		return out.Hits
	}

	if hits := search("CHECKOUT"); len(hits) != 2 || hits[0].Key != "new-checkout" || hits[1].Key != "promo-banner" {
		t.Errorf("checkout search: %+v", hits)
	}
	if hits := search("growth"); len(hits) != 2 {
		t.Errorf("team search should find both growth flags: %+v", hits)
	}
	if hits := search(""); len(hits) != 3 {
		t.Errorf("empty search lists everything: %+v", hits)
	}

	rec := request(t, srv, sealer, admin(), http.MethodGet, "/api/flags?limit=1", "")
	var out SearchResult
	decode(t, rec, &out)
	if len(out.Hits) != 1 || !out.Truncated {
		t.Errorf("limit not applied: %+v", out)
	}
}

func TestSearchIsLimitedToWhatTheCallerCanSee(t *testing.T) {
	srv, sealer, _ := testServerWith(t, newRepo(), adminRules(), nil)
	rec := request(t, srv, sealer, marketer(), http.MethodGet, "/api/flags?q=checkout", "")
	var out SearchResult
	decode(t, rec, &out)
	if len(out.Hits) != 0 {
		t.Errorf("marketer can only see growth: %+v", out.Hits)
	}
}
