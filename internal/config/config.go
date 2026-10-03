package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/go-feature-flag/studio/internal/permissions"
	"github.com/go-feature-flag/studio/internal/storage"
	"gopkg.in/yaml.v3"
)

const (
	envAddr           = "GOFF_STUDIO_ADDR"
	envBaseURL        = "GOFF_STUDIO_BASE_URL"
	envSessionSecret  = "GOFF_STUDIO_SESSION_SECRET"
	envSecureCookies  = "GOFF_STUDIO_SECURE_COOKIES"
	envStorage        = "GOFF_STUDIO_STORAGE"
	envStoragePath    = "GOFF_STUDIO_STORAGE_PATH"
	envStorageBucket  = "GOFF_STUDIO_STORAGE_BUCKET"
	envStorageRegion  = "GOFF_STUDIO_STORAGE_REGION"
	envStoragePrefix  = "GOFF_STUDIO_STORAGE_PREFIX"
	envStorageOptions = "GOFF_STUDIO_STORAGE_OPTIONS"
	envOIDCIssuerURL  = "GOFF_STUDIO_OIDC_ISSUER_URL"
	envOIDCClientID   = "GOFF_STUDIO_OIDC_CLIENT_ID"
	envOIDCSecret     = "GOFF_STUDIO_OIDC_CLIENT_SECRET"
	envOIDCGroups     = "GOFF_STUDIO_OIDC_GROUPS_CLAIM"
	envGitHubOwner    = "GOFF_STUDIO_GITHUB_OWNER"
	envGitHubRepo     = "GOFF_STUDIO_GITHUB_REPO"
	envGitHubBranch   = "GOFF_STUDIO_GITHUB_BRANCH"
	envGitHubAppID    = "GOFF_STUDIO_GITHUB_APP_ID"
	envGitHubInstall  = "GOFF_STUDIO_GITHUB_INSTALLATION_ID"
	envGitHubKeyPath  = "GOFF_STUDIO_GITHUB_PRIVATE_KEY_PATH"
	envGitHubDevToken = "GOFF_STUDIO_GITHUB_DEV_TOKEN"
	envPollSeconds    = "GOFF_STUDIO_EXPECTED_POLL_SECONDS"
	envProtectedEnvs  = "GOFF_STUDIO_PROTECTED_ENVIRONMENTS"
	envPermissions    = "GOFF_STUDIO_PERMISSIONS"
	envLayout         = "GOFF_STUDIO_LAYOUT"
	envAPITokens      = "GOFF_STUDIO_API_TOKENS"
	envNotifications  = "GOFF_STUDIO_NOTIFICATIONS"
	envMCPEnabled     = "GOFF_STUDIO_MCP_ENABLED"

	legacyEnvEnvironments = "GOFF_STUDIO_ENVIRONMENTS"
	legacyEnvDiscoverEnvs = "GOFF_STUDIO_DISCOVER_ENVIRONMENTS"
)

var environmentName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

type Server struct {
	Addr          string `yaml:"addr"`
	BaseURL       string `yaml:"baseURL"`
	SessionSecret string `yaml:"sessionSecret"`
	SecureCookies bool   `yaml:"secureCookies"`
}

type OIDC struct {
	IssuerURL    string `yaml:"issuerURL"`
	ClientID     string `yaml:"clientID"`
	ClientSecret string `yaml:"clientSecret"`
	GroupsClaim  string `yaml:"groupsClaim"`
}

type Storage struct {
	Kind    string            `yaml:"kind"`
	Backend string            `yaml:"backend"`
	Path    string            `yaml:"path"`
	Bucket  string            `yaml:"bucket"`
	Region  string            `yaml:"region"`
	Prefix  string            `yaml:"prefix"`
	Options map[string]string `yaml:"options"`
}

