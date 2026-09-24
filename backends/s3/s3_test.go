package s3

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/go-feature-flag/studio/internal/storage"
)

type apiError struct{ code string }

func (e apiError) Error() string                 { return e.code }
func (e apiError) ErrorCode() string             { return e.code }
func (e apiError) ErrorMessage() string          { return e.code }
func (e apiError) ErrorFault() smithy.ErrorFault { return smithy.FaultClient }

type fakeS3 struct {
	objects    map[string]string
	versioning bool
	puts       int
	mutate     func(reads int)
	reads      int
}

func newFake() *fakeS3 {
	return &fakeS3{objects: map[string]string{
		"production/flags.goff.yaml":  "flag-one:\n  variations:\n    on: true\n  defaultRule:\n    variation: \"on\"\n",
		"production/growth.goff.yaml": "flag-two:\n  variations:\n    on: true\n  defaultRule:\n    variation: \"on\"\n",
		"staging/flags.goff.yaml":     "flag-three:\n  variations:\n    on: true\n  defaultRule:\n    variation: \"on\"\n",
	}}
}

func tag(body string) string {
	sum := md5.Sum([]byte(body))
	return hex.EncodeToString(sum[:])
}

func (f *fakeS3) GetObject(_ context.Context, in *awss3.GetObjectInput, _ ...func(*awss3.Options)) (*awss3.GetObjectOutput, error) {
	f.reads++
	if f.mutate != nil {
		f.mutate(f.reads)
	}

	body, ok := f.objects[aws.ToString(in.Key)]
	if !ok {
		return nil, &types.NoSuchKey{}
	}
	return &awss3.GetObjectOutput{
		Body: io.NopCloser(strings.NewReader(body)),
		ETag: aws.String(`"` + tag(body) + `"`),
	}, nil
}

func (f *fakeS3) PutObject(_ context.Context, in *awss3.PutObjectInput, _ ...func(*awss3.Options)) (*awss3.PutObjectOutput, error) {
	key := aws.ToString(in.Key)
	existing, exists := f.objects[key]

	if in.IfNoneMatch != nil && exists {
		return nil, apiError{code: "PreconditionFailed"}
	}
	if in.IfMatch != nil {
		want := strings.Trim(aws.ToString(in.IfMatch), `"`)
		if !exists || tag(existing) != want {
			return nil, apiError{code: "PreconditionFailed"}
		}
	}

	buf, _ := io.ReadAll(in.Body)
	f.objects[key] = string(buf)
	f.puts++
	return &awss3.PutObjectOutput{ETag: aws.String(`"` + tag(string(buf)) + `"`)}, nil
}

func (f *fakeS3) ListObjectsV2(_ context.Context, in *awss3.ListObjectsV2Input, _ ...func(*awss3.Options)) (*awss3.ListObjectsV2Output, error) {
	prefix := aws.ToString(in.Prefix)
	out := &awss3.ListObjectsV2Output{IsTruncated: aws.Bool(false)}
	seen := map[string]bool{}

	for key := range f.objects {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		rest := strings.TrimPrefix(key, prefix)
		if dir, _, nested := strings.Cut(rest, "/"); nested {
			p := prefix + dir + "/"
			if !seen[p] {
				seen[p] = true
				out.CommonPrefixes = append(out.CommonPrefixes, types.CommonPrefix{Prefix: aws.String(p)})
			}
			continue
		}
		out.Contents = append(out.Contents, types.Object{Key: aws.String(key)})
	}
	return out, nil
}

func (f *fakeS3) ListObjectVersions(_ context.Context, in *awss3.ListObjectVersionsInput, _ ...func(*awss3.Options)) (*awss3.ListObjectVersionsOutput, error) {
	key := aws.ToString(in.Prefix)
	return &awss3.ListObjectVersionsOutput{Versions: []types.ObjectVersion{
		{Key: aws.String(key), VersionId: aws.String("v2"), LastModified: aws.Time(time.Now())},
		{Key: aws.String(key), VersionId: aws.String("v1"), LastModified: aws.Time(time.Now().Add(-time.Hour))},
	}}, nil
}

