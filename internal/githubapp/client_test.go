package githubapp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

const initial = `alpha:
  variations:
    on: true
    off: false
  defaultRule:
    variation: "off"
beta:
  variations:
    on: true
    off: false
  defaultRule:
    variation: "on"
`

type fakeRepo struct {
	mu       sync.Mutex
	content  string
	sha      string
	puts     []map[string]any
	getCount int
	mutate   func(getCount int) (string, string)
}

func (f *fakeRepo) server(t *testing.T) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/acme/flags/contents/", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()

		switch r.Method {
		case http.MethodGet:
			f.getCount++
			if f.mutate != nil {
				if c, s := f.mutate(f.getCount); c != "" {
					f.content, f.sha = c, s
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]string{
				"content": base64.StdEncoding.EncodeToString([]byte(f.content)),
				"sha":     f.sha,
			})
		case http.MethodPut:
			var payload map[string]any
			_ = json.NewDecoder(r.Body).Decode(&payload)
			if payload["sha"] != f.sha {
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(`{"message":"sha mismatch"}`))
				return
			}
			f.puts = append(f.puts, payload)
			decoded, _ := base64.StdEncoding.DecodeString(payload["content"].(string))
			f.content = string(decoded)
			f.sha = "sha-after-" + itoa(len(f.puts))
			_ = json.NewEncoder(w).Encode(map[string]any{"commit": map[string]string{"sha": "commit1"}})
		}
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func itoa(n int) string { return string(rune('0' + n)) }

func newClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	return New(Config{APIBase: srv.URL, Owner: "acme", Repo: "flags", Branch: "main"},
		StaticToken("tok"), srv.Client())
}

func who() Identity {
	return Identity{Name: "Jane Doe", Email: "jane@company.com", Subject: "okta|00u1abc"}
}

func disableAlpha(current []byte) ([]byte, error) {
	return []byte(strings.Replace(string(current), `alpha:
  variations:`, `alpha:
  disable: true
  variations:`, 1)), nil
}

func TestCommitIncludesIdentityTrailers(t *testing.T) {
	repo := &fakeRepo{content: initial, sha: "sha1"}
	srv := repo.server(t)
	c := newClient(t, srv)

	res, err := c.Commit(context.Background(), ChangeOp{
		Path:    "production/payments.goff.yaml",
		FlagKey: "alpha",
		BaseSHA: "sha1",
		Message: "[production] payments/alpha: disabled",
		Apply:   disableAlpha,
	}, who())
	if err != nil {
		t.Fatal(err)
	}
	if res.CommitSHA != "commit1" {
		t.Errorf("commit sha = %q", res.CommitSHA)
	}

	msg := repo.puts[0]["message"].(string)
	for _, want := range []string{
		"[production] payments/alpha: disabled",
		"GOFF-Studio-User: jane@company.com",
		"GOFF-Studio-User-Id: okta|00u1abc",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("commit message missing %q:\n%s", want, msg)
		}
	}

	author, ok := repo.puts[0]["author"].(map[string]any)
	if !ok || author["email"] != "jane@company.com" || author["name"] != "Jane Doe" {
		t.Errorf("author should be the Okta user, got %v", repo.puts[0]["author"])
	}
}

func TestCommitRetriesWhenADifferentFlagChanged(t *testing.T) {
	changed := `alpha:
  variations:
    on: true
    off: false
  defaultRule:
    variation: "off"
beta:
  variations:
    on: true
    off: false
  defaultRule:
    variation: "off"
`
	repo := &fakeRepo{content: initial, sha: "sha1"}
	repo.mutate = func(get int) (string, string) {
		if get == 1 {
			return changed, "sha2"
		}
		return "", ""
	}
	srv := repo.server(t)
	c := newClient(t, srv)

	res, err := c.Commit(context.Background(), ChangeOp{
		Path:    "f.yaml",
		FlagKey: "alpha",
		BaseSHA: "sha1",
		Apply:   disableAlpha,
		FlagChanged: func(current []byte) (bool, error) {
			return flagBlock([]byte(initial), "alpha") != flagBlock(current, "alpha"), nil
		},
	}, who())
	if err != nil {
		t.Fatalf("a change to a different flag should retry silently, got: %v", err)
	}
	if !res.Retried {
		t.Error("result should record that a retry happened")
	}

	if len(repo.puts) != 1 {
		t.Fatalf("want one successful put, got %d", len(repo.puts))
	}
	if !strings.Contains(repo.content, "disable: true") {
		t.Error("the user's change was lost")
	}
	if !strings.Contains(repo.content, `beta:`) || !strings.Contains(repo.content, "variation: \"off\"") {
		t.Error("the other user's change to beta was clobbered")
	}
}