type GitHub struct {
	Owner          string `yaml:"owner"`
	Repo           string `yaml:"repo"`
	Branch         string `yaml:"branch"`
	AppID          string `yaml:"appID"`
	InstallationID string `yaml:"installationID"`
	PrivateKeyPath string `yaml:"privateKeyPath"`
	DevToken       string `yaml:"devToken"`
}

// APIToken is a machine identity; only the SHA-256 of the token is configured, never the token.
type APIToken struct {
	Name   string   `yaml:"name"`
	SHA256 string   `yaml:"sha256"`
	Groups []string `yaml:"groups"`
	Email  string   `yaml:"email"`
}

type Notification struct {
	URL          string   `yaml:"url"`
	Format       string   `yaml:"format"`
	Environments []string `yaml:"environments"`
	Secret       string   `yaml:"secret"`
}

type MCP struct {
	Enabled bool `yaml:"enabled"`
}

const (
	NotifyJSON  = "json"
	NotifySlack = "slack"
)

type Config struct {
	Server                Server             `yaml:"server"`
	OIDC                  OIDC               `yaml:"oidc"`
	GitHub                GitHub             `yaml:"github"`
	Storage               Storage            `yaml:"storage"`
	ProtectedEnvironments []string           `yaml:"protectedEnvironments"`
	Permissions           []permissions.Rule `yaml:"permissions"`
	PollSeconds           int                `yaml:"expectedPollSeconds"`
	Layout                string             `yaml:"layout"`
	APITokens             []APIToken         `yaml:"apiTokens"`
	Notifications         []Notification     `yaml:"notifications"`
	MCP                   MCP                `yaml:"mcp"`

	warnings []string
}

func Load(path string) (*Config, error) { return load(path, false) }

// LoadOptional tolerates a missing file so a container can run on env vars alone.
func LoadOptional(path string) (*Config, error) { return load(path, true) }

