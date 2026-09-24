package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sample = `server:
  addr: ":9000"
  baseURL: https://studio.example.com
  sessionSecret: a-long-enough-secret-value
  secureCookies: true

oidc:
  issuerURL: https://acme.okta.com/oauth2/default
  clientID: studio-client
  clientSecret: shh

github:
  owner: acme
  repo: flags
  appID: "123"
  installationID: "456"
  privateKeyPath: /etc/goff-studio/key.pem

environments:
  - name: dev
  - name: staging
  - name: production
    protected: true
    display: Production

permissions:
  - group: flags-admins
    allow: ["*"]
  - group: marketing
    allow: ["growth"]
    environments: [production]
    actions: [toggle, rollout]
`

func write(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "studio.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadParsesEverything(t *testing.T) {
	cfg, err := Load(write(t, base(t)))
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Server.Addr != ":9000" || !cfg.Server.SecureCookies {
		t.Errorf("server = %+v", cfg.Server)
	}
	if cfg.OIDC.IssuerURL != "https://acme.okta.com/oauth2/default" {
		t.Errorf("oidc = %+v", cfg.OIDC)
	}
	if cfg.GitHub.Branch != "main" {
		t.Errorf("branch should default to main, got %q", cfg.GitHub.Branch)
	}
	if len(cfg.Environments) != 3 {
		t.Fatalf("environments = %+v", cfg.Environments)
	}
	if !cfg.Environments[2].Protected {
		t.Error("production should be protected")
	}
	if cfg.Environments[0].Display != "Dev" {
		t.Errorf("display should default from the name, got %q", cfg.Environments[0].Display)
	}
	if cfg.Environments[1].Order != 2 {
		t.Errorf("order should default to position, got %d", cfg.Environments[1].Order)
	}
	if cfg.PollSeconds != 60 {
		t.Errorf("poll seconds = %d", cfg.PollSeconds)
	}
	if cfg.RedirectURL() != "https://studio.example.com/auth/callback" {
		t.Errorf("redirect = %q", cfg.RedirectURL())
	}
}

func TestEnvironmentOverridesWin(t *testing.T) {
	t.Setenv("GOFF_STUDIO_ADDR", ":7777")
	t.Setenv("GOFF_STUDIO_GITHUB_OWNER", "other-org")
	t.Setenv("GOFF_STUDIO_OIDC_CLIENT_SECRET", "from-env")
	t.Setenv("GOFF_STUDIO_EXPECTED_POLL_SECONDS", "15")
	t.Setenv("GOFF_STUDIO_SECURE_COOKIES", "false")

	cfg, err := Load(write(t, base(t)))
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Server.Addr != ":7777" {
		t.Errorf("addr = %q", cfg.Server.Addr)
	}
	if cfg.GitHub.Owner != "other-org" {
		t.Errorf("owner = %q", cfg.GitHub.Owner)
	}
	if cfg.OIDC.ClientSecret != "from-env" {
		t.Errorf("client secret not overridden")
	}
	if cfg.PollSeconds != 15 {
		t.Errorf("poll = %d", cfg.PollSeconds)
	}
	if cfg.Server.SecureCookies {
		t.Error("secureCookies should be overridable to false")
	}
}

func TestShortSessionSecretRejected(t *testing.T) {
	body := strings.Replace(sample, "sessionSecret: a-long-enough-secret-value", "sessionSecret: short", 1)
	if _, err := Load(write(t, body)); err == nil {
		t.Error("a short session secret must be rejected")
	}
}

func TestMissingGitHubCredentialsRejected(t *testing.T) {
	body := strings.Replace(sample, `  appID: "123"
  installationID: "456"
  privateKeyPath: /etc/goff-studio/key.pem`, "", 1)
	_, err := Load(write(t, body))
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "devToken") {
		t.Errorf("error should mention the dev fallback, got: %v", err)
	}
}

