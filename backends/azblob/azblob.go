// Package azblob stores flag files as Azure Blob Storage blobs.
package azblob

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/go-feature-flag/studio/internal/storage"
)

func init() {
	storage.Register(storage.KindAzureBlobStorage, func(s storage.Settings) (storage.Backend, error) {
		return New(context.Background(), Config{
			Container:  s.Bucket,
			Prefix:     s.Prefix,
			AccountURL: s.Options["accountURL"],
		})
	})
}

// connectionStringEnv is read from the environment rather than the config
// file because a connection string carries the account key.
const connectionStringEnv = "AZURE_STORAGE_CONNECTION_STRING"

// requestTimeout bounds each API call, since the server sets no request
// deadline and a stalled endpoint would otherwise hang a handler.
const requestTimeout = 30 * time.Second

// Item is one listed blob, or one version of it.
type Item struct {
	Name      string
	VersionID string
	Metadata  map[string]string
	Modified  time.Time
}

// API is the slice of Azure Blob Storage the backend uses. It is an interface
// so tests can run against a fake; sdkAPI implements it with the Azure SDK.
type API interface {
	// Download returns the blob's content and ETag, or errNotFound.
	Download(ctx context.Context, name string) ([]byte, string, error)
	// Upload writes the blob only if its ETag is still ifMatch, or, when
	// ifMatch is empty, only if it does not exist yet. It returns
	// errPreconditionFailed when the condition fails, and the new ETag and
	// version ID (empty without blob versioning) on success.
	Upload(ctx context.Context, name string, content []byte, ifMatch string, meta map[string]string) (etag, versionID string, err error)
	// List returns the blobs under prefix and, with a delimiter, the
	// "directory" prefixes. With versions it returns every version of every
	// blob, with metadata.
	List(ctx context.Context, prefix, delimiter string, versions bool) ([]Item, []string, error)
}

var (
	errNotFound           = errors.New("azure blob not found")
	errPreconditionFailed = errors.New("azure blob precondition failed")
)

type Config struct {
	Container string
	Prefix    string
	// AccountURL is https://<account>.blob.core.windows.net, used with
	// DefaultAzureCredential. AZURE_STORAGE_CONNECTION_STRING, when set,
	// takes precedence (and is how an Azurite emulator is reached).
	AccountURL string
}

type Backend struct {
	api       API
	container string
	prefix    string
	// versioning is set by Check and by the first versioned upload, and read
	// by concurrent requests, so it is atomic.
	versioning atomic.Bool
}

func New(_ context.Context, cfg Config) (*Backend, error) {
	if strings.TrimSpace(cfg.Container) == "" {
		return nil, fmt.Errorf("a container is required (storage.bucket)")
	}
	connStr := os.Getenv(connectionStringEnv)
	if connStr == "" && cfg.AccountURL == "" {
		return nil, fmt.Errorf("set storage.options.accountURL, or %s", connectionStringEnv)
	}
	api, err := newSDKAPI(cfg.AccountURL, connStr, cfg.Container)
	if err != nil {
		return nil, err
	}
	return NewWithAPI(api, cfg.Container, cfg.Prefix), nil
}

func NewWithAPI(api API, container, prefix string) *Backend {
	return &Backend{api: api, container: container, prefix: strings.Trim(prefix, "/")}
}

func (b *Backend) Name() string { return storage.KindAzureBlobStorage }

// Capabilities reports history and attribution only when blob versioning is
// on: every version carries its author in metadata, but without versioning
// there is nothing to show it in. There is never a review step.
func (b *Backend) Capabilities() storage.Capabilities {
	v := b.versioning.Load()
	return storage.Capabilities{History: v, Attribution: v, Review: false}
}

