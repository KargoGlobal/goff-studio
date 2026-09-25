package githubapp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Config struct {
	APIBase string
	Owner   string
	Repo    string
	Branch  string
}

type Identity struct {
	Name    string
	Email   string
	Subject string
}

type Client struct {
	cfg    Config
	tokens TokenSource
	http   *http.Client
}

type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

type StaticToken string

func (s StaticToken) Token(context.Context) (string, error) { return string(s), nil }

var ErrFlagConflict = errors.New("this flag was changed by someone else")

func New(cfg Config, tokens TokenSource, client *http.Client) *Client {
	if cfg.APIBase == "" {
		cfg.APIBase = "https://api.github.com"
	}
	if cfg.Branch == "" {
		cfg.Branch = "main"
	}
	if client == nil {
		client = &http.Client{Timeout: 25 * time.Second}
	}
	return &Client{cfg: cfg, tokens: tokens, http: client}
}

type File struct {
	Path    string
	Content []byte
	SHA     string
}

func (c *Client) ReadFile(ctx context.Context, path string) (*File, error) {
	endpoint := fmt.Sprintf("%s/repos/%s/%s/contents/%s?ref=%s",
		c.cfg.APIBase, c.cfg.Owner, c.cfg.Repo, path, url.QueryEscape(c.cfg.Branch))

	var out struct {
		Content string `json:"content"`
		SHA     string `json:"sha"`
	}
	if err := c.do(ctx, http.MethodGet, endpoint, nil, &out); err != nil {
		return nil, err
	}

	decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(out.Content, "\n", ""))
	if err != nil {
		return nil, fmt.Errorf("decoding %s: %w", path, err)
	}
	return &File{Path: path, Content: decoded, SHA: out.SHA}, nil
}

func (c *Client) ListDirectories(ctx context.Context, path string) ([]string, error) {
	endpoint := fmt.Sprintf("%s/repos/%s/%s/contents/%s?ref=%s",
		c.cfg.APIBase, c.cfg.Owner, c.cfg.Repo, path, url.QueryEscape(c.cfg.Branch))

	var entries []struct {
		Name string `json:"name"`
		Type string `json:"type"`
	}
	if err := c.do(ctx, http.MethodGet, endpoint, nil, &entries); err != nil {
		return nil, err
	}

	var out []string
	for _, e := range entries {
		if e.Type == "dir" {
			out = append(out, e.Name)
		}
	}
	return out, nil
}

func (c *Client) ListFiles(ctx context.Context, path string) ([]string, error) {
	endpoint := fmt.Sprintf("%s/repos/%s/%s/contents/%s?ref=%s",
		c.cfg.APIBase, c.cfg.Owner, c.cfg.Repo, path, url.QueryEscape(c.cfg.Branch))

	var entries []struct {
		Name string `json:"name"`
		Path string `json:"path"`
		Type string `json:"type"`
	}
	if err := c.do(ctx, http.MethodGet, endpoint, nil, &entries); err != nil {
		return nil, err
	}

	var out []string
	for _, e := range entries {
		if e.Type != "file" {
			continue
		}
		if strings.HasSuffix(e.Name, ".yaml") || strings.HasSuffix(e.Name, ".yml") {
			out = append(out, e.Path)
		}
	}
	return out, nil
}

type ChangeOp struct {
	Path        string
	FlagKey     string
	BaseSHA     string
	Message     string
	Apply       func(current []byte) ([]byte, error)
	FlagChanged func(current []byte) (bool, error)
	LoadedFlag  any
	MaxAttempts int
}

type CommitResult struct {
	CommitSHA string
	Attempts  int
	Retried   bool
}

func (c *Client) Commit(ctx context.Context, op ChangeOp, who Identity) (*CommitResult, error) {
	attempts := op.MaxAttempts
	if attempts <= 0 {
		attempts = 3
	}

	retried := false

	for attempt := 1; attempt <= attempts; attempt++ {
		current, err := c.ReadFile(ctx, op.Path)
		if err != nil {
			return nil, err
		}

		if op.BaseSHA != "" && current.SHA != op.BaseSHA {
			retried = true
			if op.FlagChanged != nil {
				changed, err := op.FlagChanged(current.Content)
				if err != nil {
					return nil, err
				}
				if changed {
					return nil, ErrFlagConflict
				}
			}
		}

		next, err := op.Apply(current.Content)
		if err != nil {
			return nil, err
		}
		if bytes.Equal(next, current.Content) {
			return &CommitResult{Attempts: attempt}, nil
		}

		sha, err := c.put(ctx, op.Path, current.SHA, next, message(op, who), who)
		if err == nil {
			return &CommitResult{CommitSHA: sha, Attempts: attempt, Retried: retried}, nil
		}
		if !errors.Is(err, errStaleSHA) {
			return nil, err
		}
		retried = true
	}

	return nil, fmt.Errorf("could not commit %s after %d attempts because the file kept changing", op.Path, attempts)
}