func TestDevTokenSatisfiesGitHubCredentials(t *testing.T) {
	body := strings.Replace(sample, `  appID: "123"
  installationID: "456"
  privateKeyPath: /etc/goff-studio/key.pem`, `  devToken: ghp_local`, 1)

	cfg, err := Load(write(t, body))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.UsingDevToken() {
		t.Error("UsingDevToken should report the fallback so it can be logged loudly")
	}
}

func TestNoEnvironmentsRejected(t *testing.T) {
	full := base(t)
	body := full[:strings.Index(full, "environments:")] + "permissions:\n  - group: g\n    allow: [\"*\"]\n"
	if _, err := Load(write(t, body)); err == nil {
		t.Error("a config with no environments must be rejected")
	}
}

func TestDuplicateEnvironmentRejected(t *testing.T) {
	body := strings.Replace(sample, "  - name: staging", "  - name: dev", 1)
	if _, err := Load(write(t, body)); err == nil {
		t.Error("duplicate environments must be rejected")
	}
}

func TestInvalidPermissionActionRejectedAtLoad(t *testing.T) {
	body := strings.Replace(sample, "actions: [toggle, rollout]", "actions: [toggle, launch_missiles]", 1)
	if _, err := Load(write(t, body)); err == nil {
		t.Error("an unknown permission action must fail at startup, not at request time")
	}
}

func loadErr(t *testing.T, body string) string {
	t.Helper()
	_, err := Load(write(t, body))
	if err == nil {
		t.Fatal("expected a validation error, got none")
	}
	return err.Error()
}

func replace(t *testing.T, old, new string) string {
	t.Helper()
	body := base(t)
	if !strings.Contains(body, old) {
		t.Fatalf("sample does not contain %q", old)
	}
	return strings.Replace(body, old, new, 1)
}

const appCredentials = `  appID: "123"
  installationID: "456"
  privateKeyPath: /etc/goff-studio/key.pem`

func withoutAppCredentials(t *testing.T, replacement string) string {
	t.Helper()
	if !strings.Contains(sample, appCredentials) {
		t.Fatal("sample no longer contains the GitHub App block")
	}
	return strings.Replace(sample, appCredentials, replacement, 1)
}

func withKeyPath(t *testing.T, path string) string {
	t.Helper()
	return strings.Replace(sample, "privateKeyPath: /etc/goff-studio/key.pem", "privateKeyPath: "+path, 1)
}

func withKey(t *testing.T, pem string) string {
	t.Helper()
	key := filepath.Join(t.TempDir(), "app.pem")
	if err := os.WriteFile(key, []byte(pem), 0o600); err != nil {
		t.Fatal(err)
	}
	return withKeyPath(t, key)
}

func base(t *testing.T) string {
	t.Helper()
	return withKey(t, "-----BEGIN RSA PRIVATE KEY-----\nstub\n-----END RSA PRIVATE KEY-----\n")
}

func assertMentions(t *testing.T, msg string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(msg, w) {
			t.Errorf("error should mention %q, got: %s", w, msg)
		}
	}
}

