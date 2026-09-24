package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSealerRoundTrip(t *testing.T) {
	s, err := NewSealer("a-sufficiently-long-secret", false)
	if err != nil {
		t.Fatal(err)
	}

	want := Session{Subject: "okta|00u1abc", Name: "Jaime", Email: "j@example.com", Groups: []string{"flags-admins"}}

	rec := httptest.NewRecorder()
	if err := s.Write(rec, want); err != nil {
		t.Fatal(err)
	}

	cookie := rec.Result().Cookies()[0]
	if cookie.Value == "" {
		t.Fatal("empty cookie")
	}
	if strings.Contains(cookie.Value, "flags-admins") {
		t.Error("group membership appears in plaintext in the cookie")
	}
	if !cookie.HttpOnly {
		t.Error("session cookie must be HttpOnly")
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Error("session cookie should be SameSite=Lax for the oauth redirect")
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(cookie)

	got, err := s.Read(req)
	if err != nil {
		t.Fatal(err)
	}
	if got.Subject != want.Subject || got.Email != want.Email {
		t.Errorf("round trip lost data: %+v", got)
	}
	if len(got.Groups) != 1 || got.Groups[0] != "flags-admins" {
		t.Errorf("groups lost: %+v", got.Groups)
	}
}

func TestSealerRejectsTamperedCookie(t *testing.T) {
	s, _ := NewSealer("a-sufficiently-long-secret", false)

	rec := httptest.NewRecorder()
	_ = s.Write(rec, Session{Subject: "okta|1", Email: "j@x.com"})
	cookie := rec.Result().Cookies()[0]

	cookie.Value = cookie.Value[:len(cookie.Value)-4] + "AAAA"

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(cookie)

	if _, err := s.Read(req); err == nil {
		t.Error("tampered cookie was accepted")
	}
}

func TestSealerRejectsOtherKey(t *testing.T) {
	a, _ := NewSealer("secret-number-one-long", false)
	b, _ := NewSealer("secret-number-two-long", false)

	rec := httptest.NewRecorder()
	_ = a.Write(rec, Session{Subject: "okta|1", Email: "j@x.com"})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(rec.Result().Cookies()[0])

	if _, err := b.Read(req); err == nil {
		t.Error("cookie sealed with a different key was accepted")
	}
}

func TestSealerRequiresLongSecret(t *testing.T) {
	if _, err := NewSealer("short", false); err == nil {
		t.Error("short secrets should be rejected")
	}
}

func TestExpiredSessionIsRejected(t *testing.T) {
	s, _ := NewSealer("a-sufficiently-long-secret", false)

	rec := httptest.NewRecorder()
	_ = s.Write(rec, Session{Subject: "okta|1", Expiry: time.Now().Add(-time.Minute)})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(rec.Result().Cookies()[0])

	if _, err := s.Read(req); err == nil {
		t.Error("expired session should be rejected so the user re-authenticates")
	}
}

func TestNoCookieIsNoSession(t *testing.T) {
	s, _ := NewSealer("a-sufficiently-long-secret", false)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if _, err := s.Read(req); err != ErrNoSession {
		t.Errorf("want ErrNoSession, got %v", err)
	}
}

func TestStateCookieBlocksCSRF(t *testing.T) {
	s, _ := NewSealer("a-sufficiently-long-secret", false)

	rec := httptest.NewRecorder()
	state, err := s.WriteState(rec)
	if err != nil {
		t.Fatal(err)
	}
	if len(state) < 20 {
		t.Errorf("state too short to be unguessable: %q", state)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(rec.Result().Cookies()[0])

	if err := s.CheckState(req, state); err != nil {
		t.Errorf("matching state rejected: %v", err)
	}
	if err := s.CheckState(req, "attacker-supplied"); err == nil {
		t.Error("mismatched state accepted")
	}
	if err := s.CheckState(req, ""); err == nil {
		t.Error("empty state accepted")
	}

	bare := httptest.NewRequest(http.MethodGet, "/", nil)
	if err := s.CheckState(bare, state); err == nil {
		t.Error("missing state cookie accepted")
	}
}

func TestSecureFlagFollowsConfig(t *testing.T) {
	secure, _ := NewSealer("a-sufficiently-long-secret", true)
	rec := httptest.NewRecorder()
	_ = secure.Write(rec, Session{Subject: "okta|1"})
	if !rec.Result().Cookies()[0].Secure {
		t.Error("secure mode should set the Secure flag")
	}
}

func TestClearRemovesCookie(t *testing.T) {
	s, _ := NewSealer("a-sufficiently-long-secret", false)
	rec := httptest.NewRecorder()
	s.Clear(rec)
	c := rec.Result().Cookies()[0]
	if c.MaxAge >= 0 || c.Value != "" {
		t.Errorf("clear should expire the cookie, got %+v", c)
	}
}
