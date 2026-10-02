package server

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-feature-flag/studio/internal/auth"
)

// An IdP whose token endpoint always answers like an expired or reused code.
func rejectingIDP(t *testing.T) *auth.OIDC {
	t.Helper()
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": srv.URL, "authorization_endpoint": srv.URL + "/authorize",
			"token_endpoint": srv.URL + "/token", "jwks_uri": srv.URL + "/keys",
		})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"The authorization code is invalid or has expired."}`))
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	o, err := auth.NewOIDC(context.Background(), auth.OIDCConfig{
		IssuerURL: srv.URL, ClientID: "studio", ClientSecret: "s", RedirectURL: "http://studio/auth/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func callback(t *testing.T, srv *Server, sealer *auth.Sealer, sess *auth.Session, query string) *httptest.ResponseRecorder {
	t.Helper()
	start := httptest.NewRecorder()
	if _, err := sealer.WriteStateValue(start, "st"); err != nil {
		t.Fatal(err)
	}
	sealer.WriteVerifier(start, "verifier")

	req := httptest.NewRequest(http.MethodGet, "/auth/callback?"+query, nil)
	for _, c := range start.Result().Cookies() {
		req.AddCookie(c)
	}
	if sess != nil {
		w := httptest.NewRecorder()
		if err := sealer.Write(w, *sess); err != nil {
			t.Fatal(err)
		}
		for _, c := range w.Result().Cookies() {
			req.AddCookie(c)
		}
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })
	return &buf
}

func TestExpiredCodeShowsASignInAgainPageAndLogsTheReason(t *testing.T) {
	srv, sealer := testServer(t, newRepo(), adminRules())
	srv.oidc = rejectingIDP(t)
	logs := captureLog(t)

	rec := callback(t, srv, sealer, nil, "code=used&state=st")

	if rec.Code != http.StatusBadGateway {
		t.Errorf("status %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(rec.Header().Get("Content-Type"), "text/html") || !strings.Contains(body, `href="/auth/login"`) {
		t.Errorf("want an HTML page linking back to sign-in, got %q: %s", rec.Header().Get("Content-Type"), body)
	}
	if strings.Contains(body, "invalid_grant") {
		t.Error("the IdP's error must stay out of the browser")
	}
	if !strings.Contains(logs.String(), "invalid_grant") {
		t.Errorf("the real reason must be logged, got %q", logs.String())
	}
}

func TestReplayedCallbackWithASessionGoesHome(t *testing.T) {
	srv, sealer := testServer(t, newRepo(), adminRules())
	srv.oidc = rejectingIDP(t)

	rec := callback(t, srv, sealer, admin(), "code=used&state=st")

	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/" {
		t.Errorf("a signed-in visitor reloading the callback should land home, got %d to %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestStateMismatchShowsTheSignInAgainPage(t *testing.T) {
	srv, sealer := testServer(t, newRepo(), adminRules())
	captureLog(t)

	rec := callback(t, srv, sealer, nil, "code=x&state=forged")

	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `href="/auth/login"`) {
		t.Errorf("status %d: %s", rec.Code, rec.Body)
	}
}

func TestIdPErrorOnCallbackIsLoggedNotExchanged(t *testing.T) {
	srv, sealer := testServer(t, newRepo(), adminRules())
	logs := captureLog(t)

	rec := callback(t, srv, sealer, nil, "error=access_denied&error_description=not+assigned&state=st")

	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `href="/auth/login"`) {
		t.Errorf("status %d: %s", rec.Code, rec.Body)
	}
	if !strings.Contains(logs.String(), "access_denied: not assigned") {
		t.Errorf("logs = %q", logs.String())
	}
}