func TestExampleConfigLoads(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "studio.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	key := filepath.Join(t.TempDir(), "github-app.pem")
	if err := os.WriteFile(key, []byte("-----BEGIN RSA PRIVATE KEY-----\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOFF_STUDIO_GITHUB_PRIVATE_KEY_PATH", key)

	cfg, err := Load(write(t, string(raw)))
	if err != nil {
		t.Fatalf("studio.example.yaml must load cleanly, operators copy it: %v", err)
	}
	if len(cfg.Warnings()) != 0 {
		t.Errorf("the example config should warn about nothing, got %v", cfg.Warnings())
	}
	if cfg.PollSeconds != 30 || len(cfg.Environments) != 3 {
		t.Errorf("example parsed unexpectedly: poll=%d envs=%d", cfg.PollSeconds, len(cfg.Environments))
	}
}

func TestMissingOIDCIssuerRejected(t *testing.T) {
	msg := loadErr(t, replace(t, "  issuerURL: https://acme.okta.com/oauth2/default\n", ""))
	assertMentions(t, msg, "oidc.issuerURL", "GOFF_STUDIO_OIDC_ISSUER_URL")
}

func TestMissingOIDCClientIDRejected(t *testing.T) {
	msg := loadErr(t, replace(t, "  clientID: studio-client\n", ""))
	assertMentions(t, msg, "oidc.clientID", "GOFF_STUDIO_OIDC_CLIENT_ID")
}

func TestMissingOIDCClientSecretRejected(t *testing.T) {
	msg := loadErr(t, replace(t, "  clientSecret: shh\n", ""))
	assertMentions(t, msg, "oidc.clientSecret", "GOFF_STUDIO_OIDC_CLIENT_SECRET")
}

func TestOIDCIssuerMustBeAbsoluteHTTPSURL(t *testing.T) {
	cases := []struct {
		name   string
		issuer string
	}{
		{"relative", "acme.okta.com/oauth2/default"},
		{"scheme only", "https://"},
		{"plaintext remote host", "http://acme.okta.com/oauth2/default"},
		{"not a url", "://nope"},
		{"query string", "https://acme.okta.com/oauth2/default?foo=bar"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := loadErr(t, replace(t, "issuerURL: https://acme.okta.com/oauth2/default", "issuerURL: "+tc.issuer))
			assertMentions(t, msg, "oidc.issuerURL", "GOFF_STUDIO_OIDC_ISSUER_URL")
		})
	}
}

func TestOIDCIssuerAcceptsLocalhostOverHTTP(t *testing.T) {
	for _, issuer := range []string{"http://localhost:5556/dex", "http://127.0.0.1:5556/dex", "https://acme.okta.com/oauth2/default/"} {
		body := replace(t, "issuerURL: https://acme.okta.com/oauth2/default", "issuerURL: "+issuer)
		if _, err := Load(write(t, body)); err != nil {
			t.Errorf("issuer %q should be accepted: %v", issuer, err)
		}
	}
}

func TestBaseURLMustBeAbsolute(t *testing.T) {
	for _, base := range []string{"studio.example.com", "/studio", "https://", "https://studio.example.com/?next=1"} {
		body := replace(t, "baseURL: https://studio.example.com", "baseURL: "+base)
		msg := loadErr(t, body)
		assertMentions(t, msg, "server.baseURL", "GOFF_STUDIO_BASE_URL")
	}
}

func TestBaseURLEmptyRejected(t *testing.T) {
	t.Setenv("GOFF_STUDIO_BASE_URL", "")
	msg := loadErr(t, replace(t, "baseURL: https://studio.example.com", `baseURL: ""`))
	assertMentions(t, msg, "server.baseURL", "GOFF_STUDIO_BASE_URL")
}

func TestBaseURLWithPathKeepsRedirectDerivable(t *testing.T) {
	body := replace(t, "baseURL: https://studio.example.com", "baseURL: https://example.com/studio/")
	cfg, err := Load(write(t, body))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RedirectURL() != "https://example.com/studio/auth/callback" {
		t.Errorf("redirect = %q", cfg.RedirectURL())
	}
}

func TestAddrMustBeHostPort(t *testing.T) {
	for _, addr := range []string{"9000", "localhost", ":not-a-port", ":99999", `""`} {
		body := replace(t, `addr: ":9000"`, "addr: "+addr)
		msg := loadErr(t, body)
		assertMentions(t, msg, "server.addr", "GOFF_STUDIO_ADDR")
	}
}

func TestAddrAcceptsHostAndNamedPort(t *testing.T) {
	for _, addr := range []string{`":8080"`, `"0.0.0.0:8080"`, `"127.0.0.1:0"`, `"[::1]:8080"`, `":http"`} {
		body := replace(t, `addr: ":9000"`, "addr: "+addr)
		if _, err := Load(write(t, body)); err != nil {
			t.Errorf("addr %s should be accepted: %v", addr, err)
		}
	}
}