func load(path string, optional bool) (*Config, error) {
	var missingFile bool
	cfg := &Config{
		Server:      Server{Addr: ":8080", BaseURL: "http://localhost:8080"},
		GitHub:      GitHub{Branch: "main"},
		PollSeconds: 60,
	}

	if path != "" {
		raw, err := os.ReadFile(path)
		switch {
		case err == nil:
			if err := yaml.Unmarshal(raw, cfg); err != nil {
				return nil, fmt.Errorf("parsing config: %w", err)
			}
			if err := legacyFileKeys(raw); err != nil {
				return nil, err
			}
		case os.IsNotExist(err) && optional:
			missingFile = true
		default:
			return nil, fmt.Errorf("reading config: %w", err)
		}
	}

	if err := legacyEnvVars(); err != nil {
		return nil, err
	}
	if err := cfg.applyEnv(); err != nil {
		return nil, err
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if missingFile {
		cfg.warnf("no config file at %s, so Studio is configured entirely from GOFF_STUDIO_* variables", path)
	}
	return cfg, nil
}

func (c *Config) applyEnv() error {
	var bad []string

	str := func(key string, target *string) {
		if v := os.Getenv(key); v != "" {
			*target = v
		}
	}
	boolean := func(key string, target *bool) {
		v := os.Getenv(key)
		if v == "" {
			return
		}
		parsed, err := strconv.ParseBool(v)
		if err != nil {
			bad = append(bad, fmt.Sprintf("%s=%q is not a boolean; use true or false", key, v))
			return
		}
		*target = parsed
	}
	integer := func(key string, target *int) {
		v := os.Getenv(key)
		if v == "" {
			return
		}
		parsed, err := strconv.Atoi(v)
		if err != nil {
			bad = append(bad, fmt.Sprintf("%s=%q is not a whole number", key, v))
			return
		}
		*target = parsed
	}

	yamlList := func(key string, target any) {
		v := strings.TrimSpace(os.Getenv(key))
		if v == "" {
			return
		}
		if err := yaml.Unmarshal([]byte(v), target); err != nil {
			bad = append(bad, fmt.Sprintf("%s is not valid YAML or JSON: %v", key, err))
		}
	}

	str(envAddr, &c.Server.Addr)
	str(envBaseURL, &c.Server.BaseURL)
	str(envSessionSecret, &c.Server.SessionSecret)
	boolean(envSecureCookies, &c.Server.SecureCookies)
	if v := os.Getenv(envStorage); v != "" {
		c.Storage.Kind, c.Storage.Backend = v, ""
	}
	str(envStoragePath, &c.Storage.Path)
	str(envStorageBucket, &c.Storage.Bucket)
	str(envStorageRegion, &c.Storage.Region)
	str(envStoragePrefix, &c.Storage.Prefix)
	yamlList(envStorageOptions, &c.Storage.Options)

	if v := strings.TrimSpace(os.Getenv(envProtectedEnvs)); v != "" {
		if strings.HasPrefix(v, "[") {
			c.ProtectedEnvironments = nil
			yamlList(envProtectedEnvs, &c.ProtectedEnvironments)
		} else {
			c.ProtectedEnvironments = splitList(v)
		}
	}
	yamlList(envPermissions, &c.Permissions)
	str(envLayout, &c.Layout)
	if os.Getenv(envAPITokens) != "" {
		c.APITokens = nil
		yamlList(envAPITokens, &c.APITokens)
	}
	if os.Getenv(envNotifications) != "" {
		c.Notifications = nil
		yamlList(envNotifications, &c.Notifications)
	}
	boolean(envMCPEnabled, &c.MCP.Enabled)

	str(envOIDCIssuerURL, &c.OIDC.IssuerURL)
	str(envOIDCClientID, &c.OIDC.ClientID)
	str(envOIDCSecret, &c.OIDC.ClientSecret)
	str(envOIDCGroups, &c.OIDC.GroupsClaim)

	str(envGitHubOwner, &c.GitHub.Owner)
	str(envGitHubRepo, &c.GitHub.Repo)
	str(envGitHubBranch, &c.GitHub.Branch)
	str(envGitHubAppID, &c.GitHub.AppID)
	str(envGitHubInstall, &c.GitHub.InstallationID)
	str(envGitHubKeyPath, &c.GitHub.PrivateKeyPath)
	str(envGitHubDevToken, &c.GitHub.DevToken)

	integer(envPollSeconds, &c.PollSeconds)

	if len(bad) > 0 {
		return fmt.Errorf("environment overrides: %s", strings.Join(bad, "; "))
	}
	return nil
}

func (c *Config) validate() error {
	c.warnings = nil

	if err := c.validateServer(); err != nil {
		return err
	}
	if err := c.validateOIDC(); err != nil {
		return err
	}
	if err := c.validateStorage(); err != nil {
		return err
	}
	if c.PollSeconds <= 0 {
		return fieldErr("expectedPollSeconds", envPollSeconds,
			fmt.Sprintf("must be a positive number of seconds, got %d; it is shown to users as \"live in apps within about N seconds\"", c.PollSeconds))
	}
	if err := c.validateEnvironments(); err != nil {
		return err
	}
	if err := c.validateLayout(); err != nil {
		return err
	}
	if err := c.validateAPITokens(); err != nil {
		return err
	}
	if err := c.validateNotifications(); err != nil {
		return err
	}
	return c.validatePermissions()
}

var tokenName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

func (c *Config) validateAPITokens() error {
	seenNames, seenHashes := map[string]bool{}, map[string]bool{}
	for i := range c.APITokens {
		t := &c.APITokens[i]
		key := fmt.Sprintf("apiTokens[%d]", i)
		t.Name = strings.TrimSpace(t.Name)
		if !tokenName.MatchString(t.Name) || len(t.Name) > 64 {
			return fieldErr(key+".name", envAPITokens,
				fmt.Sprintf("%q must be 1 to 64 letters, digits, dots, dashes or underscores; it names the token in history and notifications", t.Name))
		}
		if seenNames[t.Name] {
			return fieldErr(key+".name", envAPITokens, fmt.Sprintf("repeats %q; give each token its own name", t.Name))
		}
		seenNames[t.Name] = true

		t.SHA256 = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(t.SHA256), "sha256:"))
		if !isSHA256Hex(t.SHA256) {
			return fieldErr(key+".sha256", envAPITokens,
				"must be the 64-character hex SHA-256 of the token, never the token itself; generate a token with openssl rand -hex 32 and hash it with sha256sum")
		}
		if seenHashes[t.SHA256] {
			return fieldErr(key+".sha256", envAPITokens, fmt.Sprintf("is shared with another token; %q needs a token of its own", t.Name))
		}
		seenHashes[t.SHA256] = true

		if len(t.Groups) == 0 {
			return fieldErr(key+".groups", envAPITokens,
				fmt.Sprintf("is empty for %q; list the permission groups the token acts as, otherwise it can do nothing", t.Name))
		}
		for _, g := range t.Groups {
			if strings.TrimSpace(g) == "" || g == "*" {
				return fieldErr(key+".groups", envAPITokens, fmt.Sprintf("for %q must name real groups, not %q", t.Name, g))
			}
		}
		if t.Email != "" && (hasLineBreak(t.Email) || !strings.Contains(t.Email, "@")) {
			return fieldErr(key+".email", envAPITokens, fmt.Sprintf("for %q must be a single email address, got %q", t.Name, t.Email))
		}
	}
	if c.MCP.Enabled && len(c.APITokens) == 0 {
		c.warnf("mcp.enabled is true but no apiTokens are configured; MCP clients sign in with an API token, so none can connect (%s)", envAPITokens)
	}
	return nil
}

