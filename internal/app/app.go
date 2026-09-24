package app

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/go-feature-flag/studio/internal/auth"
	"github.com/go-feature-flag/studio/internal/config"
	"github.com/go-feature-flag/studio/internal/githubapp"
	"github.com/go-feature-flag/studio/internal/permissions"
	"github.com/go-feature-flag/studio/internal/server"
	"github.com/go-feature-flag/studio/internal/storage"
)

func Run(dist embed.FS) {
	configPath := flag.String("config", "studio.yaml", "path to the config file")
	storageWait := flag.Duration("storage-timeout", 2*time.Minute, "how long to keep retrying the storage backend at startup")
	oidcWait := flag.Duration("oidc-discovery-timeout", 2*time.Minute, "how long to retry OIDC discovery before giving up at startup")
	flag.Parse()

	load := config.Load
	if !isFlagSet("config") {
		load = config.LoadOptional
	}

	cfg, err := load(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	for _, w := range cfg.Warnings() {
		log.Printf("WARNING: %s", w)
	}

	repo, err := openStorage(cfg)
	if err != nil {
		log.Fatalf("storage: %v", err)
	}

	perms, err := permissions.New(cfg.Permissions)
	if err != nil {
		log.Fatalf("permissions: %v", err)
	}

	sealer, err := auth.NewSealer(cfg.Server.SessionSecret, cfg.Server.SecureCookies)
	if err != nil {
		log.Fatalf("session: %v", err)
	}

	if err := checkStorage(cfg, repo, *storageWait); err != nil {
		log.Fatalf("storage: %v", err)
	}

	oidcClient, err := discoverOIDC(cfg, *oidcWait)
	if err != nil {
		log.Fatalf("oidc: %v", err)
	}

	assets, err := fs.Sub(dist, "dist")
	if err != nil {
		log.Fatalf("assets: %v", err)
	}

	svc := server.NewService(cfg, repo, perms)
	srv := server.New(cfg, svc, oidcClient, sealer, assets)

	if cfg.UsesGitHub() {
		log.Printf("GO Feature Flag Studio listening on %s, %s backend: %s/%s on %s",
			cfg.Server.Addr, repo.Name(), cfg.GitHub.Owner, cfg.GitHub.Repo, cfg.GitHub.Branch)
	} else {
		log.Printf("GO Feature Flag Studio listening on %s, %s backend: %s",
			cfg.Server.Addr, repo.Name(), cfg.Storage.Path)
	}

	httpServer := &http.Server{
		Addr:              cfg.Server.Addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	if err := httpServer.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

func discoverOIDC(cfg *config.Config, budget time.Duration) (*auth.OIDC, error) {
	oidcCfg := auth.OIDCConfig{
		IssuerURL:    cfg.OIDC.IssuerURL,
		ClientID:     cfg.OIDC.ClientID,
		ClientSecret: cfg.OIDC.ClientSecret,
		RedirectURL:  cfg.RedirectURL(),
		GroupsClaim:  cfg.OIDC.GroupsClaim,
	}

	deadline := time.Now().Add(budget)
	backoff := time.Second
	for attempt := 1; ; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		client, err := auth.NewOIDC(ctx, oidcCfg)
		cancel()
		if err == nil {
			return client, nil
		}
		if time.Now().Add(backoff).After(deadline) {
			return nil, fmt.Errorf("%w (issuer %s, gave up after %d attempts over %s; check oidc.issuerURL and that the provider is reachable from this pod)",
				err, cfg.OIDC.IssuerURL, attempt, budget)
		}
		log.Printf("WARNING: oidc discovery attempt %d against %s failed: %v; retrying in %s", attempt, cfg.OIDC.IssuerURL, err, backoff)
		time.Sleep(backoff)
		if backoff < 15*time.Second {
			backoff *= 2
		}
	}
}

func checkStorage(cfg *config.Config, repo storage.Backend, budget time.Duration) error {
	target := cfg.Storage.Path
	hint := "check that the path exists and is writable"
	switch {
	case cfg.UsesGitHub():
		target = fmt.Sprintf("%s/%s on %s", cfg.GitHub.Owner, cfg.GitHub.Repo, cfg.GitHub.Branch)
		hint = "check the credentials, that the repository exists, and that the GitHub App is installed on it"
	case cfg.Storage.Bucket != "":
		target = fmt.Sprintf("s3://%s/%s", cfg.Storage.Bucket, cfg.Storage.Prefix)
		hint = "check the bucket name, the region, and that this pod's credentials can list and write it"
	}

	deadline := time.Now().Add(budget)
	backoff := time.Second
	for attempt := 1; ; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		err := storage.Check(ctx, repo)
		cancel()
		if err == nil {
			log.Printf("storage: reached %s", target)
			return nil
		}
		if time.Now().Add(backoff).After(deadline) {
			return fmt.Errorf("%w (%s, gave up after %d attempts over %s; %s)",
				err, target, attempt, budget, hint)
		}
		log.Printf("WARNING: storage check attempt %d against %s failed: %v; retrying in %s", attempt, target, err, backoff)
		time.Sleep(backoff)
		if backoff < 15*time.Second {
			backoff *= 2
		}
	}
}

func openStorage(cfg *config.Config) (storage.Backend, error) {
	if !cfg.UsesGitHub() {
		return storage.Open(storage.Settings{
			Backend: cfg.Storage.Backend,
			Path:    cfg.Storage.Path,
			Bucket:  cfg.Storage.Bucket,
			Region:  cfg.Storage.Region,
			Prefix:  cfg.Storage.Prefix,
			Options: cfg.Storage.Options,
		})
	}

	if cfg.UsingDevToken() {
		log.Println("WARNING: using a personal access token. This is for local development only; production should use a GitHub App.")
	}

	tokens, err := tokenSource(cfg)
	if err != nil {
		return nil, fmt.Errorf("github credentials: %w", err)
	}

	return storage.NewGitHubBackend(githubapp.New(githubapp.Config{
		APIBase: os.Getenv("GOFF_STUDIO_GITHUB_API_BASE"),
		Owner:   cfg.GitHub.Owner,
		Repo:    cfg.GitHub.Repo,
		Branch:  cfg.GitHub.Branch,
	}, tokens, nil)), nil
}

func tokenSource(cfg *config.Config) (githubapp.TokenSource, error) {
	if cfg.UsingDevToken() {
		return githubapp.StaticToken(cfg.GitHub.DevToken), nil
	}
	return githubapp.NewInstallationTokens("", cfg.GitHub.AppID, cfg.GitHub.InstallationID, cfg.GitHub.PrivateKeyPath, nil, nil)
}

func isFlagSet(name string) bool {
	found := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}
