// Package gcs stores flag files as Google Cloud Storage objects.
//
// It talks to the GCS JSON API directly over HTTP rather than through the
// Cloud client library: the handful of calls an editor needs (read, list,
// conditional upload, list versions) do not justify gRPC and the rest of the
// client's dependency tree, and a plain HTTP API is easy to fake in tests.
package gcs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-feature-flag/studio/internal/storage"
	"golang.org/x/oauth2/google"
)

func init() {
	storage.Register(storage.KindGoogleStorage, func(s storage.Settings) (storage.Backend, error) {
		return New(context.Background(), Config{
			Bucket:   s.Bucket,
			Prefix:   s.Prefix,
			Endpoint: s.Options["endpoint"],
		})
	})
}

const (
	defaultEndpoint = "https://storage.googleapis.com"
	scope           = "https://www.googleapis.com/auth/devstorage.read_write"
	// requestTimeout bounds each API call, since the server sets no request
	// deadline and a stalled endpoint would otherwise hang a handler.
	requestTimeout = 30 * time.Second
)

type Config struct {
	Bucket string
	Prefix string
	// Endpoint overrides https://storage.googleapis.com, for an emulator.
	// Requests to a custom endpoint are sent without Google credentials.
	Endpoint string
}

type Backend struct {
	client     *http.Client
	endpoint   string
	bucket     string
	prefix     string
	versioning bool
}

func New(ctx context.Context, cfg Config) (*Backend, error) {
	if strings.TrimSpace(cfg.Bucket) == "" {
		return nil, fmt.Errorf("a bucket is required")
	}

	if cfg.Endpoint != "" {
		return NewWithClient(&http.Client{}, cfg.Endpoint, cfg.Bucket, cfg.Prefix), nil
	}

	client, err := google.DefaultClient(ctx, scope)
	if err != nil {
		return nil, fmt.Errorf("loading google application default credentials: %w", err)
	}
	return NewWithClient(client, defaultEndpoint, cfg.Bucket, cfg.Prefix), nil
}

// NewWithClient uses client as given, except that a client with no timeout
// gets requestTimeout; the caller's client is not modified.
func NewWithClient(client *http.Client, endpoint, bucket, prefix string) *Backend {
	if client.Timeout == 0 {
		bounded := *client
		bounded.Timeout = requestTimeout
		client = &bounded
	}
	return &Backend{
		client:   client,
		endpoint: strings.TrimSuffix(endpoint, "/"),
		bucket:   bucket,
		prefix:   strings.Trim(prefix, "/"),
	}
}

func (b *Backend) Name() string { return storage.KindGoogleStorage }

// Capabilities reports history and attribution only when object versioning is
// on: every generation carries its author in custom metadata, but without
// versioning there is nothing to show it in. There is never a review step.
func (b *Backend) Capabilities() storage.Capabilities {
	return storage.Capabilities{History: b.versioning, Attribution: b.versioning, Review: false}
}

func (b *Backend) key(path string) (string, error) {
	for _, part := range strings.Split(path, "/") {
		if part == ".." {
			return "", fmt.Errorf("%q is outside the flag prefix", path)
		}
	}
	clean := strings.TrimPrefix(path, "/")
	if b.prefix == "" {
		return clean, nil
	}
	return b.prefix + "/" + clean, nil
}

func (b *Backend) unkey(key string) string {
	if b.prefix == "" {
		return key
	}
	return strings.TrimPrefix(strings.TrimPrefix(key, b.prefix), "/")
}

// object is the subset of the GCS object resource Studio reads.
type object struct {
	Name       string            `json:"name"`
	Generation string            `json:"generation"`
	Updated    time.Time         `json:"updated"`
	Metadata   map[string]string `json:"metadata"`
}

type apiError struct {
	status int
	body   string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("gcs returned %d: %s", e.status, strings.TrimSpace(e.body))
}

func statusOf(err error) int {
	var api *apiError
	if errors.As(err, &api) {
		return api.status
	}
	return 0
}

func (b *Backend) objectURL(key string, query url.Values) string {
	u := fmt.Sprintf("%s/storage/v1/b/%s/o/%s", b.endpoint, url.PathEscape(b.bucket), url.PathEscape(key))
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	return u
}

func (b *Backend) do(req *http.Request, into any) (http.Header, []byte, error) {
	resp, err := b.client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, nil, &apiError{status: resp.StatusCode, body: string(body)}
	}
	if into != nil {
		if err := json.Unmarshal(body, into); err != nil {
			return nil, nil, fmt.Errorf("decoding gcs response: %w", err)
		}
	}
	return resp.Header, body, nil
}

