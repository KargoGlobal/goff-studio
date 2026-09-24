package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	gffprovider "github.com/open-feature/go-sdk-contrib/providers/go-feature-flag-in-process/pkg"
	"github.com/open-feature/go-sdk/openfeature"
	ffclient "github.com/thomaspoignant/go-feature-flag"
	"github.com/thomaspoignant/go-feature-flag/retriever"
	"github.com/thomaspoignant/go-feature-flag/retriever/githubretriever"
)

func main() {
	repoSlug := env("FLAGS_REPO", "your-org/flags-repo")
	environment := env("FLAGS_ENV", "production")
	token := os.Getenv("GITHUB_TOKEN")

	provider, err := gffprovider.NewProvider(gffprovider.ProviderOptions{
		GOFeatureFlagConfig: &ffclient.Config{
			PollingInterval: 30 * time.Second,
			Context:         context.Background(),
			Retrievers: []retriever.Retriever{
				&githubretriever.Retriever{
					RepositorySlug: repoSlug,
					FilePath:       environment + "/payments.goff.yaml",
					GithubToken:    token,
					Timeout:        10 * time.Second,
				},
				&githubretriever.Retriever{
					RepositorySlug: repoSlug,
					FilePath:       environment + "/growth.goff.yaml",
					GithubToken:    token,
					Timeout:        10 * time.Second,
				},
			},
			PersistentFlagConfigurationFile: "/tmp/goff-studio-example-last-good.yaml",
			StartWithRetrieverError:         true,
		},
	})
	if err != nil {
		log.Fatalf("provider: %v", err)
	}

	if err := openfeature.SetProviderAndWait(provider); err != nil {
		log.Fatalf("openfeature: %v", err)
	}
	defer provider.Shutdown()

	client := openfeature.NewClient("example")
	ctx := context.Background()

	log.Printf("watching %s (%s). change a flag in Studio and this loop picks it up.", repoSlug, environment)

	for {
		evalCtx := openfeature.NewEvaluationContext("user-42", map[string]any{
			"tier":       "gold",
			"account_id": "42",
		})

		details, err := client.BooleanValueDetails(ctx, "new-checkout", false, evalCtx)
		if err != nil {
			log.Printf("evaluation error: %v", err)
		} else {
			fmt.Printf("%s  new-checkout=%v  variant=%q  reason=%v\n",
				time.Now().Format(time.TimeOnly), details.Value, details.Variant, details.Reason)
		}

		time.Sleep(10 * time.Second)
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
