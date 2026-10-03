package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

type APIToken struct {
	Name   string
	SHA256 string
	Groups []string
	Email  string
}

type Tokens struct {
	tokens []parsedToken
}

type parsedToken struct {
	APIToken
	hash [sha256.Size]byte
}

var ErrBadToken = errors.New("that API token is not recognised")

func NewTokens(configured []APIToken) (*Tokens, error) {
	out := &Tokens{}
	for _, t := range configured {
		raw, err := hex.DecodeString(t.SHA256)
		if err != nil || len(raw) != sha256.Size {
			return nil, fmt.Errorf("api token %q: sha256 must be 64 hex characters", t.Name)
		}
		p := parsedToken{APIToken: t}
		copy(p.hash[:], raw)
		out.tokens = append(out.tokens, p)
	}
	return out, nil
}

func (t *Tokens) Enabled() bool {
	return t != nil && len(t.tokens) > 0
}

// HasBearer reports whether the request carries an Authorization header at all, so a bad token never falls back to a cookie.
func HasBearer(r *http.Request) bool {
	return r.Header.Get("Authorization") != ""
}

// Authenticate compares against every configured hash so the time taken says nothing about which one nearly matched.
func (t *Tokens) Authenticate(r *http.Request) (Session, error) {
	scheme, presented, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || strings.TrimSpace(presented) == "" {
		return Session{}, ErrBadToken
	}
	if !t.Enabled() {
		return Session{}, ErrBadToken
	}

	sum := sha256.Sum256([]byte(strings.TrimSpace(presented)))
	match := -1
	for i := range t.tokens {
		if subtle.ConstantTimeCompare(sum[:], t.tokens[i].hash[:]) == 1 {
			match = i
		}
	}
	if match < 0 {
		return Session{}, ErrBadToken
	}

	tok := t.tokens[match]
	email := tok.Email
	if email == "" {
		email = tok.Name + "@api-token.invalid"
	}
	return Session{
		Subject: "token:" + tok.Name,
		Name:    tok.Name + " (API token)",
		Email:   email,
		Groups:  append([]string(nil), tok.Groups...),
		Token:   tok.Name,
	}, nil
}

func (s Session) IsToken() bool {
	return s.Token != ""
}