func TestCommitReturnsConflictWhenSameFlagChanged(t *testing.T) {
	sameFlagChanged := `alpha:
  variations:
    on: true
    off: false
  defaultRule:
    variation: "on"
beta:
  variations:
    on: true
    off: false
  defaultRule:
    variation: "on"
`
	repo := &fakeRepo{content: initial, sha: "sha1"}
	repo.mutate = func(get int) (string, string) {
		if get == 1 {
			return sameFlagChanged, "sha2"
		}
		return "", ""
	}
	srv := repo.server(t)
	c := newClient(t, srv)

	_, err := c.Commit(context.Background(), ChangeOp{
		Path:    "f.yaml",
		FlagKey: "alpha",
		BaseSHA: "sha1",
		Apply:   disableAlpha,
		FlagChanged: func(current []byte) (bool, error) {
			return flagBlock([]byte(initial), "alpha") != flagBlock(current, "alpha"), nil
		},
	}, who())

	if !errors.Is(err, ErrFlagConflict) {
		t.Errorf("want ErrFlagConflict so the UI can show the current state, got %v", err)
	}
	if len(repo.puts) != 0 {
		t.Error("nothing should have been written on conflict")
	}
}

func TestCommitGivesUpAfterMaxAttempts(t *testing.T) {
	repo := &fakeRepo{content: initial, sha: "sha1"}
	calls := 0
	repo.mutate = func(get int) (string, string) {
		calls++
		return initial, "sha-" + itoa(calls)
	}
	srv := repo.server(t)

	putAlwaysStale := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(map[string]string{
				"content": base64.StdEncoding.EncodeToString([]byte(initial)),
				"sha":     "moving-target",
			})
			return
		}
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"message":"sha mismatch"}`))
	}))
	defer putAlwaysStale.Close()
	_ = srv

	c := New(Config{APIBase: putAlwaysStale.URL, Owner: "acme", Repo: "flags"}, StaticToken("t"), putAlwaysStale.Client())

	_, err := c.Commit(context.Background(), ChangeOp{
		Path:        "f.yaml",
		FlagKey:     "alpha",
		Apply:       disableAlpha,
		MaxAttempts: 3,
	}, who())
	if err == nil {
		t.Fatal("expected failure after exhausting retries")
	}
	if !strings.Contains(err.Error(), "kept changing") {
		t.Errorf("error should explain the retry exhaustion, got: %v", err)
	}
}

func TestNoOpApplySkipsTheCommit(t *testing.T) {
	repo := &fakeRepo{content: initial, sha: "sha1"}
	srv := repo.server(t)
	c := newClient(t, srv)

	res, err := c.Commit(context.Background(), ChangeOp{
		Path:    "f.yaml",
		FlagKey: "alpha",
		BaseSHA: "sha1",
		Apply:   func(current []byte) ([]byte, error) { return current, nil },
	}, who())
	if err != nil {
		t.Fatal(err)
	}
	if res.CommitSHA != "" {
		t.Error("an unchanged file should not produce a commit")
	}
	if len(repo.puts) != 0 {
		t.Error("nothing should have been written")
	}
}

func TestReadFileDecodesContent(t *testing.T) {
	repo := &fakeRepo{content: initial, sha: "abc"}
	srv := repo.server(t)
	c := newClient(t, srv)

	f, err := c.ReadFile(context.Background(), "production/payments.goff.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if string(f.Content) != initial {
		t.Error("content mismatch")
	}
	if f.SHA != "abc" {
		t.Errorf("sha = %q", f.SHA)
	}
}

func TestSendsAuthAndVersionHeaders(t *testing.T) {
	var auth, version string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		version = r.Header.Get("X-GitHub-Api-Version")
		_ = json.NewEncoder(w).Encode(map[string]string{"content": "", "sha": "s"})
	}))
	defer srv.Close()

	c := New(Config{APIBase: srv.URL, Owner: "a", Repo: "b"}, StaticToken("installation-token"), srv.Client())
	if _, err := c.ReadFile(context.Background(), "f.yaml"); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer installation-token" {
		t.Errorf("auth = %q", auth)
	}
	if version != "2022-11-28" {
		t.Errorf("api version = %q", version)
	}
}

func TestHistoryParsesCommits(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"sha":"c1","commit":{"message":"[production] payments/alpha: disabled\n\nGOFF-Studio-User: jane@company.com","author":{"name":"Jane Doe","email":"jane@company.com","date":"2026-09-22T10:00:00Z"}}}]`))
	}))
	defer srv.Close()

	c := New(Config{APIBase: srv.URL, Owner: "a", Repo: "b"}, StaticToken("t"), srv.Client())
	commits, err := c.History(context.Background(), "production/payments.goff.yaml", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 1 {
		t.Fatalf("want 1 commit, got %d", len(commits))
	}
	if commits[0].Author != "Jane Doe" || commits[0].Email != "jane@company.com" {
		t.Errorf("author = %+v", commits[0])
	}
	if commits[0].When.Year() != 2026 {
		t.Errorf("date not parsed: %v", commits[0].When)
	}
}

func TestSurfacesAPIErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"message":"Validation Failed","errors":[{"message":"branch is protected"}]}`))
	}))
	defer srv.Close()

	c := New(Config{APIBase: srv.URL, Owner: "a", Repo: "b"}, StaticToken("t"), srv.Client())
	_, err := c.ReadFile(context.Background(), "f.yaml")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "branch is protected") {
		t.Errorf("should surface github's explanation, got %v", err)
	}
}

func flagBlock(content []byte, key string) string {
	lines := strings.Split(string(content), "\n")
	var out []string
	capturing := false
	for _, l := range lines {
		if strings.HasPrefix(l, key+":") {
			capturing = true
			out = append(out, l)
			continue
		}
		if capturing {
			if len(l) > 0 && l[0] != ' ' && l[0] != '\t' {
				break
			}
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}
