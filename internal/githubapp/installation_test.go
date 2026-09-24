package githubapp

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testKeyPEM(t *testing.T, pkcs8 bool) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	if pkcs8 {
		der, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			t.Fatal(err)
		}
		return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	}
	return pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
}

func TestAcceptsBothPEMFormats(t *testing.T) {
	for _, pkcs8 := range []bool{false, true} {
		if _, err := NewInstallationTokens("", "123", "456", "", testKeyPEM(t, pkcs8), nil); err != nil {
			t.Errorf("pkcs8=%v rejected: %v", pkcs8, err)
		}
	}
}

func TestRejectsGarbageKey(t *testing.T) {
	if _, err := NewInstallationTokens("", "1", "2", "", []byte("not a key"), nil); err == nil {
		t.Error("garbage should be rejected")
	}
}

func TestExchangesAppJWTForInstallationToken(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/app/installations/456/access_tokens") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token":      "ghs_installation",
			"expires_at": time.Now().Add(time.Hour),
		})
	}))
	defer srv.Close()

	tokens, err := NewInstallationTokens(srv.URL, "123", "456", "", testKeyPEM(t, false), srv.Client())
	if err != nil {
		t.Fatal(err)
	}

	token, err := tokens.Token(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if token != "ghs_installation" {
		t.Errorf("token = %q", token)
	}

	if !strings.HasPrefix(gotAuth, "Bearer ") {
		t.Fatalf("auth header = %q", gotAuth)
	}
	jwt := strings.TrimPrefix(gotAuth, "Bearer ")
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		t.Fatalf("expected a three part jwt, got %d parts", len(parts))
	}

	claimsRaw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims map[string]any
	if err := json.Unmarshal(claimsRaw, &claims); err != nil {
		t.Fatal(err)
	}
	if claims["iss"] != "123" {
		t.Errorf("iss = %v, want the app id", claims["iss"])
	}
	exp, iat := claims["exp"].(float64), claims["iat"].(float64)
	if exp-iat > 610 {
		t.Errorf("github rejects app jwts valid longer than 10 minutes, got %.0fs", exp-iat)
	}
}

func TestCachesTokenUntilNearExpiry(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token":      "ghs_x",
			"expires_at": time.Now().Add(time.Hour),
		})
	}))
	defer srv.Close()

	tokens, _ := NewInstallationTokens(srv.URL, "1", "2", "", testKeyPEM(t, false), srv.Client())
	for i := 0; i < 5; i++ {
		if _, err := tokens.Token(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Errorf("token should be cached, minted %d times", calls)
	}
}

func TestRefetchesExpiredToken(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token":      "ghs_x",
			"expires_at": time.Now().Add(30 * time.Second),
		})
	}))
	defer srv.Close()

	tokens, _ := NewInstallationTokens(srv.URL, "1", "2", "", testKeyPEM(t, false), srv.Client())
	_, _ = tokens.Token(context.Background())
	_, _ = tokens.Token(context.Background())

	if calls != 2 {
		t.Errorf("a token expiring inside the 2 minute margin should be refetched, minted %d times", calls)
	}
}

func TestStaticTokenIsTheDevFallback(t *testing.T) {
	tok, err := StaticToken("ghp_local_dev").Token(context.Background())
	if err != nil || tok != "ghp_local_dev" {
		t.Errorf("static token = %q, %v", tok, err)
	}
}