func TestPollSecondsMustBePositive(t *testing.T) {
	for _, v := range []string{"0", "-5"} {
		body := base(t) + "expectedPollSeconds: " + v + "\n"
		msg := loadErr(t, body)
		assertMentions(t, msg, "expectedPollSeconds", "GOFF_STUDIO_EXPECTED_POLL_SECONDS")
	}
}

func TestPollSecondsPositiveAccepted(t *testing.T) {
	cfg, err := Load(write(t, base(t)+"expectedPollSeconds: 5\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PollSeconds != 5 {
		t.Errorf("poll = %d", cfg.PollSeconds)
	}
}

func TestNonNumericEnvOverrideRejected(t *testing.T) {
	t.Setenv("GOFF_STUDIO_EXPECTED_POLL_SECONDS", "sixty")
	msg := loadErr(t, base(t))
	assertMentions(t, msg, "GOFF_STUDIO_EXPECTED_POLL_SECONDS", "whole number")
}

func TestNonBooleanEnvOverrideRejected(t *testing.T) {
	t.Setenv("GOFF_STUDIO_SECURE_COOKIES", "yes-please")
	msg := loadErr(t, base(t))
	assertMentions(t, msg, "GOFF_STUDIO_SECURE_COOKIES", "boolean")
}

func TestSessionSecretErrorNamesEnvVar(t *testing.T) {
	msg := loadErr(t, replace(t, "sessionSecret: a-long-enough-secret-value", "sessionSecret: short"))
	assertMentions(t, msg, "server.sessionSecret", "GOFF_STUDIO_SESSION_SECRET", "16")
}

func TestGitHubOwnerAndRepoErrorsNameTheirKeys(t *testing.T) {
	msg := loadErr(t, replace(t, "  owner: acme\n", ""))
	assertMentions(t, msg, "github.owner", "GOFF_STUDIO_GITHUB_OWNER")

	msg = loadErr(t, replace(t, "  repo: flags\n", ""))
	assertMentions(t, msg, "github.repo", "GOFF_STUDIO_GITHUB_REPO")
}

func TestGitHubOwnerRepoSlugRejected(t *testing.T) {
	msg := loadErr(t, replace(t, "owner: acme", "owner: acme/flags"))
	assertMentions(t, msg, "github.owner", "github.repo")

	msg = loadErr(t, replace(t, "repo: flags", "repo: acme/flags"))
	assertMentions(t, msg, "github.repo", "github.owner")
}

func TestGitHubBranchCannotBeBlanked(t *testing.T) {
	msg := loadErr(t, replace(t, "  repo: flags\n", "  repo: flags\n  branch: \"\"\n"))
	assertMentions(t, msg, "github.branch", "GOFF_STUDIO_GITHUB_BRANCH")
}

func TestGitHubAppIDsMustBeNumeric(t *testing.T) {
	msg := loadErr(t, replace(t, `appID: "123"`, `appID: "Iv1.abc123"`))
	assertMentions(t, msg, "github.appID", "GOFF_STUDIO_GITHUB_APP_ID")

	msg = loadErr(t, replace(t, `installationID: "456"`, `installationID: "not-a-number"`))
	assertMentions(t, msg, "github.installationID", "GOFF_STUDIO_GITHUB_INSTALLATION_ID")
}

func TestMissingGitHubCredentialsNamesEveryMissingKey(t *testing.T) {
	msg := loadErr(t, withoutAppCredentials(t, ""))
	assertMentions(t, msg, "github.appID", "github.installationID", "github.privateKeyPath", "github.devToken")
}

func TestMissingPrivateKeyFileRejected(t *testing.T) {
	msg := loadErr(t, sample)
	assertMentions(t, msg, "github.privateKeyPath", "GOFF_STUDIO_GITHUB_PRIVATE_KEY_PATH", "does not exist")
}

func TestReadablePrivateKeyAccepted(t *testing.T) {
	if _, err := Load(write(t, withKey(t, "-----BEGIN RSA PRIVATE KEY-----\n"))); err != nil {
		t.Fatalf("a readable key must be accepted: %v", err)
	}
}

func TestEmptyPrivateKeyFileRejected(t *testing.T) {
	msg := loadErr(t, withKey(t, ""))
	assertMentions(t, msg, "github.privateKeyPath", "empty")
}

func TestPrivateKeyDirectoryRejected(t *testing.T) {
	msg := loadErr(t, withKeyPath(t, t.TempDir()))
	assertMentions(t, msg, "github.privateKeyPath", "directory")
}

func TestDevTokenAlongsideAppCredentialsWarns(t *testing.T) {
	body := replace(t, "  repo: flags\n", "  repo: flags\n  devToken: ghp_local\n")
	cfg, err := Load(write(t, body))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.UsingDevToken() {
		t.Fatal("dev token should still be honoured")
	}
	if !warned(cfg, "devToken") {
		t.Errorf("expected a warning that the App settings are ignored, got %v", cfg.Warnings())
	}
}

func TestDevTokenSkipsAppIDValidation(t *testing.T) {
	cfg, err := Load(write(t, withoutAppCredentials(t, "  devToken: ghp_local")))
	if err != nil {
		t.Fatalf("the dev-token path must not require App credentials: %v", err)
	}
	if !cfg.UsingDevToken() {
		t.Error("UsingDevToken should report the fallback")
	}
}

func TestInsecureCookiesOverHTTPSWarns(t *testing.T) {
	cfg, err := Load(write(t, replace(t, "secureCookies: true", "secureCookies: false")))
	if err != nil {
		t.Fatal(err)
	}
	if !warned(cfg, "secureCookies") {
		t.Errorf("https baseURL with insecure cookies should warn, got %v", cfg.Warnings())
	}
}

func TestPlaintextRemoteBaseURLWarnsButLoads(t *testing.T) {
	cfg, err := Load(write(t, replace(t, "baseURL: https://studio.example.com", "baseURL: http://studio.example.com")))
	if err != nil {
		t.Fatalf("http baseURL should load with a warning, not fail: %v", err)
	}
	if !warned(cfg, "https") {
		t.Errorf("expected an https warning, got %v", cfg.Warnings())
	}
}

func TestLocalhostBaseURLIsQuiet(t *testing.T) {
	body := replace(t, "baseURL: https://studio.example.com", "baseURL: http://localhost:8080")
	body = strings.Replace(body, "secureCookies: true", "secureCookies: false", 1)

	cfg, err := Load(write(t, body))
	if err != nil {
		t.Fatal(err)
	}
	if warned(cfg, "https") || warned(cfg, "secureCookies") {
		t.Errorf("local development should not warn, got %v", cfg.Warnings())
	}
}

func TestEnvironmentNameMustBeUsableAsPathSegment(t *testing.T) {
	for _, name := range []string{"pro duction", "../etc", "prod/us"} {
		msg := loadErr(t, replace(t, "  - name: staging", "  - name: "+name))
		assertMentions(t, msg, "environments[1]")
	}
}

func TestEnvironmentErrorsUseIndexedKeys(t *testing.T) {
	msg := loadErr(t, replace(t, "  - name: staging", "  - name: dev"))
	assertMentions(t, msg, "environments[1]", "dev")

	msg = loadErr(t, replace(t, "  - name: staging", "  - display: Staging"))
	assertMentions(t, msg, "environments[1]")
}

func TestPermissionsErrorIsPrefixed(t *testing.T) {
	msg := loadErr(t, replace(t, "actions: [toggle, rollout]", "actions: [toggle, launch_missiles]"))
	assertMentions(t, msg, "permissions", "launch_missiles")
}

func TestUnknownPermissionEnvironmentWarns(t *testing.T) {
	cfg, err := Load(write(t, replace(t, "environments: [production]", "environments: [prodution]")))
	if err != nil {
		t.Fatalf("a typo'd environment should warn, not fail: %v", err)
	}
	if !warned(cfg, "prodution") {
		t.Errorf("expected a warning naming the unmatched environment, got %v", cfg.Warnings())
	}
}

func TestEmptyPermissionsWarns(t *testing.T) {
	full := base(t)
	body := full[:strings.Index(full, "permissions:")]
	cfg, err := Load(write(t, body))
	if err != nil {
		t.Fatal(err)
	}
	if !warned(cfg, "deny every request") {
		t.Errorf("expected a deny-all warning, got %v", cfg.Warnings())
	}
}

func TestFullyValidConfigProducesNoWarnings(t *testing.T) {
	cfg, err := Load(write(t, base(t)))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Warnings()) != 0 {
		t.Errorf("a fully valid config should warn about nothing, got %v", cfg.Warnings())
	}
}