func message(op ChangeOp, who Identity) string {
	body := op.Message
	if body == "" {
		body = fmt.Sprintf("update %s", op.FlagKey)
	}

	var b strings.Builder
	b.WriteString(body)
	b.WriteString("\n\n")
	if who.Email != "" {
		b.WriteString("GOFF-Studio-User: " + who.Email + "\n")
	}
	if who.Subject != "" {
		b.WriteString("GOFF-Studio-User-Id: " + who.Subject + "\n")
	}
	return b.String()
}

var errStaleSHA = errors.New("stale file sha")

var ErrNotFound = errors.New("not found")

func (c *Client) put(ctx context.Context, path, sha string, content []byte, msg string, who Identity) (string, error) {
	endpoint := fmt.Sprintf("%s/repos/%s/%s/contents/%s", c.cfg.APIBase, c.cfg.Owner, c.cfg.Repo, path)

	body := map[string]any{
		"message": msg,
		"content": base64.StdEncoding.EncodeToString(content),
		"branch":  c.cfg.Branch,
	}
	if sha != "" {
		body["sha"] = sha
	}
	if who.Name != "" && who.Email != "" {
		body["author"] = map[string]string{"name": who.Name, "email": who.Email}
	}

	var out struct {
		Commit struct {
			SHA string `json:"sha"`
		} `json:"commit"`
	}
	if err := c.do(ctx, http.MethodPut, endpoint, body, &out); err != nil {
		return "", err
	}
	return out.Commit.SHA, nil
}

type Commit struct {
	SHA     string    `json:"sha"`
	Message string    `json:"message"`
	Author  string    `json:"author"`
	Email   string    `json:"email"`
	When    time.Time `json:"when"`
}

func (c *Client) History(ctx context.Context, path string, limit int) ([]Commit, error) {
	if limit <= 0 {
		limit = 30
	}
	endpoint := fmt.Sprintf("%s/repos/%s/%s/commits?path=%s&sha=%s&per_page=%d",
		c.cfg.APIBase, c.cfg.Owner, c.cfg.Repo, url.QueryEscape(path), url.QueryEscape(c.cfg.Branch), limit)

	var raw []struct {
		SHA    string `json:"sha"`
		Commit struct {
			Message string `json:"message"`
			Author  struct {
				Name  string    `json:"name"`
				Email string    `json:"email"`
				Date  time.Time `json:"date"`
			} `json:"author"`
		} `json:"commit"`
	}
	if err := c.do(ctx, http.MethodGet, endpoint, nil, &raw); err != nil {
		return nil, err
	}

	out := make([]Commit, 0, len(raw))
	for _, r := range raw {
		out = append(out, Commit{
			SHA:     r.SHA,
			Message: r.Commit.Message,
			Author:  r.Commit.Author.Name,
			Email:   r.Commit.Author.Email,
			When:    r.Commit.Author.Date,
		})
	}
	return out, nil
}

func (c *Client) do(ctx context.Context, method, endpoint string, body, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return err
	}

	token, err := c.tokens.Token(ctx)
	if err != nil {
		return fmt.Errorf("github credentials: %w", err)
	}

	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()

	payload, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}

	switch {
	case res.StatusCode == http.StatusConflict, res.StatusCode == http.StatusPreconditionFailed:
		return errStaleSHA
	case res.StatusCode == http.StatusNotFound:
		return fmt.Errorf("%w: %s", ErrNotFound, shortPath(endpoint))
	case res.StatusCode < 200 || res.StatusCode >= 300:
		return fmt.Errorf("github %s %s: %s", method, shortPath(endpoint), apiError(payload, res.StatusCode))
	}

	if out != nil {
		if err := json.Unmarshal(payload, out); err != nil {
			return fmt.Errorf("decoding github response: %w", err)
		}
	}
	return nil
}

func shortPath(endpoint string) string {
	if u, err := url.Parse(endpoint); err == nil {
		return u.Path
	}
	return endpoint
}

func apiError(payload []byte, status int) string {
	var parsed struct {
		Message string `json:"message"`
		Errors  []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(payload, &parsed); err == nil && parsed.Message != "" {
		if len(parsed.Errors) > 0 && parsed.Errors[0].Message != "" {
			return parsed.Message + " (" + parsed.Errors[0].Message + ")"
		}
		return parsed.Message
	}
	return fmt.Sprintf("status %d", status)
}

func (c *Client) CreateFile(ctx context.Context, path string, content []byte, message string, who Identity) (string, error) {
	return c.put(ctx, path, "", content, message, who)
}