func (b *Backend) key(p string) (string, error) {
	for _, part := range strings.Split(p, "/") {
		if part == ".." {
			return "", fmt.Errorf("%q is outside the flag prefix", p)
		}
	}
	clean := strings.TrimPrefix(p, "/")
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

func (b *Backend) ReadFile(ctx context.Context, p string) (*storage.File, error) {
	key, err := b.key(p)
	if err != nil {
		return nil, err
	}
	content, etag, err := b.api.Download(ctx, key)
	if err != nil {
		if errors.Is(err, errNotFound) {
			return nil, fmt.Errorf("%w: %s", storage.ErrNotFound, p)
		}
		return nil, fmt.Errorf("reading %s/%s: %w", b.container, key, err)
	}
	return &storage.File{Path: p, Content: content, Version: etag}, nil
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

func (b *Backend) list(ctx context.Context, dir string) ([]Item, []string, error) {
	prefix, err := b.key(strings.TrimSuffix(dir, "/"))
	if err != nil {
		return nil, nil, err
	}
	prefix = strings.TrimSuffix(prefix, "/")
	if prefix != "" {
		prefix += "/"
	}
	items, prefixes, err := b.api.List(ctx, prefix, "/", false)
	if err != nil {
		return nil, nil, fmt.Errorf("listing %s/%s: %w", b.container, prefix, err)
	}
	if len(items) == 0 && len(prefixes) == 0 {
		return nil, nil, fmt.Errorf("%w: %s", storage.ErrNotFound, dir)
	}
	return items, prefixes, nil
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

		etag, err := b.upload(ctx, op.Path, next, current.Version, meta)
		if err == nil {
			return &storage.Result{Version: etag, Retried: retried}, nil
		}
		if !errors.Is(err, errPreconditionFailed) {
			return nil, err
		}
		retried = true
	}

	return nil, fmt.Errorf("could not write %s after %d attempts because the blob kept changing", op.Path, attempts)
}

func (b *Backend) upload(ctx context.Context, p string, content []byte, ifMatch string, meta map[string]string) (string, error) {
	key, err := b.key(p)
	if err != nil {
		return "", err
	}
	etag, versionID, err := b.api.Upload(ctx, key, content, ifMatch, meta)
	if err != nil {
		if errors.Is(err, errPreconditionFailed) {
			return "", err
		}
		return "", fmt.Errorf("writing %s/%s: %w", b.container, key, err)
	}
	// A version ID proves versioning is on even if Check saw no versions yet.
	if versionID != "" {
		b.versioning.Store(true)
	}
	return etag, nil
}

func (b *Backend) CreateFile(ctx context.Context, p string, content []byte, message string, who storage.Identity) error {
	_, err := b.upload(ctx, p, content, "", attributionMetadata(who, changeMessage(message, "")))
	if errors.Is(err, errPreconditionFailed) {
		return fmt.Errorf("%s already exists", p)
	}
	return err
}

const unknownAuthor = "unknown"

// History lists the blob's versions, newest first, with the attribution each
// one carries. The listing already includes metadata, so this is one list
// call (per page) and no per-version reads. Versions written before Studio
// recorded attribution show an unknown author.
func (b *Backend) History(ctx context.Context, p string, limit int) ([]storage.Commit, error) {
	if !b.versioning.Load() {
		return nil, nil
	}
	if limit <= 0 {
		limit = 30
	}
	if limit > 1000 {
		limit = 1000
	}

	key, err := b.key(p)
	if err != nil {
		return nil, err
	}
	items, _, err := b.api.List(ctx, key, "", true)
	if err != nil {
		return nil, fmt.Errorf("listing versions of %s/%s: %w", b.container, key, err)
	}

	var versions []Item
	for _, item := range items {
		if item.Name == key && item.VersionID != "" {
			versions = append(versions, item)
		}
	}
	// Version IDs are RFC 3339 timestamps, so they sort in time order.
	sort.SliceStable(versions, func(i, j int) bool { return versions[i].VersionID > versions[j].VersionID })
	if len(versions) > limit {
		versions = versions[:limit]
	}

	commits := make([]storage.Commit, 0, len(versions))
	for _, v := range versions {
		c := storage.Commit{
			SHA:     v.VersionID,
			Message: "blob version " + v.VersionID,
			Author:  unknownAuthor,
			When:    v.Modified,
		}
		applyMetadata(&c, v.Metadata)
		commits = append(commits, c)
	}
	return commits, nil
}

func applyMetadata(c *storage.Commit, meta map[string]string) {
	name := metaValue(meta, metaUserName)
	email := metaValue(meta, metaUserEmail)
	switch {
	case name != "":
		c.Author = name
	case email != "":
		c.Author = email
	}
	c.Email = email
	if msg := metaValue(meta, metaMessage); msg != "" {
		c.Message = msg
	}
}

// Check confirms the container can be listed and detects blob versioning.
// The data plane cannot read the account's versioning setting, so Studio
// infers it from whether listed blobs carry version IDs; the first write
// under versioning also turns it on.
func (b *Backend) Check(ctx context.Context) error {
	prefix := ""
	if b.prefix != "" {
		prefix = b.prefix + "/"
	}
	items, _, err := b.api.List(ctx, prefix, "", true)
	if err != nil {
		return fmt.Errorf("cannot list %s/%s: %w", b.container, b.prefix, err)
	}
	for _, item := range items {
		if item.VersionID != "" {
			b.versioning.Store(true)
			break
		}
	}
	return nil
}

func (b *Backend) SetVersioning(enabled bool) { b.versioning.Store(enabled) }
