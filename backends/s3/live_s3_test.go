//go:build live

package s3

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/go-feature-flag/studio/internal/storage"
)

func liveBackend(t *testing.T) *Backend {
	t.Helper()
	bucket := os.Getenv("LIVE_S3_BUCKET")
	if bucket == "" {
		t.Skip("set LIVE_S3_BUCKET to run against real S3")
	}

	b, err := New(context.Background(), Config{
		Bucket: bucket,
		Region: os.Getenv("LIVE_S3_REGION"),
		Prefix: os.Getenv("LIVE_S3_PREFIX"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Check(context.Background()); err != nil {
		t.Fatalf("startup check failed against real S3: %v", err)
	}
	return b
}

func TestLiveReadAndETag(t *testing.T) {
	b := liveBackend(t)
	ctx := context.Background()

	f, err := b.ReadFile(ctx, "production/flags.goff.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(f.Content), "s3-test-flag") {
		t.Errorf("content = %q", f.Content)
	}
	if f.Version == "" || strings.Contains(f.Version, `"`) {
		t.Errorf("version should be a bare ETag, got %q", f.Version)
	}
	t.Logf("read %d bytes, etag %s", len(f.Content), f.Version)
}

func TestLiveListing(t *testing.T) {
	b := liveBackend(t)
	ctx := context.Background()

	dirs, err := b.ListDirectories(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("environments discovered: %v", dirs)
	found := false
	for _, d := range dirs {
		if d == "production" {
			found = true
		}
	}
	if !found {
		t.Errorf("production not discovered, got %v", dirs)
	}

	files, err := b.ListFiles(ctx, "production")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("files: %v", files)
}

func TestLiveWriteWithRealETagConcurrency(t *testing.T) {
	b := liveBackend(t)
	ctx := context.Background()

	before, err := b.ReadFile(ctx, "production/flags.goff.yaml")
	if err != nil {
		t.Fatal(err)
	}

	result, err := b.Write(ctx, storage.ChangeOp{
		Path:        "production/flags.goff.yaml",
		Key:         "s3-test-flag",
		BaseVersion: before.Version,
		Apply: func(current []byte) ([]byte, error) {
			return append(current, []byte("  disable: true\n")...), nil
		},
	}, storage.Identity{Name: "Jaime", Email: "jane@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote, new etag %s (was %s)", result.Version, before.Version)

	after, _ := b.ReadFile(ctx, "production/flags.goff.yaml")
	if !strings.Contains(string(after.Content), "disable: true") {
		t.Error("the write did not land in S3")
	}
	if after.Version == before.Version {
		t.Error("the ETag should have changed")
	}
}

func TestLiveStaleETagIsRejectedByS3(t *testing.T) {
	b := liveBackend(t)
	ctx := context.Background()

	_, err := b.Write(ctx, storage.ChangeOp{
		Path:        "production/flags.goff.yaml",
		BaseVersion: "00000000000000000000000000000000",
		Changed:     func([]byte) (bool, error) { return true, nil },
		Apply:       func(current []byte) ([]byte, error) { return append(current, 'x'), nil },
	}, storage.Identity{})

	if !errors.Is(err, storage.ErrConflict) {
		t.Errorf("a stale version must conflict rather than clobber, got %v", err)
	}
}

func TestLiveIfMatchActuallyEnforcedByS3(t *testing.T) {
	b := liveBackend(t)
	ctx := context.Background()

	current, err := b.ReadFile(ctx, "production/flags.goff.yaml")
	if err != nil {
		t.Fatal(err)
	}

	_, err = b.put(ctx, "production/flags.goff.yaml", []byte("# clobbered\n"), "ffffffffffffffffffffffffffffffff")
	if !errors.Is(err, errPreconditionFailed) {
		t.Fatalf("S3 must reject a mismatched If-Match with 412; got %v", err)
	}
	t.Log("S3 rejected the mismatched If-Match, so real optimistic concurrency works")

	after, _ := b.ReadFile(ctx, "production/flags.goff.yaml")
	if after.Version != current.Version {
		t.Error("the rejected write must not have changed the object")
	}
}

func TestLiveCreateFileWillNotOverwrite(t *testing.T) {
	b := liveBackend(t)
	ctx := context.Background()

	if err := b.CreateFile(ctx, "staging/flags.goff.yaml", []byte("# created by the live test\n"), "", storage.Identity{}); err != nil {
		t.Fatal(err)
	}

	err := b.CreateFile(ctx, "staging/flags.goff.yaml", []byte("# again\n"), "", storage.Identity{})
	if err == nil {
		t.Error("If-None-Match should stop a second create from overwriting")
	}
	t.Logf("second create refused: %v", err)
}

func TestLiveCapabilitiesReflectRealBucket(t *testing.T) {
	b := liveBackend(t)
	caps := b.Capabilities()
	t.Logf("capabilities against the real bucket: %+v", caps)

	if caps.Attribution || caps.Review {
		t.Error("s3 has neither attribution nor review")
	}
}