func (c *Config) validateNotifications() error {
	for i := range c.Notifications {
		n := &c.Notifications[i]
		key := fmt.Sprintf("notifications[%d]", i)
		if strings.TrimSpace(n.URL) == "" {
			return fieldErr(key+".url", envNotifications, "is required; it is where Studio posts each flag change")
		}
		target, err := absoluteURL(key+".url", envNotifications, n.URL)
		if err != nil {
			return err
		}
		if target.Scheme != "https" && !isLoopbackHost(target.Hostname()) {
			c.warnf("%s.url is not https, so flag changes are sent in plaintext (%s)", key, envNotifications)
		}
		n.Format = strings.ToLower(strings.TrimSpace(n.Format))
		if n.Format == "" {
			n.Format = NotifyJSON
		}
		if n.Format != NotifyJSON && n.Format != NotifySlack {
			return fieldErr(key+".format", envNotifications, fmt.Sprintf("must be %q or %q, got %q", NotifyJSON, NotifySlack, n.Format))
		}
		for _, env := range n.Environments {
			if env != "*" && !environmentName.MatchString(env) {
				return fieldErr(key+".environments", envNotifications, fmt.Sprintf("%q is not usable as a folder name", env))
			}
		}
	}
	return nil
}

func isSHA256Hex(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func hasLineBreak(s string) bool {
	return strings.ContainsAny(s, "\r\n")
}

// NotifiesFor reports whether a notification hook covers the environment; no list means all of them.
func (n Notification) NotifiesFor(environment string) bool {
	if len(n.Environments) == 0 {
		return true
	}
	for _, e := range n.Environments {
		if e == "*" || e == environment {
			return true
		}
	}
	return false
}

func (c *Config) validateServer() error {
	if len(c.Server.SessionSecret) < 16 {
		return fieldErr("server.sessionSecret", envSessionSecret,
			fmt.Sprintf("must be at least 16 characters, got %d; generate one with: openssl rand -hex 32", len(c.Server.SessionSecret)))
	}

	if strings.TrimSpace(c.Server.Addr) == "" {
		return fieldErr("server.addr", envAddr, `is required; use a host:port listen address like ":8080"`)
	}
	_, port, err := net.SplitHostPort(c.Server.Addr)
	if err != nil {
		return fieldErr("server.addr", envAddr,
			fmt.Sprintf("must be a host:port listen address like \":8080\", got %q", c.Server.Addr))
	}
	//nolint:noctx // named ports like ":http" are resolved from /etc/services, no network call
	if _, err := net.LookupPort("tcp", port); err != nil {
		return fieldErr("server.addr", envAddr,
			fmt.Sprintf("has an invalid port %q; use a name from /etc/services or a number between 0 and 65535", port))
	}

	if strings.TrimSpace(c.Server.BaseURL) == "" {
		return fieldErr("server.baseURL", envBaseURL, "is required; the OIDC redirect is <baseURL>/auth/callback")
	}
	base, err := absoluteURL("server.baseURL", envBaseURL, c.Server.BaseURL)
	if err != nil {
		return err
	}
	if base.RawQuery != "" || base.Fragment != "" {
		return fieldErr("server.baseURL", envBaseURL,
			fmt.Sprintf("must not contain a query string or fragment, got %q; the OIDC redirect is <baseURL>/auth/callback", c.Server.BaseURL))
	}

	loopback := isLoopbackHost(base.Hostname())
	if base.Scheme == "https" && !c.Server.SecureCookies {
		c.warnf("server.baseURL is https but server.secureCookies is false (%s), so session cookies will be sent without the Secure attribute", envSecureCookies)
	}
	if base.Scheme == "http" && !loopback {
		c.warnf("server.baseURL %q is not https (%s); OIDC providers usually reject plaintext redirect URIs", c.Server.BaseURL, envBaseURL)
		if c.Server.SecureCookies {
			c.warnf("server.secureCookies is true but server.baseURL is http, so browsers will discard the session cookie and login will loop (%s)", envSecureCookies)
		}
	}
	return nil
}

func (c *Config) validateOIDC() error {
	if strings.TrimSpace(c.OIDC.IssuerURL) == "" {
		return fieldErr("oidc.issuerURL", envOIDCIssuerURL,
			"is required; Studio authenticates every request through OIDC, e.g. https://your-org.okta.com/oauth2/default")
	}
	issuer, err := absoluteURL("oidc.issuerURL", envOIDCIssuerURL, c.OIDC.IssuerURL)
	if err != nil {
		return err
	}
	if issuer.Scheme != "https" && !isLoopbackHost(issuer.Hostname()) {
		return fieldErr("oidc.issuerURL", envOIDCIssuerURL,
			fmt.Sprintf("must use https (http is allowed only for localhost), got %q", c.OIDC.IssuerURL))
	}
	if issuer.RawQuery != "" || issuer.Fragment != "" {
		return fieldErr("oidc.issuerURL", envOIDCIssuerURL,
			fmt.Sprintf("must not contain a query string or fragment, got %q; discovery appends /.well-known/openid-configuration to it", c.OIDC.IssuerURL))
	}

	if strings.TrimSpace(c.OIDC.ClientID) == "" {
		return fieldErr("oidc.clientID", envOIDCClientID, "is required; it is the client ID of the OIDC application you registered for Studio")
	}
	if c.OIDC.ClientSecret == "" {
		return fieldErr("oidc.clientSecret", envOIDCSecret, "is required; Studio exchanges the authorization code as a confidential client")
	}
	return nil
}

func (c *Config) validateStorage() error {
	kind, err := c.resolveKind()
	if err != nil {
		return err
	}
	c.Storage.Kind, c.Storage.Backend = kind, ""

	switch kind {
	case storage.KindGitHub:
		return c.validateGitHub()
	case storage.KindFile:
		if strings.TrimSpace(c.Storage.Path) == "" {
			return fieldErr("storage.path", envStoragePath, "is required when storage.kind is file; it is the directory holding your environment directories")
		}
		info, err := os.Stat(c.Storage.Path)
		if err != nil {
			return fieldErr("storage.path", envStoragePath, fmt.Sprintf("%q cannot be read: %v", c.Storage.Path, err))
		}
		if !info.IsDir() {
			return fieldErr("storage.path", envStoragePath, fmt.Sprintf("%q is not a directory", c.Storage.Path))
		}
		c.warnf("storage.kind is file, so Studio writes flags straight to disk with no history, attribution or review; use the github kind for anything shared")
		return nil
	case storage.KindS3:
		return c.validateObjectStore(kind, "", "bucket versioning")
	case storage.KindGoogleStorage:
		return c.validateObjectStore(kind, "", "object versioning")
	case storage.KindAzureBlobStorage:
		return c.validateObjectStore(kind, "; it is the container name", "blob versioning")
	case storage.KindConfigMap:
		if err := c.requireCompiledIn(kind); err != nil {
			return err
		}
		c.warnf("storage.kind is configmap, so changes have no history, attribution or review; Studio's permission config is the only control over who may change a flag")
		return nil
	default:
		if storage.Registered(kind) {
			return nil
		}
		return fieldErr("storage.kind", envStorage, fmt.Sprintf("%q is not compiled into this binary; available: %s", kind, strings.Join(storage.Available(), ", ")))
	}
}

func (c *Config) resolveKind() (string, error) {
	kind := strings.TrimSpace(c.Storage.Kind)
	if legacy := strings.TrimSpace(c.Storage.Backend); legacy != "" {
		if kind != "" && storage.Canonical(kind) != storage.Canonical(legacy) {
			return "", fmt.Errorf("storage.kind is %q but storage.backend is %q; storage.backend is the old name for storage.kind, so remove it", kind, legacy)
		}
		c.warnf("storage.backend is deprecated; rename it to storage.kind")
		if kind == "" {
			kind = legacy
		}
	}
	if kind == "" {
		return storage.KindGitHub, nil
	}
	if renamed, ok := storage.LegacyKind(kind); ok {
		c.warnf("storage kind %q is deprecated; use %q, the name GO Feature Flag's retriever uses", kind, renamed)
	}
	return storage.Canonical(kind), nil
}

func (c *Config) validateObjectStore(kind, bucketHint, versioning string) error {
	if strings.TrimSpace(c.Storage.Bucket) == "" {
		return fieldErr("storage.bucket", envStorageBucket, "is required when storage.kind is "+kind+bucketHint)
	}
	if err := c.requireCompiledIn(kind); err != nil {
		return err
	}
	c.warnf("storage.kind is %s, so changes have no review; Studio's permission config is the only control over who may change a flag, and history and attribution need %s", kind, versioning)
	return nil
}

func (c *Config) requireCompiledIn(kind string) error {
	if storage.Registered(kind) {
		return nil
	}
	return fieldErr("storage.kind", envStorage, fmt.Sprintf("%s is not compiled into this binary, which was built without it; use the standard image, or pick one of: %s", kind, strings.Join(storage.Available(), ", ")))
}

func (c *Config) UsesGitHub() bool {
	kind := strings.TrimSpace(c.Storage.Kind)
	if kind == "" {
		kind = strings.TrimSpace(c.Storage.Backend)
	}
	return kind == "" || storage.Canonical(kind) == storage.KindGitHub
}

func (c *Config) validateGitHub() error {
	if strings.TrimSpace(c.GitHub.Owner) == "" {
		return fieldErr("github.owner", envGitHubOwner, "is required; it is the organization or user that owns the flags repository")
	}
	if strings.Contains(c.GitHub.Owner, "/") {
		return fieldErr("github.owner", envGitHubOwner,
			fmt.Sprintf("must be the organization or user name only, not %q; put the repository in github.repo", c.GitHub.Owner))
	}
	if strings.TrimSpace(c.GitHub.Repo) == "" {
		return fieldErr("github.repo", envGitHubRepo, "is required; it is the repository holding the GO Feature Flag YAML files")
	}
	if strings.Contains(c.GitHub.Repo, "/") {
		return fieldErr("github.repo", envGitHubRepo,
			fmt.Sprintf("must be the repository name only, not %q; put the owner in github.owner", c.GitHub.Repo))
	}
	if strings.TrimSpace(c.GitHub.Branch) == "" {
		return fieldErr("github.branch", envGitHubBranch, `is required; it defaults to "main" unless you set it to an empty value`)
	}

	if c.GitHub.DevToken != "" {
		if c.GitHub.AppID != "" || c.GitHub.InstallationID != "" || c.GitHub.PrivateKeyPath != "" {
			c.warnf("github.devToken is set, so the GitHub App settings are ignored and every commit is attributed to the token owner (%s)", envGitHubDevToken)
		}
		return nil
	}

	var missing []string
	if c.GitHub.AppID == "" {
		missing = append(missing, fmt.Sprintf("github.appID (%s)", envGitHubAppID))
	}
	if c.GitHub.InstallationID == "" {
		missing = append(missing, fmt.Sprintf("github.installationID (%s)", envGitHubInstall))
	}
	if c.GitHub.PrivateKeyPath == "" {
		missing = append(missing, fmt.Sprintf("github.privateKeyPath (%s)", envGitHubKeyPath))
	}
	if len(missing) > 0 {
		return fmt.Errorf("github: %s must be set to authenticate as a GitHub App, or set github.devToken (%s) for local development",
			strings.Join(missing, ", "), envGitHubDevToken)
	}

	if !isDigits(c.GitHub.AppID) {
		return fieldErr("github.appID", envGitHubAppID,
			fmt.Sprintf("must be the numeric App ID, got %q; the client ID and the App slug will not work", c.GitHub.AppID))
	}
	if !isDigits(c.GitHub.InstallationID) {
		return fieldErr("github.installationID", envGitHubInstall,
			fmt.Sprintf("must be numeric, got %q; it is the last path segment of the App installation settings URL", c.GitHub.InstallationID))
	}

	if err := readable(c.GitHub.PrivateKeyPath); err != nil {
		return fieldErr("github.privateKeyPath", envGitHubKeyPath,
			fmt.Sprintf("%q %v; Studio needs the GitHub App key to read or write any flag, so check the secret is mounted there", c.GitHub.PrivateKeyPath, err))
	}
	return nil
}

func (c *Config) validateEnvironments() error {
	seen := map[string]bool{}
	for i, name := range c.ProtectedEnvironments {
		key := fmt.Sprintf("protectedEnvironments[%d]", i)
		if !environmentName.MatchString(name) {
			return fieldErr(key, envProtectedEnvs,
				fmt.Sprintf("%q is not usable as a folder name; use letters, digits, dots, dashes and underscores", name))
		}
		if seen[name] {
			return fieldErr(key, envProtectedEnvs, fmt.Sprintf("repeats %q; list each environment once", name))
		}
		seen[name] = true
	}
	return nil
}

const (
	LayoutTeamFiles  = "team-files"
	LayoutSingleFile = "single-file"
)

func (c *Config) validateLayout() error {
	layout := strings.ToLower(strings.TrimSpace(c.Layout))
	if layout == "" {
		layout = LayoutTeamFiles
	}
	if layout != LayoutTeamFiles && layout != LayoutSingleFile {
		return fieldErr("layout", envLayout, fmt.Sprintf("must be %q or %q, got %q", LayoutTeamFiles, LayoutSingleFile, c.Layout))
	}
	c.Layout = layout
	return nil
}

func (c *Config) SingleFile() bool {
	return c.Layout == LayoutSingleFile
}

func (c *Config) IsProtected(environment string) bool {
	for _, name := range c.ProtectedEnvironments {
		if name == environment {
			return true
		}
	}
	return false
}

func legacyFileKeys(raw []byte) error {
	var legacy struct {
		Environments         yaml.Node `yaml:"environments"`
		DiscoverEnvironments yaml.Node `yaml:"discoverEnvironments"`
	}
	if err := yaml.Unmarshal(raw, &legacy); err != nil {
		return fmt.Errorf("parsing config: %w", err)
	}
	if legacy.Environments.Kind != 0 {
		var old []struct {
			Name      string `yaml:"name"`
			Protected bool   `yaml:"protected"`
		}
		_ = legacy.Environments.Decode(&old)
		var protected []string
		for _, e := range old {
			if e.Protected && e.Name != "" {
				protected = append(protected, e.Name)
			}
		}
		if len(protected) == 0 {
			return fmt.Errorf("environments is no longer supported; Studio now finds environments from the folders in storage. Remove it, and list any that need typed confirmation in protectedEnvironments: [production]")
		}
		return fmt.Errorf("environments is no longer supported; Studio now finds environments from the folders in storage. Replace it with protectedEnvironments: [%s]",
			strings.Join(protected, ", "))
	}
	if legacy.DiscoverEnvironments.Kind != 0 {
		return fmt.Errorf("discoverEnvironments is no longer supported; Studio now always finds environments from the folders in storage. Remove it, and list any that need typed confirmation in protectedEnvironments: [production]")
	}
	return nil
}

func legacyEnvVars() error {
	if strings.TrimSpace(os.Getenv(legacyEnvEnvironments)) != "" {
		return fmt.Errorf("%s is no longer supported; Studio now finds environments from the folders in storage. Unset it and move protected ones to %s=production",
			legacyEnvEnvironments, envProtectedEnvs)
	}
	if strings.TrimSpace(os.Getenv(legacyEnvDiscoverEnvs)) != "" {
		return fmt.Errorf("%s is no longer supported; Studio now always finds environments from the folders in storage. Unset it, and list any that need typed confirmation in %s=production",
			legacyEnvDiscoverEnvs, envProtectedEnvs)
	}
	return nil
}

func splitList(v string) []string {
	var out []string
	for _, item := range strings.Split(v, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

func (c *Config) validatePermissions() error {
	if _, err := permissions.New(c.Permissions); err != nil {
		return fmt.Errorf("permissions: %w", err)
	}

	if len(c.Permissions) == 0 {
		c.warnf("permissions is empty, so Studio will deny every request; add at least one rule granting a group access")
	}
	return nil
}

func (c *Config) warnf(format string, args ...any) {
	c.warnings = append(c.warnings, fmt.Sprintf(format, args...))
}

func (c *Config) Warnings() []string {
	return c.warnings
}

func fieldErr(key, envVar, detail string) error {
	return fmt.Errorf("%s %s (override with %s)", key, detail, envVar)
}

func absoluteURL(key, envVar, raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, fieldErr(key, envVar, fmt.Sprintf("is not a valid URL (%v), got %q", err, raw))
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fieldErr(key, envVar, fmt.Sprintf("must be an absolute http(s) URL, got %q", raw))
	}
	if parsed.Host == "" {
		return nil, fieldErr(key, envVar, fmt.Sprintf("must include a host, got %q", raw))
	}
	return parsed, nil
}

func isLoopbackHost(host string) bool {
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func readable(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("does not exist")
		}
		if os.IsPermission(err) {
			return fmt.Errorf("is not readable by this process")
		}
		return err
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("is a directory, not a PEM file")
	}
	if info.Size() == 0 {
		return fmt.Errorf("is empty")
	}
	return nil
}

func (c *Config) UsingDevToken() bool {
	return c.GitHub.DevToken != ""
}

func (c *Config) RedirectURL() string {
	return strings.TrimRight(c.Server.BaseURL, "/") + "/auth/callback"
}
