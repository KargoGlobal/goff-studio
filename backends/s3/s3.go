package s3

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/go-feature-flag/studio/internal/storage"
)

func init() {
	storage.Register("s3", func(s storage.Settings) (storage.Backend, error) {
		return New(context.Background(), Config{
			Bucket:   s.Bucket,
			Region:   s.Region,
			Prefix:   s.Prefix,
			Endpoint: s.Options["endpoint"],
		})
	})
}

type API interface {
	GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	PutObject(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	HeadObject(ctx context.Context, params *s3.HeadObjectInput, optFns ...func(*s3.Options)) (*s3.HeadObjectOutput, error)
	ListObjectsV2(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
	ListObjectVersions(ctx context.Context, params *s3.ListObjectVersionsInput, optFns ...func(*s3.Options)) (*s3.ListObjectVersionsOutput, error)
	GetBucketVersioning(ctx context.Context, params *s3.GetBucketVersioningInput, optFns ...func(*s3.Options)) (*s3.GetBucketVersioningOutput, error)
}

type Config struct {
	Bucket   string
	Region   string
	Prefix   string
	Endpoint string
}

type Backend struct {
	api        API
	bucket     string
	prefix     string
	versioning bool
}

func New(ctx context.Context, cfg Config) (*Backend, error) {
	if strings.TrimSpace(cfg.Bucket) == "" {
		return nil, fmt.Errorf("a bucket is required")
	}

	opts := []func(*awsconfig.LoadOptions) error{}
	if cfg.Region != "" {
		opts = append(opts, awsconfig.WithRegion(cfg.Region))
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("loading aws configuration: %w", err)
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		if cfg.Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
			o.UsePathStyle = true
		}
	})

	return NewWithAPI(client, cfg.Bucket, cfg.Prefix), nil
}

func NewWithAPI(api API, bucket, prefix string) *Backend {
	return &Backend{api: api, bucket: bucket, prefix: strings.Trim(prefix, "/")}
}

func (b *Backend) Name() string { return "s3" }

// Capabilities reports history and attribution only when bucket versioning is
// on: every version carries its author in object metadata, but without
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

func (b *Backend) ReadFile(ctx context.Context, path string) (*storage.File, error) {
	key, err := b.key(path)
	if err != nil {
		return nil, err
	}

	out, err := b.api.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		if isNotFound(err) {
			return nil, fmt.Errorf("%w: %s", storage.ErrNotFound, path)
		}
		return nil, fmt.Errorf("reading s3://%s/%s: %w", b.bucket, key, err)
	}
	defer out.Body.Close()

	content, err := io.ReadAll(out.Body)
	if err != nil {
		return nil, err
	}

	return &storage.File{Path: path, Content: content, Version: etag(out.ETag)}, nil
}

