package githubapp

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

type InstallationTokens struct {
	APIBase        string
	AppID          string
	InstallationID string
	key            *rsa.PrivateKey
	http           *http.Client

	mu      sync.Mutex
	token   string
	expires time.Time
}

func NewInstallationTokens(apiBase, appID, installationID, pemPath string, pemBytes []byte, client *http.Client) (*InstallationTokens, error) {
	if apiBase == "" {
		apiBase = "https://api.github.com"
	}
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}

	raw := pemBytes
	if len(raw) == 0 {
		if pemPath == "" {
			return nil, fmt.Errorf("a GitHub App private key path or contents is required")
		}
		var err error
		raw, err = os.ReadFile(pemPath)
		if err != nil {
			return nil, fmt.Errorf("reading app private key: %w", err)
		}
	}

	key, err := parsePrivateKey(raw)
	if err != nil {
		return nil, err
	}

	return &InstallationTokens{
		APIBase:        apiBase,
		AppID:          appID,
		InstallationID: installationID,
		key:            key,
		http:           client,
	}, nil
}

func parsePrivateKey(raw []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, fmt.Errorf("app private key is not valid PEM")
	}

	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}

	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("app private key is not a supported RSA key: %w", err)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("app private key must be RSA")
	}
	return key, nil
}

func (i *InstallationTokens) Token(ctx context.Context) (string, error) {
	i.mu.Lock()
	defer i.mu.Unlock()

	if i.token != "" && time.Now().Before(i.expires.Add(-2*time.Minute)) {
		return i.token, nil
	}

	assertion, err := i.appJWT()
	if err != nil {
		return "", err
	}

	endpoint := fmt.Sprintf("%s/app/installations/%s/access_tokens", i.APIBase, i.InstallationID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+assertion)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	res, err := i.http.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return "", fmt.Errorf("github installation token: status %d", res.StatusCode)
	}

	var out struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.Token == "" {
		return "", fmt.Errorf("github returned an empty installation token")
	}

	i.token = out.Token
	i.expires = out.ExpiresAt
	return i.token, nil
}

func (i *InstallationTokens) appJWT() (string, error) {
	now := time.Now()
	header := map[string]string{"alg": "RS256", "typ": "JWT"}
	claims := map[string]any{
		"iat": now.Add(-30 * time.Second).Unix(),
		"exp": now.Add(9 * time.Minute).Unix(),
		"iss": i.AppID,
	}

	encode := func(v any) (string, error) {
		raw, err := json.Marshal(v)
		if err != nil {
			return "", err
		}
		return base64.RawURLEncoding.EncodeToString(raw), nil
	}

	h, err := encode(header)
	if err != nil {
		return "", err
	}
	c, err := encode(claims)
	if err != nil {
		return "", err
	}

	signingInput := h + "." + c
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, i.key, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("signing app jwt: %w", err)
	}

	return strings.Join([]string{h, c, base64.RawURLEncoding.EncodeToString(signature)}, "."), nil
}
