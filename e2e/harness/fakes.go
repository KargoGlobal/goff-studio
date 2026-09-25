// Command fakes runs the stub OIDC IDP, GitHub Contents API, analysis service, and state-dump endpoint the Playwright suite needs.
package main

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	owner    = "acme"
	repo     = "flags"
	clientID = "studio-e2e"
	subject  = "00u1jaime"
	fullName = "Jaime Moncayo"
	email    = "jaime@acme.com"
	group    = "flags-admins"
)

const paymentsFixture = `# Payment flags, owned by @acme/payments
new-checkout:
  variations:
    on: true
    off: false
  targeting:
    - name: gold-cohort
      query: (tier eq "gold") or (account_id eq "42")
      percentage:
        on: 20
        off: 80
  defaultRule:
    variation: "off"
  version: "3.1.0"
  metadata:
    owner: payments
    team: payments
`

const growthFixture = `banner-test:
  variations:
    on: true
    off: false
  defaultRule:
    variation: "on"
`

const rampFixture = `ramped:
  variations:
    on: true
    off: false
  targeting:
    - name: ramp
      query: (region eq "us")
      progressiveRollout:
        initial:
          variation: "off"
          percentage: 0
          date: 2026-10-01T00:00:00Z
        end:
          variation: "on"
          percentage: 100
          date: 2026-11-01T00:00:00Z
  defaultRule:
    variation: "off"
  metadata:
    team: platform

request-timeout:
  variations:
    control: 200
    fast: 150
  targeting:
    - name: exp-region-a
      query: region in ["region-a"]
      variation: control
  defaultRule:
    variation: control
  metadata:
    team: platform
    experiment:
      version: 1
      hash: md5-shard
      totalShards: 10000
      unit: {type: request, key: targetingKey}
      holdout: null
      allocations:
        exp-region-a:
          experimentKey: request-timeout-exp-region-a
          doLog: true
          startAt: null
          endAt: null
          passThrough: true
          layer: null
          splits:
            - variation: control
              shards:
                - {salt: "c1e0a7d25f", ranges: [[0, 100]]}
                - {salt: "9b3f41e2aa", ranges: [[0, 5000]]}
            - variation: fast
              shards:
                - {salt: "c1e0a7d25f", ranges: [[0, 100]]}
                - {salt: "9b3f41e2aa", ranges: [[5000, 10000]]}
`

type commit struct {
	Path    string `json:"path"`
	Message string `json:"message"`
	Author  string `json:"author"`
	Email   string `json:"email"`
	When    string `json:"when"`
}

type repoState struct {
	mu            sync.Mutex
	gen           int
	files         map[string]string
	shas          map[string]string
	commits       []commit
	analysisCalls int
}

func newRepoState() *repoState {
	s := &repoState{}
	s.reset()
	return s
}

func (s *repoState) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.gen++
	s.files = map[string]string{
		"production/payments.goff.yaml": paymentsFixture,
		"production/growth.goff.yaml":   growthFixture,
		"production/platform.goff.yaml": rampFixture,
	}
	// Generation-scoped so a reset looks like new content to the app's (path, sha) flag cache.
	s.shas = map[string]string{
		"production/payments.goff.yaml": fmt.Sprintf("sha-payments-%d-0", s.gen),
		"production/growth.goff.yaml":   fmt.Sprintf("sha-growth-%d-0", s.gen),
	}
	for path, body := range experimentFixtures(time.Now()) {
		s.files[path] = body
		s.shas[path] = fmt.Sprintf("sha-%s-%d-0", strings.NewReplacer("/", "-", ".", "-").Replace(path), s.gen)
	}
	for path, body := range metricFixtures() {
		s.files[path] = body
		s.shas[path] = fmt.Sprintf("sha-%s-%d-0", strings.NewReplacer("/", "-", ".", "-").Replace(path), s.gen)
	}
	s.commits = nil
	s.analysisCalls = 0
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	idpAddr := "127.0.0.1:" + env("E2E_IDP_PORT", "9401")
	ghAddr := "127.0.0.1:" + env("E2E_GITHUB_PORT", "9402")
	dumpAddr := "127.0.0.1:" + env("E2E_DUMP_PORT", "9403")
	analysisAddr := "127.0.0.1:" + env("E2E_ANALYSIS_PORT", "9404")
	idpBase := "http://" + idpAddr

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		log.Fatalf("generating signing key: %v", err)
	}

	state := newRepoState()

	serve(idpAddr, idpHandler(idpBase, key))
	serve(ghAddr, githubHandler(state))
	serve(dumpAddr, dumpHandler(state))
	serve(analysisAddr, analysisHandler(state))

	log.Printf("fakes up: idp %s, github %s, dump %s, analysis %s", idpAddr, ghAddr, dumpAddr, analysisAddr)
	select {}
}

func serve(addr string, handler http.Handler) {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("listening on %s: %v", addr, err)
	}
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	go func() { log.Fatal(srv.Serve(listener)) }()
}

func idpHandler(base string, key *rsa.PrivateKey) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"issuer":                                base,
			"authorization_endpoint":                base + "/authorize",
			"token_endpoint":                        base + "/token",
			"jwks_uri":                              base + "/jwks",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})

	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		pub := key.Public().(*rsa.PublicKey)
		writeJSON(w, map[string]any{"keys": []map[string]any{{
			"kty": "RSA", "alg": "RS256", "use": "sig", "kid": "k1",
			"n": base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
		}}})
	})

	// Auto-approves so the suite never drives a login form.
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		redirect := r.URL.Query().Get("redirect_uri")
		if redirect == "" {
			http.Error(w, "missing redirect_uri", http.StatusBadRequest)
			return
		}
		target := redirect + "?code=fake-code&state=" + url.QueryEscape(r.URL.Query().Get("state"))
		http.Redirect(w, r, target, http.StatusFound)
	})

	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		now := time.Now()
		idToken, err := signJWT(key, map[string]any{
			"iss":    base,
			"aud":    clientID,
			"sub":    subject,
			"iat":    now.Unix(),
			"exp":    now.Add(time.Hour).Unix(),
			"name":   fullName,
			"email":  email,
			"groups": []string{group},
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]any{
			"access_token": "fake-access-token",
			"token_type":   "Bearer",
			"expires_in":   3600,
			"id_token":     idToken,
		})
	})

	return mux
}