func warned(cfg *Config, substr string) bool {
	for _, w := range cfg.Warnings() {
		if strings.Contains(w, substr) {
			return true
		}
	}
	return false
}

func TestEnvironmentLookup(t *testing.T) {
	cfg, err := Load(write(t, base(t)))
	if err != nil {
		t.Fatal(err)
	}

	env, ok := cfg.Environment("production")
	if !ok || !env.Protected {
		t.Errorf("production lookup = %+v %v", env, ok)
	}
	if _, ok := cfg.Environment("nope"); ok {
		t.Error("unknown environment should not be found")
	}

	names := cfg.EnvironmentNames()
	if len(names) != 3 || names[0] != "dev" {
		t.Errorf("names = %v", names)
	}
}

func TestStorageBackendDefaultsToGitHub(t *testing.T) {
	cfg, err := Load(write(t, base(t)))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Storage.Backend != "github" {
		t.Errorf("backend = %q, want github by default", cfg.Storage.Backend)
	}
	if !cfg.UsesGitHub() {
		t.Error("UsesGitHub should be true by default")
	}
}

func TestFileBackendNeedsAPathAndSkipsGitHubValidation(t *testing.T) {
	dir := t.TempDir()

	body := strings.Replace(base(t), "github:", "storage:\n  backend: file\n  path: "+dir+"\n\ngithub:", 1)
	body = strings.Replace(body, "  appID: \"123456\"\n", "", 1)
	body = strings.Replace(body, "  installationID: \"7890123\"\n", "", 1)

	cfg, err := Load(write(t, body))
	if err != nil {
		t.Fatalf("the file backend must not require GitHub App credentials: %v", err)
	}
	if cfg.UsesGitHub() {
		t.Error("UsesGitHub should be false")
	}

	found := false
	for _, w := range cfg.Warnings() {
		if strings.Contains(w, "no history, attribution or review") {
			found = true
		}
	}
	if !found {
		t.Errorf("choosing the file backend should warn about what it gives up, got %v", cfg.Warnings())
	}
}

