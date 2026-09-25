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
	envDiscoverEnvs   = "GOFF_STUDIO_DISCOVER_ENVIRONMENTS"
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
	envEnvironments   = "GOFF_STUDIO_ENVIRONMENTS"
	envPermissions    = "GOFF_STUDIO_PERMISSIONS"
	envAnalysisURL    = "GOFF_STUDIO_ANALYSIS_BASE_URL"
	envAnalysisToken  = "GOFF_STUDIO_ANALYSIS_TOKEN"
)

var environmentName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

type Environment struct {
	Name      string `yaml:"name" json:"name"`
	Display   string `yaml:"display" json:"display"`
	Protected bool   `yaml:"protected" json:"protected"`
	Order     int    `yaml:"order" json:"order"`
}

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

// Analysis points at the service that computes experiment results. Unset,
// Studio serves clearly labelled sample results instead.
type Analysis struct {
	BaseURL string `yaml:"baseURL"`
	Token   string `yaml:"token"`
}

type Config struct {
	Server               Server             `yaml:"server"`
	OIDC                 OIDC               `yaml:"oidc"`
	GitHub               GitHub             `yaml:"github"`
	Storage              Storage            `yaml:"storage"`
	Environments         []Environment      `yaml:"environments"`
	DiscoverEnvironments bool               `yaml:"discoverEnvironments"`
	Permissions          []permissions.Rule `yaml:"permissions"`
	PollSeconds          int                `yaml:"expectedPollSeconds"`
	Analysis             Analysis           `yaml:"analysis"`

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
		case os.IsNotExist(err) && optional:
			missingFile = true
		default:
			return nil, fmt.Errorf("reading config: %w", err)
		}
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
	boolean(envDiscoverEnvs, &c.DiscoverEnvironments)
	str(envStorage, &c.Storage.Backend)
	str(envStoragePath, &c.Storage.Path)
	str(envStorageBucket, &c.Storage.Bucket)
	str(envStorageRegion, &c.Storage.Region)
	str(envStoragePrefix, &c.Storage.Prefix)
	yamlList(envStorageOptions, &c.Storage.Options)

	if v := strings.TrimSpace(os.Getenv(envEnvironments)); v != "" {
		if strings.HasPrefix(v, "[") || strings.Contains(v, "name:") {
			yamlList(envEnvironments, &c.Environments)
		} else {
			c.Environments = nil
			for _, name := range strings.Split(v, ",") {
				if name = strings.TrimSpace(name); name != "" {
					c.Environments = append(c.Environments, Environment{Name: name})
				}
			}
		}
	}
	yamlList(envPermissions, &c.Permissions)

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

	str(envAnalysisURL, &c.Analysis.BaseURL)
	str(envAnalysisToken, &c.Analysis.Token)

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
	if err := c.validateAnalysis(); err != nil {
		return err
	}
	return c.validatePermissions()
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
	backend := strings.ToLower(strings.TrimSpace(c.Storage.Backend))
	if backend == "" {
		backend = "github"
		c.Storage.Backend = backend
	}

	switch backend {
	case "github":
		return c.validateGitHub()
	case "file":
		if strings.TrimSpace(c.Storage.Path) == "" {
			return fieldErr("storage.path", envStoragePath, "is required when storage.backend is file; it is the directory holding your environment directories")
		}
		info, err := os.Stat(c.Storage.Path)
		if err != nil {
			return fieldErr("storage.path", envStoragePath, fmt.Sprintf("%q cannot be read: %v", c.Storage.Path, err))
		}
		if !info.IsDir() {
			return fieldErr("storage.path", envStoragePath, fmt.Sprintf("%q is not a directory", c.Storage.Path))
		}
		c.warnf("storage.backend is file, so Studio writes flags straight to disk with no history, attribution or review; use the github backend for anything shared")
		return nil
	case "s3":
		if strings.TrimSpace(c.Storage.Bucket) == "" {
			return fieldErr("storage.bucket", envStorageBucket, "is required when storage.backend is s3")
		}
		if !storage.Registered("s3") {
			return fieldErr("storage.backend", envStorage, "s3 is not compiled into this binary; use an image built with the s3 backend, or pick one of: "+strings.Join(storage.Available(), ", "))
		}
		c.warnf("storage.backend is s3, so changes have no attribution and no review; Studio's permission config is the only control over who may change a flag")
		return nil
	default:
		if storage.Registered(backend) {
			return nil
		}
		return fieldErr("storage.backend", envStorage, fmt.Sprintf("%q is not compiled into this binary; available: %s", c.Storage.Backend, strings.Join(storage.Available(), ", ")))
	}
}

