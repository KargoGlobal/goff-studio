package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

type OIDCConfig struct {
	IssuerURL    string
	ClientID     string
	ClientSecret string
	RedirectURL  string
	Scopes       []string
	GroupsClaim  string
}

type OIDC struct {
	cfg      OIDCConfig
	provider *oidc.Provider
	verifier *oidc.IDTokenVerifier
	oauth    oauth2.Config
}

func NewOIDC(ctx context.Context, cfg OIDCConfig) (*OIDC, error) {
	if cfg.IssuerURL == "" || cfg.ClientID == "" {
		return nil, fmt.Errorf("oidc issuer url and client id are required")
	}
	if cfg.GroupsClaim == "" {
		cfg.GroupsClaim = "groups"
	}
	if len(cfg.Scopes) == 0 {
		cfg.Scopes = []string{oidc.ScopeOpenID, "profile", "email", "groups"}
	}

	provider, err := oidc.NewProvider(ctx, cfg.IssuerURL)
	if err != nil {
		return nil, fmt.Errorf("discovering oidc provider: %w", err)
	}

	return &OIDC{
		cfg:      cfg,
		provider: provider,
		verifier: provider.Verifier(&oidc.Config{ClientID: cfg.ClientID}),
		oauth: oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			RedirectURL:  cfg.RedirectURL,
			Endpoint:     provider.Endpoint(),
			Scopes:       cfg.Scopes,
		},
	}, nil
}

func (o *OIDC) GroupsClaim() string { return o.cfg.GroupsClaim }

type Challenge struct {
	State    string
	Verifier string
	URL      string
}

func (o *OIDC) Start() (Challenge, error) {
	state, err := randomString(24)
	if err != nil {
		return Challenge{}, err
	}
	verifier, err := randomString(48)
	if err != nil {
		return Challenge{}, err
	}

	digest := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(digest[:])

	url := o.oauth.AuthCodeURL(state,
		oauth2.SetAuthURLParam("code_challenge", challenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)

	return Challenge{State: state, Verifier: verifier, URL: url}, nil
}

func (o *OIDC) Exchange(ctx context.Context, code, verifier string) (Session, error) {
	token, err := o.oauth.Exchange(ctx, code,
		oauth2.SetAuthURLParam("code_verifier", verifier),
	)
	if err != nil {
		return Session{}, fmt.Errorf("exchanging code: %w", err)
	}

	rawID, ok := token.Extra("id_token").(string)
	if !ok || rawID == "" {
		return Session{}, fmt.Errorf("no id_token in the provider response")
	}

	idToken, err := o.verifier.Verify(ctx, rawID)
	if err != nil {
		return Session{}, fmt.Errorf("verifying id token: %w", err)
	}

	var claims map[string]any
	if err := idToken.Claims(&claims); err != nil {
		return Session{}, fmt.Errorf("reading claims: %w", err)
	}

	sess := Session{
		Subject: idToken.Subject,
		Name:    stringClaim(claims, "name"),
		Email:   stringClaim(claims, "email"),
		Groups:  stringsClaim(claims, o.cfg.GroupsClaim),
		Expiry:  idToken.Expiry,
	}

	if sess.Expiry.IsZero() || time.Until(sess.Expiry) > 12*time.Hour {
		sess.Expiry = time.Now().Add(12 * time.Hour)
	}
	if sess.Email == "" {
		return Session{}, fmt.Errorf("the id token has no email claim, which Studio needs to attribute commits")
	}

	return sess, nil
}

func stringClaim(claims map[string]any, key string) string {
	if v, ok := claims[key].(string); ok {
		return v
	}
	return ""
}

func stringsClaim(claims map[string]any, key string) []string {
	switch v := claims[key].(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case string:
		if v == "" {
			return nil
		}
		return []string{v}
	}
	return nil
}

func randomString(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := io.ReadFull(rand.Reader, buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func (s *Sealer) WriteVerifier(w http.ResponseWriter, verifier string) {
	http.SetCookie(w, &http.Cookie{
		Name:     verifierName,
		Value:    verifier,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   600,
	})
}

func (s *Sealer) Verifier(r *http.Request) (string, error) {
	cookie, err := r.Cookie(verifierName)
	if err != nil || cookie.Value == "" {
		return "", fmt.Errorf("missing pkce verifier")
	}
	return cookie.Value, nil
}

func (s *Sealer) ClearVerifier(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     verifierName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}