func TestFileBackendRejectsMissingPath(t *testing.T) {
	body := strings.Replace(base(t), "github:", "storage:\n  backend: file\n\ngithub:", 1)
	_, err := Load(write(t, body))
	if err == nil {
		t.Fatal("the file backend needs a path")
	}
	if !strings.Contains(err.Error(), "storage.path") {
		t.Errorf("error should name the key, got %v", err)
	}
}

func TestUnknownStorageBackendRejected(t *testing.T) {
	body := strings.Replace(base(t), "github:", "storage:\n  backend: dynamodb\n\ngithub:", 1)
	_, err := Load(write(t, body))
	if err == nil {
		t.Fatal("an unknown backend must be rejected at startup")
	}
	if !strings.Contains(err.Error(), "available:") {
		t.Errorf("error should list the backends this binary was built with, got %v", err)
	}
}

func TestStorageBackendFromEnvironment(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GOFF_STUDIO_STORAGE", "file")
	t.Setenv("GOFF_STUDIO_STORAGE_PATH", dir)

	cfg, err := Load(write(t, base(t)))
	if err != nil {
		t.Fatalf("env vars alone should select a backend, since a container may mount no config file: %v", err)
	}
	if cfg.UsesGitHub() || cfg.Storage.Path != dir {
		t.Errorf("storage = %+v", cfg.Storage)
	}
}

