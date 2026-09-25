package storage

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-feature-flag/studio/internal/githubapp"
)

type GitHubBackend struct {
	client *githubapp.Client
}

func NewGitHubBackend(client *githubapp.Client) *GitHubBackend {
	return &GitHubBackend{client: client}
}

func (g *GitHubBackend) Name() string { return "github" }

func (g *GitHubBackend) Capabilities() Capabilities {
	return Capabilities{History: true, Attribution: true, Review: true}
}

func (g *GitHubBackend) ReadFile(ctx context.Context, path string) (*File, error) {
	f, err := g.client.ReadFile(ctx, path)
	if err != nil {
		return nil, notFound(err)
	}
	return &File{Path: f.Path, Content: f.Content, Version: f.SHA}, nil
}

func (g *GitHubBackend) ListFiles(ctx context.Context, dir string) ([]string, error) {
	files, err := g.client.ListFiles(ctx, dir)
	return files, notFound(err)
}

func notFound(err error) error {
	if errors.Is(err, githubapp.ErrNotFound) {
		return missing{err}
	}
	return err
}

// missing keeps GitHub's message while matching ErrNotFound.
type missing struct{ err error }

func (m missing) Error() string        { return m.err.Error() }
func (m missing) Unwrap() error        { return m.err }
func (m missing) Is(target error) bool { return target == ErrNotFound }

func (g *GitHubBackend) ListDirectories(ctx context.Context, dir string) ([]string, error) {
	return g.client.ListDirectories(ctx, dir)
}

func (g *GitHubBackend) Write(ctx context.Context, op ChangeOp, who Identity) (*Result, error) {
	result, err := g.client.Commit(ctx, githubapp.ChangeOp{
		Path:        op.Path,
		FlagKey:     op.Key,
		BaseSHA:     op.BaseVersion,
		Message:     op.Message,
		Apply:       op.Apply,
		FlagChanged: op.Changed,
		MaxAttempts: op.MaxAttempts,
	}, githubapp.Identity(who))

	if errors.Is(err, githubapp.ErrFlagConflict) {
		return nil, ErrConflict
	}
	if err != nil {
		return nil, err
	}
	return &Result{Version: result.CommitSHA, Retried: result.Retried}, nil
}

func (g *GitHubBackend) CreateFile(ctx context.Context, path string, content []byte, message string, who Identity) error {
	_, err := g.client.CreateFile(ctx, path, content, message, githubapp.Identity(who))
	return err
}

func (g *GitHubBackend) History(ctx context.Context, path string, limit int) ([]Commit, error) {
	commits, err := g.client.History(ctx, path, limit)
	if err != nil {
		return nil, err
	}

	out := make([]Commit, 0, len(commits))
	for _, c := range commits {
		out = append(out, Commit(c))
	}
	return out, nil
}

func (g *GitHubBackend) Check(ctx context.Context) error {
	if _, err := g.client.ListDirectories(ctx, ""); err != nil {
		return fmt.Errorf("cannot read the flags repository: %w", err)
	}
	return nil
}