func (b *Backend) ReadFile(ctx context.Context, path string) (*storage.File, error) {
	key, err := b.key(path)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.objectURL(key, url.Values{"alt": {"media"}}), nil)
	if err != nil {
		return nil, err
	}
	header, body, err := b.do(req, nil)
	if err != nil {
		if statusOf(err) == http.StatusNotFound {
			return nil, fmt.Errorf("%w: %s", storage.ErrNotFound, path)
		}
		return nil, fmt.Errorf("reading gs://%s/%s: %w", b.bucket, key, err)
	}

	generation := header.Get("X-Goog-Generation")
	if generation == "" {
		return nil, fmt.Errorf("reading gs://%s/%s: no generation in the response", b.bucket, key)
	}
	return &storage.File{Path: path, Content: body, Version: generation}, nil
}

func (b *Backend) ListFiles(ctx context.Context, dir string) ([]string, error) {
	items, _, err := b.list(ctx, dir)
	if err != nil {
		return nil, err
	}

	var out []string
	for _, item := range items {
		if strings.HasSuffix(item.Name, ".yaml") || strings.HasSuffix(item.Name, ".yml") {
			out = append(out, b.unkey(item.Name))
		}
	}
	sort.Strings(out)
	return out, nil
}

func (b *Backend) ListDirectories(ctx context.Context, dir string) ([]string, error) {
	_, prefixes, err := b.list(ctx, dir)
	if err != nil {
		return nil, err
	}

	var out []string
	for _, p := range prefixes {
		// A delimited listing returns full prefixes like "production/eu/";
		// callers expect just "eu", as the file and github backends return.
		name := path.Base(strings.TrimSuffix(p, "/"))
		if name != "" && name != "." && !strings.HasPrefix(name, ".") {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out, nil
}

type listResponse struct {
	Items         []object `json:"items"`
	Prefixes      []string `json:"prefixes"`
	NextPageToken string   `json:"nextPageToken"`
}

func (b *Backend) list(ctx context.Context, dir string) ([]object, []string, error) {
	prefix, err := b.key(strings.TrimSuffix(dir, "/"))
	if err != nil {
		return nil, nil, err
	}
	prefix = strings.TrimSuffix(prefix, "/")
	if prefix != "" {
		prefix += "/"
	}

	var items []object
	var prefixes []string
	token := ""
	for {
		query := url.Values{"prefix": {prefix}, "delimiter": {"/"}}
		if token != "" {
			query.Set("pageToken", token)
		}
		var out listResponse
		if err := b.getJSON(ctx, b.bucketURL("/o", query), &out); err != nil {
			return nil, nil, fmt.Errorf("listing gs://%s/%s: %w", b.bucket, prefix, err)
		}
		items = append(items, out.Items...)
		prefixes = append(prefixes, out.Prefixes...)
		if out.NextPageToken == "" {
			break
		}
		token = out.NextPageToken
	}

	if len(items) == 0 && len(prefixes) == 0 {
		return nil, nil, fmt.Errorf("%w: %s", storage.ErrNotFound, dir)
	}
	return items, prefixes, nil
}

func (b *Backend) bucketURL(suffix string, query url.Values) string {
	u := fmt.Sprintf("%s/storage/v1/b/%s%s", b.endpoint, url.PathEscape(b.bucket), suffix)
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	return u
}

func (b *Backend) getJSON(ctx context.Context, u string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	_, _, err = b.do(req, into)
	return err
}

func (b *Backend) Write(ctx context.Context, op storage.ChangeOp, who storage.Identity) (*storage.Result, error) {
	meta := attributionMetadata(who, changeMessage(op.Message, op.Key))

	attempts := op.MaxAttempts
	if attempts <= 0 {
		attempts = 3
	}

	retried := false
	for attempt := 1; attempt <= attempts; attempt++ {
		current, err := b.ReadFile(ctx, op.Path)
		if err != nil {
			return nil, err
		}

		if op.BaseVersion != "" && current.Version != op.BaseVersion {
			retried = true
			if op.Changed != nil {
				changed, err := op.Changed(current.Content)
				if err != nil {
					return nil, err
				}
				if changed {
					return nil, storage.ErrConflict
				}
			}
		}

		next, err := op.Apply(current.Content)
		if err != nil {
			return nil, err
		}
		if bytes.Equal(next, current.Content) {
			return &storage.Result{Version: current.Version}, nil
		}

		version, err := b.upload(ctx, op.Path, next, current.Version, meta)
		if err == nil {
			return &storage.Result{Version: version, Retried: retried}, nil
		}
		if !errors.Is(err, errPreconditionFailed) {
			return nil, err
		}
		retried = true
	}

	return nil, fmt.Errorf("could not write %s after %d attempts because the object kept changing", op.Path, attempts)
}

var errPreconditionFailed = errors.New("gcs precondition failed")

// upload writes content only if the live object is still at generation
// ifGeneration; "0" means the object must not exist yet.
func (b *Backend) upload(ctx context.Context, path string, content []byte, ifGeneration string, meta map[string]string) (string, error) {
	key, err := b.key(path)
	if err != nil {
		return "", err
	}

	resource, err := json.Marshal(struct {
		Name        string            `json:"name"`
		ContentType string            `json:"contentType"`
		Metadata    map[string]string `json:"metadata,omitempty"`
	}{key, "application/yaml", meta})
	if err != nil {
		return "", err
	}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for _, part := range []struct {
		contentType string
		data        []byte
	}{
		{"application/json; charset=UTF-8", resource},
		{"application/yaml", content},
	} {
		w, err := mw.CreatePart(textproto.MIMEHeader{"Content-Type": {part.contentType}})
		if err != nil {
			return "", err
		}
		if _, err := w.Write(part.data); err != nil {
			return "", err
		}
	}
	if err := mw.Close(); err != nil {
		return "", err
	}

	query := url.Values{"uploadType": {"multipart"}, "ifGenerationMatch": {ifGeneration}}
	u := fmt.Sprintf("%s/upload/storage/v1/b/%s/o?%s", b.endpoint, url.PathEscape(b.bucket), query.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "multipart/related; boundary="+mw.Boundary())

	var out object
	if _, _, err := b.do(req, &out); err != nil {
		if statusOf(err) == http.StatusPreconditionFailed {
			return "", errPreconditionFailed
		}
		return "", fmt.Errorf("writing gs://%s/%s: %w", b.bucket, key, err)
	}
	return out.Generation, nil
}

func (b *Backend) CreateFile(ctx context.Context, path string, content []byte, message string, who storage.Identity) error {
	_, err := b.upload(ctx, path, content, "0", attributionMetadata(who, changeMessage(message, "")))
	if errors.Is(err, errPreconditionFailed) {
		return fmt.Errorf("%s already exists", path)
	}
	return err
}

const unknownAuthor = "unknown"

// History lists the object's generations, newest first. Unlike S3, a versioned
// listing already carries each generation's custom metadata, so attribution
// costs no extra request per entry. Generations written before Studio recorded
// attribution show an unknown author.
func (b *Backend) History(ctx context.Context, path string, limit int) ([]storage.Commit, error) {
	if !b.versioning {
		return nil, nil
	}
	if limit <= 0 {
		limit = 30
	}
	if limit > 1000 {
		limit = 1000
	}

	key, err := b.key(path)
	if err != nil {
		return nil, err
	}

	var generations []object
	token := ""
	// GCS lists generations oldest first, so the newest are on the last page;
	// every page has to be read before the limit is applied.
	for {
		query := url.Values{"prefix": {key}, "versions": {"true"}}
		if token != "" {
			query.Set("pageToken", token)
		}
		var out listResponse
		if err := b.getJSON(ctx, b.bucketURL("/o", query), &out); err != nil {
			return nil, fmt.Errorf("listing versions of gs://%s/%s: %w", b.bucket, key, err)
		}
		for _, item := range out.Items {
			if item.Name == key {
				generations = append(generations, item)
			}
		}
		if out.NextPageToken == "" {
			break
		}
		token = out.NextPageToken
	}

	sort.SliceStable(generations, func(i, j int) bool {
		gi, _ := strconv.ParseInt(generations[i].Generation, 10, 64)
		gj, _ := strconv.ParseInt(generations[j].Generation, 10, 64)
		return gi > gj
	})
	if len(generations) > limit {
		generations = generations[:limit]
	}

	commits := make([]storage.Commit, 0, len(generations))
	for _, g := range generations {
		c := storage.Commit{
			SHA:     g.Generation,
			Message: "object generation " + g.Generation,
			Author:  unknownAuthor,
			When:    g.Updated,
		}
		applyMetadata(&c, g.Metadata)
		commits = append(commits, c)
	}
	return commits, nil
}

func applyMetadata(c *storage.Commit, meta map[string]string) {
	name := meta[metaUserName]
	email := meta[metaUserEmail]
	switch {
	case name != "":
		c.Author = name
	case email != "":
		c.Author = email
	}
	c.Email = email
	if msg := meta[metaMessage]; msg != "" {
		c.Message = msg
	}
}

func (b *Backend) Check(ctx context.Context) error {
	if _, _, err := b.list(ctx, ""); err != nil && !errors.Is(err, storage.ErrNotFound) {
		return fmt.Errorf("cannot list gs://%s/%s: %w", b.bucket, b.prefix, err)
	}

	var out struct {
		Versioning struct {
			Enabled bool `json:"enabled"`
		} `json:"versioning"`
	}
	if err := b.getJSON(ctx, b.bucketURL("", url.Values{"fields": {"versioning"}}), &out); err == nil {
		b.versioning = out.Versioning.Enabled
	}
	return nil
}

func (b *Backend) SetVersioning(enabled bool) { b.versioning = enabled }