func TestS3BackendNeedsABucketAndMustBeCompiledIn(t *testing.T) {
	body := strings.Replace(base(t), "github:", "storage:\n  backend: s3\n\ngithub:", 1)
	_, err := Load(write(t, body))
	if err == nil {
		t.Fatal("the s3 backend needs a bucket")
	}
	if !strings.Contains(err.Error(), "storage.bucket") {
		t.Errorf("error should name the key, got %v", err)
	}

	body = strings.Replace(base(t), "github:", "storage:\n  backend: s3\n  bucket: my-flags-bucket\n\ngithub:", 1)
	_, err = Load(write(t, body))
	if err == nil {
		t.Fatal("the core binary has no s3 backend, so this must fail rather than starting and failing later")
	}
	if !strings.Contains(err.Error(), "not compiled into this binary") {
		t.Errorf("error should explain that a different image is needed, got %v", err)
	}
}

func TestUsesGitHubOnlyForTheGitHubBackend(t *testing.T) {
	dir := t.TempDir()
	body := strings.Replace(base(t), "github:", "storage:\n  backend: file\n  path: "+dir+"\n\ngithub:", 1)
	cfg, err := Load(write(t, body))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UsesGitHub() {
		t.Error("the file backend must not pull in GitHub credential handling")
	}
}

func TestLoadOptionalRunsWithNoConfigFileAtAll(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "dev"), 0o750); err != nil {
		t.Fatal(err)
	}

	t.Setenv("GOFF_STUDIO_SESSION_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("GOFF_STUDIO_OIDC_ISSUER_URL", "https://issuer.example.com")
	t.Setenv("GOFF_STUDIO_OIDC_CLIENT_ID", "client")
	t.Setenv("GOFF_STUDIO_OIDC_CLIENT_SECRET", "secret")
	t.Setenv("GOFF_STUDIO_STORAGE", "file")
	t.Setenv("GOFF_STUDIO_STORAGE_PATH", dir)
	t.Setenv("GOFF_STUDIO_ENVIRONMENTS", "dev, production")
	t.Setenv("GOFF_STUDIO_PERMISSIONS", `[{group: flags-admins, allow: ["*"]}]`)

	cfg, err := LoadOptional(filepath.Join(dir, "absent.yaml"))
	if err != nil {
		t.Fatalf("a container with only env vars must start: %v", err)
	}

	if len(cfg.Environments) != 2 || cfg.Environments[0].Name != "dev" || cfg.Environments[1].Name != "production" {
		t.Errorf("environments = %+v, want dev and production", cfg.Environments)
	}
	if len(cfg.Permissions) != 1 || cfg.Permissions[0].Group != "flags-admins" {
		t.Errorf("permissions = %+v", cfg.Permissions)
	}

	var warned bool
	for _, w := range cfg.Warnings() {
		if strings.Contains(w, "no config file") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("running without a config file should say so once, got %v", cfg.Warnings())
	}
}

func TestLoadStillFailsOnAMissingFileWhenAskedForOne(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "typo.yaml")); err == nil {
		t.Fatal("an explicit -config path that does not exist must be an error, not a silent default")
	}
}