func (b *Backend) ListFiles(ctx context.Context, dir string) ([]string, error) {
	keys, _, err := b.list(ctx, dir)
	if err != nil {
		return nil, err
	}

	var out []string
	for _, key := range keys {
		if strings.HasSuffix(key, ".yaml") || strings.HasSuffix(key, ".yml") {
			out = append(out, b.unkey(key))
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
		name := strings.Trim(b.unkey(strings.TrimSuffix(p, "/")), "/")
		if name != "" && !strings.HasPrefix(name, ".") {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (b *Backend) list(ctx context.Context, dir string) (keys []string, prefixes []string, err error) {
	prefix, err := b.key(strings.TrimSuffix(dir, "/"))
	if err != nil {
		return nil, nil, err
	}
	prefix = strings.TrimSuffix(prefix, "/")
	if prefix != "" {
		prefix += "/"
	}

	var token *string
	for {
		out, err := b.api.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(b.bucket),
			Prefix:            aws.String(prefix),
			Delimiter:         aws.String("/"),
			ContinuationToken: token,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("listing s3://%s/%s: %w", b.bucket, prefix, err)
		}

		for _, obj := range out.Contents {
			keys = append(keys, aws.ToString(obj.Key))
		}
		for _, cp := range out.CommonPrefixes {
			prefixes = append(prefixes, aws.ToString(cp.Prefix))
		}

		if !aws.ToBool(out.IsTruncated) {
			break
		}
		token = out.NextContinuationToken
	}

	if len(keys) == 0 && len(prefixes) == 0 {
		return nil, nil, fmt.Errorf("%w: %s", storage.ErrNotFound, dir)
	}
	return keys, prefixes, nil
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

		version, err := b.put(ctx, op.Path, next, current.Version, meta)
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

var errPreconditionFailed = errors.New("s3 precondition failed")

func (b *Backend) put(ctx context.Context, path string, content []byte, ifMatch string, meta map[string]string) (string, error) {
	key, err := b.key(path)
	if err != nil {
		return "", err
	}

	input := &s3.PutObjectInput{
		Bucket:      aws.String(b.bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(content),
		ContentType: aws.String("application/yaml"),
		Metadata:    meta,
	}
	if ifMatch != "" {
		input.IfMatch = aws.String(`"` + ifMatch + `"`)
	}

	out, err := b.api.PutObject(ctx, input)
	if err != nil {
		if isPreconditionFailed(err) {
			return "", errPreconditionFailed
		}
		return "", fmt.Errorf("writing s3://%s/%s: %w", b.bucket, key, err)
	}
	return etag(out.ETag), nil
}

func (b *Backend) CreateFile(ctx context.Context, path string, content []byte, message string, who storage.Identity) error {
	key, err := b.key(path)
	if err != nil {
		return err
	}

	_, err = b.api.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(b.bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(content),
		ContentType: aws.String("application/yaml"),
		IfNoneMatch: aws.String("*"),
		Metadata:    attributionMetadata(who, changeMessage(message, "")),
	})
	if err != nil {
		if isPreconditionFailed(err) {
			return fmt.Errorf("%s already exists", path)
		}
		return fmt.Errorf("creating s3://%s/%s: %w", b.bucket, key, err)
	}
	return nil
}

const (
	// headConcurrency caps parallel HeadObject calls when reading authors.
	headConcurrency = 4
	// maxVersionPages bounds ListObjectVersions calls for one history read.
	maxVersionPages = 10
)

// History lists the object's versions, newest first, and reads each version's
// attribution metadata with one HeadObject per version (at most limit of them,
// headConcurrency at a time). Versions written before attribution existed, or
// whose metadata cannot be read, fall back to an "object version" entry with an
// unknown author. Delete markers carry no metadata, so a delete made outside
// Studio shows with an unknown author; Studio itself never deletes objects.
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

	entries, err := b.versions(ctx, key, limit)
	if err != nil {
		return nil, err
	}
	if err := b.attribute(ctx, key, entries); err != nil {
		return nil, err
	}

	commits := make([]storage.Commit, 0, len(entries))
	for _, e := range entries {
		commits = append(commits, e.commit)
	}
	return commits, nil
}

type versionEntry struct {
	commit    storage.Commit
	versionID string
	deleted   bool
}

const unknownAuthor = "unknown"

func (b *Backend) versions(ctx context.Context, key string, limit int) ([]versionEntry, error) {
	var entries []versionEntry
	var keyMarker, versionMarker *string

	for page := 0; page < maxVersionPages && len(entries) < limit; page++ {
		out, err := b.api.ListObjectVersions(ctx, &s3.ListObjectVersionsInput{
			Bucket:          aws.String(b.bucket),
			Prefix:          aws.String(key),
			MaxKeys:         aws.Int32(int32(limit)), //nolint:gosec // History clamps limit to 1..1000
			KeyMarker:       keyMarker,
			VersionIdMarker: versionMarker,
		})
		if err != nil {
			return nil, fmt.Errorf("listing versions of s3://%s/%s: %w", b.bucket, key, err)
		}

		for _, v := range out.Versions {
			if aws.ToString(v.Key) != key {
				continue
			}
			id := aws.ToString(v.VersionId)
			entries = append(entries, versionEntry{versionID: id, commit: storage.Commit{
				SHA:     id,
				Message: "object version " + id,
				Author:  unknownAuthor,
				When:    aws.ToTime(v.LastModified),
			}})
		}
		for _, m := range out.DeleteMarkers {
			if aws.ToString(m.Key) != key {
				continue
			}
			id := aws.ToString(m.VersionId)
			entries = append(entries, versionEntry{versionID: id, deleted: true, commit: storage.Commit{
				SHA:     id,
				Message: "object deleted (a delete marker records no author)",
				Author:  unknownAuthor,
				When:    aws.ToTime(m.LastModified),
			}})
		}

		// Keys come back in order, and every key sharing this prefix sorts
		// after it, so once the listing moves past the key it is done.
		if !aws.ToBool(out.IsTruncated) || aws.ToString(out.NextKeyMarker) != key {
			break
		}
		keyMarker, versionMarker = out.NextKeyMarker, out.NextVersionIdMarker
	}

	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].commit.When.After(entries[j].commit.When)
	})
	if len(entries) > limit {
		entries = entries[:limit]
	}
	return entries, nil
}

// attribute fills author, email and message from each version's metadata.
func (b *Backend) attribute(ctx context.Context, key string, entries []versionEntry) error {
	sem := make(chan struct{}, headConcurrency)
	var wg sync.WaitGroup

	for i := range entries {
		if entries[i].deleted {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(e *versionEntry) {
			defer wg.Done()
			defer func() { <-sem }()

			out, err := b.api.HeadObject(ctx, &s3.HeadObjectInput{
				Bucket:    aws.String(b.bucket),
				Key:       aws.String(key),
				VersionId: aws.String(e.versionID),
			})
			if err != nil {
				return
			}
			applyMetadata(&e.commit, out.Metadata)
		}(&entries[i])
	}
	wg.Wait()
	return ctx.Err()
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

func (b *Backend) Check(ctx context.Context) error {
	if _, _, err := b.list(ctx, ""); err != nil && !errors.Is(err, storage.ErrNotFound) {
		return fmt.Errorf("cannot list s3://%s/%s: %w", b.bucket, b.prefix, err)
	}

	out, err := b.api.GetBucketVersioning(ctx, &s3.GetBucketVersioningInput{
		Bucket: aws.String(b.bucket),
	})
	if err == nil {
		b.versioning = out.Status == types.BucketVersioningStatusEnabled
	}
	return nil
}

func (b *Backend) SetVersioning(enabled bool) { b.versioning = enabled }

func etag(raw *string) string {
	return strings.Trim(aws.ToString(raw), `"`)
}

func isNotFound(err error) bool {
	var nsk *types.NoSuchKey
	if errors.As(err, &nsk) {
		return true
	}
	return statusCode(err) == 404
}

func isPreconditionFailed(err error) bool {
	code := statusCode(err)
	return code == 412 || code == 409
}

func statusCode(err error) int {
	var api smithy.APIError
	if !errors.As(err, &api) {
		return 0
	}
	switch api.ErrorCode() {
	case "PreconditionFailed":
		return 412
	case "NoSuchKey", "NotFound":
		return 404
	case "ConditionalRequestConflict":
		return 409
	}
	return 0
}