func (c *Config) UsesGitHub() bool {
	backend := strings.ToLower(strings.TrimSpace(c.Storage.Backend))
	return backend == "" || backend == "github"
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
	if len(c.Environments) == 0 && !c.DiscoverEnvironments {
		return fmt.Errorf("at least one environment is required; each entry names a top-level directory in %s/%s", c.GitHub.Owner, c.GitHub.Repo)
	}

	seen := map[string]bool{}
	for i := range c.Environments {
		env := &c.Environments[i]
		if env.Name == "" {
			return fmt.Errorf("environments[%d] has no name; each entry needs the directory name it maps to in the flags repository", i)
		}
		if !environmentName.MatchString(env.Name) {
			return fmt.Errorf("environments[%d] name %q is not usable as a directory or URL segment; use letters, digits, dots, dashes and underscores", i, env.Name)
		}
		if seen[env.Name] {
			return fmt.Errorf("environments[%d] repeats the name %q; environment names must be unique", i, env.Name)
		}
		seen[env.Name] = true
		if env.Display == "" {
			env.Display = strings.ToUpper(env.Name[:1]) + env.Name[1:]
		}
		if env.Order == 0 {
			env.Order = i + 1
		}
	}
	return nil
}

func (c *Config) validateAnalysis() error {
	if strings.TrimSpace(c.Analysis.BaseURL) == "" {
		if c.Analysis.Token != "" {
			c.warnf("analysis.token is set but analysis.baseURL is not (%s), so the token is unused and experiments show sample results", envAnalysisURL)
		}
		return nil
	}
	parsed, err := absoluteURL("analysis.baseURL", envAnalysisURL, c.Analysis.BaseURL)
	if err != nil {
		return err
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return fieldErr("analysis.baseURL", envAnalysisURL,
			fmt.Sprintf("must not contain a query string or fragment, got %q; Studio appends /v1/experiments/...", c.Analysis.BaseURL))
	}
	if parsed.Scheme == "http" && !isLoopbackHost(parsed.Hostname()) && c.Analysis.Token != "" {
		c.warnf("analysis.baseURL %q is not https, so analysis.token is sent in plaintext (%s)", c.Analysis.BaseURL, envAnalysisURL)
	}
	return nil
}

func (c *Config) AnalysisConfigured() bool {
	return strings.TrimSpace(c.Analysis.BaseURL) != ""
}

func (c *Config) validatePermissions() error {
	if _, err := permissions.New(c.Permissions); err != nil {
		return fmt.Errorf("permissions: %w", err)
	}

	if len(c.Permissions) == 0 {
		c.warnf("permissions is empty, so Studio will deny every request; add at least one rule granting a group access")
		return nil
	}

	known := map[string]bool{}
	for _, env := range c.Environments {
		known[env.Name] = true
	}
	for i, rule := range c.Permissions {
		for _, name := range rule.Environments {
			if !known[name] {
				c.warnf("permissions[%d] for group %q references environment %q, which is not in environments %v, so that rule can never match",
					i, rule.Group, name, c.EnvironmentNames())
			}
		}
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

func (c *Config) EnvironmentNames() []string {
	out := make([]string, 0, len(c.Environments))
	for _, e := range c.Environments {
		out = append(out, e.Name)
	}
	return out
}

func (c *Config) Environment(name string) (Environment, bool) {
	for _, e := range c.Environments {
		if e.Name == name {
			return e, true
		}
	}
	return Environment{}, false
}

func (c *Config) RedirectURL() string {
	return strings.TrimRight(c.Server.BaseURL, "/") + "/auth/callback"
}
