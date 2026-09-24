package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	CookieName   = "goff_studio_session"
	stateName    = "goff_studio_state"
	verifierName = "goff_studio_verifier"
)

type Session struct {
	Subject string    `json:"sub"`
	Name    string    `json:"name"`
	Email   string    `json:"email"`
	Groups  []string  `json:"groups"`
	Expiry  time.Time `json:"expiry"`
}

func (s Session) Expired() bool {
	return !s.Expiry.IsZero() && time.Now().After(s.Expiry)
}

func (s Session) DisplayName() string {
	if s.Name != "" {
		return s.Name
	}
	if s.Email != "" {
		return s.Email
	}
	return s.Subject
}

type Sealer struct {
	aead   cipher.AEAD
	secure bool
}

var ErrNoSession = errors.New("no session")

func NewSealer(secret string, secure bool) (*Sealer, error) {
	if len(secret) < 16 {
		return nil, fmt.Errorf("session secret must be at least 16 characters")
	}
	key := sha256.Sum256([]byte(secret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Sealer{aead: aead, secure: secure}, nil
}

func (s *Sealer) seal(payload []byte) (string, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := s.aead.Seal(nonce, nonce, payload, nil)
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (s *Sealer) open(encoded string) ([]byte, error) {
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}
	if len(raw) < s.aead.NonceSize() {
		return nil, errors.New("ciphertext too short")
	}
	nonce, body := raw[:s.aead.NonceSize()], raw[s.aead.NonceSize():]
	return s.aead.Open(nil, nonce, body, nil)
}

func (s *Sealer) Write(w http.ResponseWriter, sess Session) error {
	payload, err := json.Marshal(sess)
	if err != nil {
		return err
	}
	value, err := s.seal(payload)
	if err != nil {
		return err
	}

	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(12 * time.Hour),
	})
	return nil
}

func (s *Sealer) Read(r *http.Request) (Session, error) {
	cookie, err := r.Cookie(CookieName)
	if err != nil {
		return Session{}, ErrNoSession
	}
	payload, err := s.open(cookie.Value)
	if err != nil {
		return Session{}, ErrNoSession
	}

	var sess Session
	if err := json.Unmarshal(payload, &sess); err != nil {
		return Session{}, ErrNoSession
	}
	if sess.Expired() {
		return Session{}, ErrNoSession
	}
	return sess, nil
}

func (s *Sealer) Clear(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

func (s *Sealer) WriteState(w http.ResponseWriter) (string, error) {
	buf := make([]byte, 24)
	if _, err := io.ReadFull(rand.Reader, buf); err != nil {
		return "", err
	}
	state := base64.RawURLEncoding.EncodeToString(buf)

	http.SetCookie(w, &http.Cookie{
		Name:     stateName,
		Value:    state,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   600,
	})
	return state, nil
}

func (s *Sealer) WriteStateValue(w http.ResponseWriter, state string) (string, error) {
	http.SetCookie(w, &http.Cookie{
		Name:     stateName,
		Value:    state,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   600,
	})
	return state, nil
}

func (s *Sealer) CheckState(r *http.Request, got string) error {
	cookie, err := r.Cookie(stateName)
	if err != nil {
		return errors.New("missing state cookie")
	}
	if cookie.Value == "" || got == "" || cookie.Value != got {
		return errors.New("state mismatch")
	}
	return nil
}

func (s *Sealer) ClearState(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     stateName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}
