package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func hashOf(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func tokens(t *testing.T) *Tokens {
	t.Helper()
	tok, err := NewTokens([]APIToken{
		{Name: "incident-bot", SHA256: hashOf("first-secret"), Groups: []string{"responders"}},
		{Name: "reader", SHA256: hashOf("second-secret"), Groups: []string{"readers"}, Email: "reader@example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func TestTokenAuthenticatesAsItsGroups(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Authorization", "bearer second-secret")

	sess, err := tokens(t).Authenticate(r)
	if err != nil {
		t.Fatal(err)
	}
	if !sess.IsToken() || sess.Token != "reader" || sess.Subject != "token:reader" {
		t.Errorf("wrong identity: %+v", sess)
	}
	if sess.Email != "reader@example.com" || len(sess.Groups) != 1 || sess.Groups[0] != "readers" {
		t.Errorf("wrong email or groups: %+v", sess)
	}
}

func TestTokenWithoutEmailGetsAnUnroutableOne(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Authorization", "Bearer first-secret")
	sess, err := tokens(t).Authenticate(r)
	if err != nil {
		t.Fatal(err)
	}
	if sess.Email != "incident-bot@api-token.invalid" {
		t.Errorf("email = %q", sess.Email)
	}
}

func TestBadTokensAreRejected(t *testing.T) {
	for _, header := range []string{"", "Bearer", "Bearer ", "Basic first-secret", "Bearer wrong", "first-secret"} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		if header != "" {
			r.Header.Set("Authorization", header)
		}
		if _, err := tokens(t).Authenticate(r); !errors.Is(err, ErrBadToken) {
			t.Errorf("%q: got %v, want ErrBadToken", header, err)
		}
	}
}

func TestNoTokensConfiguredRejectsEverything(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Authorization", "Bearer anything")
	var none *Tokens
	if _, err := none.Authenticate(r); !errors.Is(err, ErrBadToken) {
		t.Errorf("got %v", err)
	}
}

func TestTokenSessionIsNeverSealedAsAToken(t *testing.T) {
	sealer, err := NewSealer("0123456789abcdef0123", false)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	if err := sealer.Write(rec, Session{Subject: "x", Token: "incident-bot"}); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(rec.Result().Cookies()[0])
	sess, err := sealer.Read(r)
	if err != nil {
		t.Fatal(err)
	}
	if sess.IsToken() {
		t.Error("a cookie must never carry token identity")
	}
}

func TestMalformedHashRejected(t *testing.T) {
	if _, err := NewTokens([]APIToken{{Name: "x", SHA256: "zz"}}); err == nil {
		t.Error("expected an error")
	}
}