func (f *fakeS3) GetBucketVersioning(context.Context, *awss3.GetBucketVersioningInput, ...func(*awss3.Options)) (*awss3.GetBucketVersioningOutput, error) {
	if f.versioning {
		return &awss3.GetBucketVersioningOutput{Status: types.BucketVersioningStatusEnabled}, nil
	}
	return &awss3.GetBucketVersioningOutput{}, nil
}

func backend(t *testing.T, f *fakeS3) *Backend {
	t.Helper()
	b := NewWithAPI(f, "my-flags-bucket", "")
	if err := b.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestRegisteredUnderS3(t *testing.T) {
	if !storage.Registered("s3") {
		t.Fatal("importing this package must register the s3 backend")
	}
}

func TestReadFileReturnsETagAsVersion(t *testing.T) {
	f := newFake()
	b := backend(t, f)

	file, err := b.ReadFile(context.Background(), "production/flags.goff.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(file.Content), "flag-one") {
		t.Error("content mismatch")
	}
	if file.Version != tag(f.objects["production/flags.goff.yaml"]) {
		t.Errorf("version should be the unquoted ETag, got %q", file.Version)
	}
	if strings.Contains(file.Version, `"`) {
		t.Error("the ETag's quotes must be stripped")
	}
}

func TestMissingObjectIsNotFound(t *testing.T) {
	b := backend(t, newFake())
	_, err := b.ReadFile(context.Background(), "production/nope.yaml")
	if !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestListing(t *testing.T) {
	b := backend(t, newFake())
	ctx := context.Background()

	dirs, err := b.ListDirectories(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) != 2 || dirs[0] != "production" || dirs[1] != "staging" {
		t.Errorf("directories = %v", dirs)
	}

	files, err := b.ListFiles(ctx, "production")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("files = %v", files)
	}
	for _, f := range files {
		if !strings.HasPrefix(f, "production/") {
			t.Errorf("file %q should keep its full path", f)
		}
	}
}

func TestWriteUsesIfMatchSoAStaleVersionCannotClobber(t *testing.T) {
	f := newFake()
	b := backend(t, f)
	ctx := context.Background()

	before, _ := b.ReadFile(ctx, "production/flags.goff.yaml")

	result, err := b.Write(ctx, storage.ChangeOp{
		Path:        "production/flags.goff.yaml",
		Key:         "flag-one",
		BaseVersion: before.Version,
		Apply: func(current []byte) ([]byte, error) {
			return append(current, []byte("  disable: true\n")...), nil
		},
	}, storage.Identity{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Version == before.Version {
		t.Error("the version should change after a write")
	}
	if !strings.Contains(f.objects["production/flags.goff.yaml"], "disable: true") {
		t.Error("write did not land")
	}
	if f.puts != 1 {
		t.Errorf("puts = %d, want 1", f.puts)
	}
}

func TestSameFlagChangedElsewhereConflicts(t *testing.T) {
	f := newFake()
	b := backend(t, f)
	ctx := context.Background()

	_, err := b.Write(ctx, storage.ChangeOp{
		Path:        "production/flags.goff.yaml",
		BaseVersion: "a-version-that-never-existed",
		Changed:     func([]byte) (bool, error) { return true, nil },
		Apply:       func(current []byte) ([]byte, error) { return append(current, 'x'), nil },
	}, storage.Identity{})

	if !errors.Is(err, storage.ErrConflict) {
		t.Errorf("want ErrConflict, got %v", err)
	}
	if f.puts != 0 {
		t.Error("nothing should have been written on conflict")
	}
}

func TestDifferentFlagChangedRetriesSilently(t *testing.T) {
	f := newFake()
	b := backend(t, f)
	ctx := context.Background()

	before, _ := b.ReadFile(ctx, "production/flags.goff.yaml")

	f.mutate = func(reads int) {
		if reads == 2 {
			f.objects["production/flags.goff.yaml"] += "unrelated:\n  variations:\n    on: true\n"
			f.mutate = nil
		}
	}

	result, err := b.Write(ctx, storage.ChangeOp{
		Path:        "production/flags.goff.yaml",
		BaseVersion: before.Version,
		Changed:     func([]byte) (bool, error) { return false, nil },
		Apply: func(current []byte) ([]byte, error) {
			return append(current, []byte("  disable: true\n")...), nil
		},
	}, storage.Identity{})
	if err != nil {
		t.Fatalf("a change to an unrelated flag should retry silently: %v", err)
	}
	if !result.Retried {
		t.Error("the result should record the retry")
	}

	body := f.objects["production/flags.goff.yaml"]
	if !strings.Contains(body, "disable: true") {
		t.Error("the user's change was lost")
	}
	if !strings.Contains(body, "unrelated:") {
		t.Error("the other writer's change was clobbered")
	}
}

func TestNoOpWriteSkipsThePut(t *testing.T) {
	f := newFake()
	b := backend(t, f)

	_, err := b.Write(context.Background(), storage.ChangeOp{
		Path:  "production/flags.goff.yaml",
		Apply: func(current []byte) ([]byte, error) { return current, nil },
	}, storage.Identity{})
	if err != nil {
		t.Fatal(err)
	}
	if f.puts != 0 {
		t.Error("an unchanged object should not be written")
	}
}

func TestCreateFileRefusesToOverwrite(t *testing.T) {
	f := newFake()
	b := backend(t, f)
	ctx := context.Background()

	if err := b.CreateFile(ctx, "dev/flags.goff.yaml", []byte("# new\n"), "", storage.Identity{}); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.objects["dev/flags.goff.yaml"]; !ok {
		t.Error("object not created")
	}

	err := b.CreateFile(ctx, "dev/flags.goff.yaml", []byte("# again\n"), "", storage.Identity{})
	if err == nil {
		t.Error("creating over an existing object must fail, not silently overwrite")
	}
}

func TestPrefixIsAppliedAndStripped(t *testing.T) {
	f := newFake()
	f.objects = map[string]string{
		"flags/production/app.goff.yaml": "a:\n  variations:\n    on: true\n",
	}
	b := NewWithAPI(f, "my-flags-bucket", "flags")
	ctx := context.Background()

	dirs, err := b.ListDirectories(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) != 1 || dirs[0] != "production" {
		t.Errorf("the prefix should be invisible to callers, got %v", dirs)
	}

	files, _ := b.ListFiles(ctx, "production")
	if len(files) != 1 || files[0] != "production/app.goff.yaml" {
		t.Errorf("files = %v", files)
	}

	if _, err := b.ReadFile(ctx, "production/app.goff.yaml"); err != nil {
		t.Errorf("reading through the prefix failed: %v", err)
	}
}

func TestRefusesPathsOutsideThePrefix(t *testing.T) {
	b := backend(t, newFake())
	ctx := context.Background()

	for _, path := range []string{"../escape.yaml", "production/../../escape.yaml"} {
		if _, err := b.ReadFile(ctx, path); err == nil {
			t.Errorf("ReadFile(%q) should be refused", path)
		}
	}
}

func TestHistoryFollowsBucketVersioning(t *testing.T) {
	f := newFake()
	ctx := context.Background()

	off := backend(t, f)
	if off.Capabilities().History {
		t.Error("without bucket versioning there is no history, so the UI must hide the panel")
	}
	commits, _ := off.History(ctx, "production/flags.goff.yaml", 10)
	if len(commits) != 0 {
		t.Errorf("history should be empty, got %d", len(commits))
	}

	f.versioning = true
	on := backend(t, f)
	if !on.Capabilities().History {
		t.Error("with versioning enabled, object versions can back a history panel")
	}
	commits, err := on.History(ctx, "production/flags.goff.yaml", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 2 || commits[0].SHA != "v2" {
		t.Errorf("commits = %+v", commits)
	}
}

func TestCapabilitiesNeverClaimAttributionOrReview(t *testing.T) {
	f := newFake()
	f.versioning = true
	caps := backend(t, f).Capabilities()

	if caps.Attribution {
		t.Error("s3 objects carry no author, so attribution must be false")
	}
	if caps.Review {
		t.Error("there is no pull request in front of an s3 write, so review must be false")
	}
}

func TestNewRequiresABucket(t *testing.T) {
	if _, err := New(context.Background(), Config{}); err == nil {
		t.Error("a bucket is required")
	}
}

var _ = bytes.NewReader
var _ = fmt.Sprintf