func TestEnvironmentsAcceptFullYAMLForDisplayAndProtection(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "prod"), 0o750); err != nil {
		t.Fatal(err)
	}

	t.Setenv("GOFF_STUDIO_SESSION_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("GOFF_STUDIO_OIDC_ISSUER_URL", "https://issuer.example.com")
	t.Setenv("GOFF_STUDIO_OIDC_CLIENT_ID", "client")
	t.Setenv("GOFF_STUDIO_OIDC_CLIENT_SECRET", "secret")
	t.Setenv("GOFF_STUDIO_STORAGE", "file")
	t.Setenv("GOFF_STUDIO_STORAGE_PATH", dir)
	t.Setenv("GOFF_STUDIO_PERMISSIONS", `[{group: "*", allow: ["*"]}]`)
	t.Setenv("GOFF_STUDIO_ENVIRONMENTS", `[{name: prod, display: Production, protected: true}]`)

	cfg, err := LoadOptional(filepath.Join(dir, "absent.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	if len(cfg.Environments) != 1 {
		t.Fatalf("environments = %+v", cfg.Environments)
	}
	if !cfg.Environments[0].Protected || cfg.Environments[0].Display != "Production" {
		t.Errorf("full YAML should carry display and protected, got %+v", cfg.Environments[0])
	}
}

func TestBadYAMLInAListEnvVarIsReportedClearly(t *testing.T) {
	t.Setenv("GOFF_STUDIO_PERMISSIONS", "{not: [valid")

	_, err := LoadOptional(filepath.Join(t.TempDir(), "absent.yaml"))
	if err == nil {
		t.Fatal("malformed YAML in an env var must fail")
	}
	if !strings.Contains(err.Error(), "GOFF_STUDIO_PERMISSIONS") {
		t.Errorf("the error should name the variable, got %v", err)
	}
}

func TestStorageOptionsCanComeFromTheEnvironment(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "dev"), 0o750); err != nil {
		t.Fatal(err)
	}

	t.Setenv("GOFF_STUDIO_SESSION_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("GOFF_STUDIO_OIDC_ISSUER_URL", "https://issuer.example.com")
	t.Setenv("GOFF_STUDIO_OIDC_CLIENT_ID", "client")
	t.Setenv("GOFF_STUDIO_OIDC_CLIENT_SECRET", "secret")
	t.Setenv("GOFF_STUDIO_STORAGE", "file")
	t.Setenv("GOFF_STUDIO_STORAGE_PATH", dir)
	t.Setenv("GOFF_STUDIO_ENVIRONMENTS", "dev")
	t.Setenv("GOFF_STUDIO_PERMISSIONS", `[{group: "*", allow: ["*"]}]`)
	t.Setenv("GOFF_STUDIO_STORAGE_OPTIONS", `{endpoint: "http://minio:9000"}`)

	cfg, err := LoadOptional(filepath.Join(dir, "absent.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	if got := cfg.Storage.Options["endpoint"]; got != "http://minio:9000" {
		t.Errorf("options = %+v, want an endpoint for S3-compatible storage", cfg.Storage.Options)
	}
}

func TestEnvVarsOverrideAFileAndReplaceListsWholesale(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "dev"), 0o750); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(dir, "studio.yaml")
	body := "server:\n  addr: \":9999\"\nstorage:\n  backend: file\n  path: " + dir +
		"\nenvironments:\n  - name: fromfile\npermissions:\n  - group: file-group\n    allow: [\"*\"]\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("GOFF_STUDIO_SESSION_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("GOFF_STUDIO_OIDC_ISSUER_URL", "https://issuer.example.com")
	t.Setenv("GOFF_STUDIO_OIDC_CLIENT_ID", "client")
	t.Setenv("GOFF_STUDIO_OIDC_CLIENT_SECRET", "secret")
	t.Setenv("GOFF_STUDIO_ADDR", ":7777")
	t.Setenv("GOFF_STUDIO_ENVIRONMENTS", "fromenv")

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Server.Addr != ":7777" {
		t.Errorf("addr = %q, want the env var to win", cfg.Server.Addr)
	}
	if len(cfg.Environments) != 1 || cfg.Environments[0].Name != "fromenv" {
		t.Errorf("environments = %+v; an env list replaces the file's, it does not merge", cfg.Environments)
	}
	if len(cfg.Permissions) != 1 || cfg.Permissions[0].Group != "file-group" {
		t.Errorf("permissions = %+v; an unset env var must leave the file alone", cfg.Permissions)
	}
}
