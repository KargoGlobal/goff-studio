package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/go-feature-flag/studio/internal/config"
	"github.com/go-feature-flag/studio/internal/permissions"
	"github.com/go-feature-flag/studio/internal/storage"
)

// noopBackend answers like the object-store backends do when the bytes did not change: the current version, not "".
type noopBackend struct {
	storage.Backend
}

func (noopBackend) Write(_ context.Context, op storage.ChangeOp, _ storage.Identity) (*storage.Result, error) {
	current := []byte(paymentsFile)
	if _, err := op.Apply(current); err != nil {
		return nil, err
	}
	return &storage.Result{Version: "v1"}, nil
}

func TestANoOpWriteIsNotNotified(t *testing.T) {
	hook := &hookRecorder{}
	hookSrv := hook.server(t)
	cfg := &config.Config{Notifications: []config.Notification{{URL: hookSrv.URL, Format: config.NotifyJSON}}}
	n := newNotifier(cfg)
	b := notifyingBackend{Backend: noopBackend{}, notify: n}

	_, err := b.Write(context.Background(), storage.ChangeOp{
		Path: "production/payments.goff.yaml", Key: "new-checkout", Message: "[production] payments/new-checkout: disabled",
		Apply: func(current []byte) ([]byte, error) { return current, nil },
	}, storage.Identity{Name: "x"})
	if err != nil {
		t.Fatal(err)
	}
	n.wait()
	if hook.attempts != 0 {
		t.Errorf("a write that changed nothing sent %d notifications", hook.attempts)
	}
}

func TestBearerWithoutConfiguredTokensFallsBackToTheCookie(t *testing.T) {
	srv, sealer := testServer(t, newRepo(), adminRules())

	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req.Header.Set("Authorization", "Bearer some-proxy-id-token")
	cookie := httptest.NewRecorder()
	if err := sealer.Write(cookie, *admin()); err != nil {
		t.Fatal(err)
	}
	req.AddCookie(cookie.Result().Cookies()[0])
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("with no API tokens configured, a proxy's Bearer header must not lock users out, got %d", rec.Code)
	}
}

func TestWellKnownAndDisabledMCPAreNotTheSPA(t *testing.T) {
	srv, _, _ := testServerWith(t, newRepo(), automationRules(), withTokens)
	srv.assets = fstest.MapFS{"index.html": {Data: []byte("<!doctype html><title>Studio</title>")}}

	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/mcp"},
		{http.MethodGet, "/.well-known/oauth-protected-resource/mcp"},
		{http.MethodGet, "/.well-known/oauth-authorization-server"},
	} {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, strings.NewReader("{}")))
		if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "<!doctype") {
			t.Errorf("%s %s: %d %q, want a 404 that is not the app page", tc.method, tc.path, rec.Code, rec.Body)
		}
	}
}

func TestUnknownEnvironmentDoesNotLeakStorageDetail(t *testing.T) {
	srv, sealer, _ := testServerWith(t, newRepo(), adminRules(), withMCP)

	rec := request(t, srv, sealer, admin(), http.MethodGet, "/api/flags?environment=nope", "")
	if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "/repos/") {
		t.Errorf("status %d body %s", rec.Code, rec.Body)
	}
}

func TestUnknownEnvironmentOverMCPDoesNotLeakStorageDetail(t *testing.T) {
	repo := newRepo()
	srv, _, _ := testServerWith(t, repo, []permissions.Rule{{Group: "readers", Allow: []string{"*"}, Actions: []string{"view"}}}, withMCP)
	res := callTool(t, srv, readerToken, "search_flags", map[string]any{"environment": "nope"})
	if !res.IsError || strings.Contains(res.Content[0].Text, "/repos/") {
		t.Errorf("got %+v", res)
	}
}

func TestReasonIsValidatedEvenWhenNothingChanges(t *testing.T) {
	repo := newRepo()
	srv, _, _ := testServerWith(t, repo, automationRules(), withTokens)
	rec := tokenRequest(t, srv, responderToken, http.MethodPost, "/api/environments/production/flags/new-checkout/state",
		`{"enabled":true,"reason":"a\nb"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status %d, want 400 whatever the flag's state", rec.Code)
	}
}

func TestSearchingAHiddenEnvironmentSaysSoForAReader(t *testing.T) {
	srv, sealer, _ := testServerWith(t, newRepo(), adminRules(), nil)
	rec := request(t, srv, sealer, marketer(), http.MethodGet, "/api/flags?environment=dev", "")
	if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "change") {
		t.Errorf("status %d body %s", rec.Code, rec.Body)
	}
}

func TestMCPResponsesAndOddBatchItems(t *testing.T) {
	srv, _, _ := testServerWith(t, newRepo(), automationRules(), withMCP)
	if rec := mcpPost(t, srv, readerToken, `{"jsonrpc":"2.0","id":3,"result":{}}`); rec.Code != http.StatusAccepted {
		t.Errorf("a client response gets 202, got %d %s", rec.Code, rec.Body)
	}
	rec := mcpPost(t, srv, readerToken, `[1]`)
	var batch []rpcResponse
	decode(t, rec, &batch)
	if len(batch) != 1 || batch[0].Error == nil || batch[0].Error.Code != rpcInvalidRequest {
		t.Errorf("a non-object batch item is an invalid request, got %s", rec.Body)
	}
}

func TestMCPCallsAreRateLimitedPerToken(t *testing.T) {
	srv, _, _ := testServerWith(t, newRepo(), automationRules(), withMCP)
	ping := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_environments"}}`
	limited := 0
	for range mcpCallsPerMinute + 5 {
		if rec := mcpPost(t, srv, readerToken, ping); rec.Code == http.StatusTooManyRequests {
			limited++
			if rec.Header().Get("Retry-After") == "" {
				t.Error("429 without Retry-After")
			}
		}
	}
	if limited != 5 {
		t.Errorf("want 5 limited calls, got %d", limited)
	}
	if rec := mcpPost(t, srv, responderToken, ping); rec.Code != http.StatusOK {
		t.Errorf("another token has its own budget, got %d", rec.Code)
	}
}