func signJWT(key *rsa.PrivateKey, claims map[string]any) (string, error) {
	segment := func(v any) (string, error) {
		raw, err := json.Marshal(v)
		if err != nil {
			return "", err
		}
		return base64.RawURLEncoding.EncodeToString(raw), nil
	}

	header, err := segment(map[string]string{"alg": "RS256", "typ": "JWT", "kid": "k1"})
	if err != nil {
		return "", err
	}
	payload, err := segment(claims)
	if err != nil {
		return "", err
	}

	signing := header + "." + payload
	digest := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

func githubHandler(state *repoState) http.Handler {
	contentsPrefix := fmt.Sprintf("/repos/%s/%s/contents/", owner, repo)
	commitsPath := fmt.Sprintf("/repos/%s/%s/commits", owner, repo)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state.mu.Lock()
		defer state.mu.Unlock()

		if strings.HasPrefix(r.URL.Path, commitsPath) {
			state.serveHistory(w)
			return
		}

		if !strings.HasPrefix(r.URL.Path, contentsPrefix) {
			notFound(w)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, contentsPrefix)

		if r.Method == http.MethodGet {
			state.serveRead(w, path)
			return
		}
		state.servePut(w, r, path)
	})
}

func (s *repoState) serveHistory(w http.ResponseWriter) {
	out := make([]map[string]any, 0, len(s.commits))
	for i := len(s.commits) - 1; i >= 0; i-- {
		c := s.commits[i]
		out = append(out, map[string]any{
			"sha": fmt.Sprintf("commit-%d", i),
			"commit": map[string]any{
				"message": c.Message,
				"author":  map[string]any{"name": c.Author, "email": c.Email, "date": c.When},
			},
		})
	}
	writeJSON(w, out)
}

func (s *repoState) serveRead(w http.ResponseWriter, path string) {
	if body, ok := s.files[path]; ok {
		writeJSON(w, map[string]string{
			"content": base64.StdEncoding.EncodeToString([]byte(body)),
			"sha":     s.shas[path],
			"path":    path,
			"type":    "file",
		})
		return
	}

	entries := []map[string]string{}
	seenDirs := map[string]bool{}

	for name := range s.files {
		rest := name
		if path != "" {
			if !strings.HasPrefix(name, path+"/") {
				continue
			}
			rest = strings.TrimPrefix(name, path+"/")
		}

		if dir, _, nested := strings.Cut(rest, "/"); nested {
			if seenDirs[dir] {
				continue
			}
			seenDirs[dir] = true
			full := dir
			if path != "" {
				full = path + "/" + dir
			}
			entries = append(entries, map[string]string{
				"name": dir,
				"path": full,
				"type": "dir",
			})
			continue
		}

		full := rest
		if path != "" {
			full = path + "/" + rest
		}
		entries = append(entries, map[string]string{
			"name": rest,
			"path": full,
			"type": "file",
		})
	}

	if len(entries) == 0 {
		notFound(w)
		return
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i]["path"] < entries[j]["path"] })
	writeJSON(w, entries)
}

func (s *repoState) servePut(w http.ResponseWriter, r *http.Request, path string) {
	var payload struct {
		Message string `json:"message"`
		Content string `json:"content"`
		SHA     string `json:"sha"`
		Author  struct {
			Name  string `json:"name"`
			Email string `json:"email"`
		} `json:"author"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}

	if payload.SHA != s.shas[path] {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"message":"sha mismatch"}`))
		return
	}

	decoded, err := base64.StdEncoding.DecodeString(payload.Content)
	if err != nil {
		http.Error(w, "content is not base64", http.StatusBadRequest)
		return
	}

	slug := strings.NewReplacer("/", "-", ".", "-").Replace(path)
	s.files[path] = string(decoded)
	s.shas[path] = fmt.Sprintf("sha-%s-%d-%d", slug, s.gen, len(s.commits)+1)
	s.commits = append(s.commits, commit{
		Path:    path,
		Message: payload.Message,
		Author:  payload.Author.Name,
		Email:   payload.Author.Email,
		When:    time.Now().UTC().Format(time.RFC3339),
	})

	log.Printf("COMMIT %s\n%s", path, payload.Message)
	writeJSON(w, map[string]any{
		"content": map[string]string{"path": path, "sha": s.shas[path]},
		"commit":  map[string]string{"sha": fmt.Sprintf("commit-%d", len(s.commits)-1)},
	})
}

func dumpHandler(state *repoState) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /reset", func(w http.ResponseWriter, r *http.Request) {
		state.reset()
		writeJSON(w, map[string]string{"status": "reset"})
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		state.mu.Lock()
		defer state.mu.Unlock()

		commits := state.commits
		if commits == nil {
			commits = []commit{}
		}
		writeJSON(w, map[string]any{
			"files":         state.files,
			"shas":          state.shas,
			"commits":       commits,
			"analysisCalls": state.analysisCalls,
			"user":          map[string]string{"name": fullName, "email": email, "subject": subject},
		})
	})

	return mux
}

func writeJSON(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

func notFound(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte(`{"message":"Not Found"}`))
}
