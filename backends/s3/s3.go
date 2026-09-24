package s3

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

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
	GetObject(context.Context, *s3.GetObjectInput, ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	PutObject(context.Context, *s3.PutObjectInput, ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	ListObjectsV2(context.Context, *s3.ListObjectsV2Input, ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
	ListObjectVersions(context.Context, *s3.ListObjectVersionsInput, ...func(*s3.Options)) (*s3.ListObjectVersionsOutput, error)
	GetBucketVersioning(context.Context, *s3.GetBucketVersioningInput, ...func(*s3.Options)) (*s3.GetBucketVersioningOutput, error)
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

func (b *Backend) Capabilities() storage.Capabilities {
	return storage.Capabilities{History: b.versioning, Attribution: false, Review: false}
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

func (b *Backend) Write(ctx context.Context, op storage.ChangeOp, _ storage.Identity) (*storage.Result, error) {
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

		version, err := b.put(ctx, op.Path, next, current.Version)
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

func (b *Backend) put(ctx context.Context, path string, content []byte, ifMatch string) (string, error) {
	key, err := b.key(path)
	if err != nil {
		return "", err
	}

	input := &s3.PutObjectInput{
		Bucket:      aws.String(b.bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(content),
		ContentType: aws.String("application/yaml"),
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

func (b *Backend) CreateFile(ctx context.Context, path string, content []byte, _ string, _ storage.Identity) error {
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
	})
	if err != nil {
		if isPreconditionFailed(err) {
			return fmt.Errorf("%s already exists", path)
		}
		return fmt.Errorf("creating s3://%s/%s: %w", b.bucket, key, err)
	}
	return nil
}

func (b *Backend) History(ctx context.Context, path string, limit int) ([]storage.Commit, error) {
	if !b.versioning {
		return nil, nil
	}
	if limit <= 0 {
		limit = 30
	}

	key, err := b.key(path)
	if err != nil {
		return nil, err
	}

	out, err := b.api.ListObjectVersions(ctx, &s3.ListObjectVersionsInput{
		Bucket:  aws.String(b.bucket),
		Prefix:  aws.String(key),
		MaxKeys: aws.Int32(int32(limit)),
	})
	if err != nil {
		return nil, fmt.Errorf("listing versions of s3://%s/%s: %w", b.bucket, key, err)
	}

	var commits []storage.Commit
	for _, v := range out.Versions {
		if aws.ToString(v.Key) != key {
			continue
		}
		commits = append(commits, storage.Commit{
			SHA:     aws.ToString(v.VersionId),
			Message: "object version " + aws.ToString(v.VersionId),
			When:    aws.ToTime(v.LastModified),
		})
	}
	return commits, nil
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
