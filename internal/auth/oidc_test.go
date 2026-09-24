package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

type fakeIDP struct {
	server   *httptest.Server
	key      *rsa.PrivateKey
	claims   map[string]any
	lastPKCE url.Values
}

func newFakeIDP(t *testing.T, claims map[string]any) *fakeIDP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	idp := &fakeIDP{key: key, claims: claims}
	mux := http.NewServeMux()

	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		base := idp.server.URL
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                base,
			"authorization_endpoint":                base + "/authorize",
			"token_endpoint":                        base + "/token",
			"jwks_uri":                              base + "/jwks",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})

	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		pub := key.Public().(*rsa.PublicKey)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]any{{
				"kty": "RSA",
				"alg": "RS256",
				"use": "sig",
				"kid": "test",
				"n":   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
				"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
			}},
		})
	})

	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		idp.lastPKCE = r.Form

		signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key},
			(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "test"))
		if err != nil {
			t.Error(err)
			return
		}

		all := map[string]any{
			"iss": idp.server.URL,
			"aud": "studio-client",
			"exp": time.Now().Add(time.Hour).Unix(),
			"iat": time.Now().Unix(),
		}
		for k, v := range idp.claims {
			all[k] = v
		}

		raw, err := jwt.Signed(signer).Claims(all).Serialize()
		if err != nil {
			t.Error(err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "at",
			"token_type":   "Bearer",
			"id_token":     raw,
		})
	})

	idp.server = httptest.NewServer(mux)
	t.Cleanup(idp.server.Close)
	return idp
}

func newOIDCForTest(t *testing.T, idp *fakeIDP, groupsClaim string) *OIDC {
	t.Helper()
	o, err := NewOIDC(context.Background(), OIDCConfig{
		IssuerURL:    idp.server.URL,
		ClientID:     "studio-client",
		ClientSecret: "secret",
		RedirectURL:  "http://localhost:8080/auth/callback",
		GroupsClaim:  groupsClaim,
	})
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func TestStartProducesPKCEChallenge(t *testing.T) {
	idp := newFakeIDP(t, nil)
	o := newOIDCForTest(t, idp, "")

	ch, err := o.Start()
	if err != nil {
		t.Fatal(err)
	}

	if len(ch.State) < 20 || len(ch.Verifier) < 40 {
		t.Errorf("state/verifier too short: %d/%d", len(ch.State), len(ch.Verifier))
	}

	u, err := url.Parse(ch.URL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" {
		t.Errorf("challenge method = %q", q.Get("code_challenge_method"))
	}

	digest := sha256.Sum256([]byte(ch.Verifier))
	want := base64.RawURLEncoding.EncodeToString(digest[:])
	if q.Get("code_challenge") != want {
		t.Error("code_challenge is not the S256 hash of the verifier")
	}
	if q.Get("state") != ch.State {
		t.Error("state not carried into the authorize url")
	}
	if !strings.Contains(q.Get("scope"), "openid") {
		t.Errorf("scope = %q", q.Get("scope"))
	}
}

func TestTwoChallengesDiffer(t *testing.T) {
	idp := newFakeIDP(t, nil)
	o := newOIDCForTest(t, idp, "")

	a, _ := o.Start()
	b, _ := o.Start()
	if a.State == b.State || a.Verifier == b.Verifier {
		t.Error("state and verifier must be per-request")
	}
}

func TestExchangeBuildsSessionFromClaims(t *testing.T) {
	idp := newFakeIDP(t, map[string]any{
		"sub":    "00u1abc",
		"name":   "Jane Doe",
		"email":  "jane@company.com",
		"groups": []string{"flags-admins", "payments-team"},
	})
	o := newOIDCForTest(t, idp, "")

	sess, err := o.Exchange(context.Background(), "the-code", "the-verifier")
	if err != nil {
		t.Fatal(err)
	}

	if sess.Subject != "00u1abc" || sess.Name != "Jane Doe" || sess.Email != "jane@company.com" {
		t.Errorf("session = %+v", sess)
	}
	if len(sess.Groups) != 2 || sess.Groups[0] != "flags-admins" {
		t.Errorf("groups = %v", sess.Groups)
	}
	if sess.Expiry.IsZero() {
		t.Error("expiry not set")
	}

	if idp.lastPKCE.Get("code_verifier") != "the-verifier" {
		t.Errorf("code_verifier not sent: %v", idp.lastPKCE)
	}
}

func TestExchangeReadsCustomGroupsClaim(t *testing.T) {
	idp := newFakeIDP(t, map[string]any{
		"sub":          "x",
		"email":        "a@b.com",
		"studio_roles": []string{"marketing"},
		"groups":       []string{"should-be-ignored"},
	})
	o := newOIDCForTest(t, idp, "studio_roles")

	sess, err := o.Exchange(context.Background(), "c", "v")
	if err != nil {
		t.Fatal(err)
	}
	if len(sess.Groups) != 1 || sess.Groups[0] != "marketing" {
		t.Errorf("groups = %v, should come from the configured claim", sess.Groups)
	}
}

func TestExchangeHandlesSingleStringGroupClaim(t *testing.T) {
	idp := newFakeIDP(t, map[string]any{"sub": "x", "email": "a@b.com", "groups": "solo-group"})
	o := newOIDCForTest(t, idp, "")

	sess, err := o.Exchange(context.Background(), "c", "v")
	if err != nil {
		t.Fatal(err)
	}
	if len(sess.Groups) != 1 || sess.Groups[0] != "solo-group" {
		t.Errorf("a string groups claim should still work, got %v", sess.Groups)
	}
}

func TestExchangeRequiresEmailForCommitAttribution(t *testing.T) {
	idp := newFakeIDP(t, map[string]any{"sub": "x", "groups": []string{"g"}})
	o := newOIDCForTest(t, idp, "")

	if _, err := o.Exchange(context.Background(), "c", "v"); err == nil {
		t.Error("a token without email should be rejected, since commits need it")
	}
}

func TestExchangeRejectsWrongAudience(t *testing.T) {
	idp := newFakeIDP(t, map[string]any{"sub": "x", "email": "a@b.com", "aud": "someone-else"})
	o := newOIDCForTest(t, idp, "")

	if _, err := o.Exchange(context.Background(), "c", "v"); err == nil {
		t.Error("an id token for a different client must be rejected")
	}
}

func TestNewOIDCValidatesConfig(t *testing.T) {
	if _, err := NewOIDC(context.Background(), OIDCConfig{}); err == nil {
		t.Error("missing issuer and client id should fail")
	}
}

func TestGroupsClaimDefaultsToGroups(t *testing.T) {
	idp := newFakeIDP(t, nil)
	o := newOIDCForTest(t, idp, "")
	if o.GroupsClaim() != "groups" {
		t.Errorf("default groups claim = %q", o.GroupsClaim())
	}
}

func TestVerifierCookieRoundTrip(t *testing.T) {
	s, err := NewSealer("a-sufficiently-long-secret", false)
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	s.WriteVerifier(rec, "the-verifier")

	cookie := rec.Result().Cookies()[0]
	if !cookie.HttpOnly {
		t.Error("verifier cookie must be HttpOnly")
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(cookie)

	got, err := s.Verifier(req)
	if err != nil || got != "the-verifier" {
		t.Errorf("verifier = %q, %v", got, err)
	}

	bare := httptest.NewRequest(http.MethodGet, "/", nil)
	if _, err := s.Verifier(bare); err == nil {
		t.Error("a missing verifier must be an error, not an empty string")
	}
}
