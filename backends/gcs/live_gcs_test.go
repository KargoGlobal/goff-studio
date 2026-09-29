//go:build live

package gcs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-feature-flag/studio/internal/storage"
)

// Run against real GCS (credentials from Application Default Credentials):
//
//	LIVE_GCS_BUCKET=b LIVE_GCS_PREFIX=goff-studio-test go test -tags live -run TestLive -v ./...
//
// or against an emulator by adding LIVE_GCS_ENDPOINT=http://localhost:4443.
func liveBackend(t *testing.T) *Backend {
	t.Helper()
	bucket := os.Getenv("LIVE_GCS_BUCKET")
	if bucket == "" {
		t.Skip("set LIVE_GCS_BUCKET to run against real GCS")
	}
	prefix := os.Getenv("LIVE_GCS_PREFIX")
	b, err := New(context.Background(), Config{
		Bucket:   bucket,
		Prefix:   fmt.Sprintf("%s/run-%d", strings.Trim(prefix, "/"), time.Now().UnixNano()),
		Endpoint: os.Getenv("LIVE_GCS_ENDPOINT"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Check(context.Background()); err != nil {
		t.Fatalf("startup check failed: %v", err)
	}
	return b
}

func TestLiveRoundTrip(t *testing.T) {
	b := liveBackend(t)
	ctx := context.Background()
	path := "production/flags.goff.yaml"
	who := storage.Identity{Name: "Zoë Example", Email: "zoe@example.com", Subject: "sub-1"}

	if err := b.CreateFile(ctx, path, []byte("# seed\n"), "create", who); err != nil {
		t.Fatal(err)
	}
	if err := b.CreateFile(ctx, path, []byte("# again\n"), "create", who); err == nil {
		t.Fatal("CreateFile overwrote an existing object")
	}

	dirs, err := b.ListDirectories(ctx, "")
	if err != nil || len(dirs) != 1 || dirs[0] != "production" {
		t.Fatalf("dirs = %v, %v", dirs, err)
	}
	files, err := b.ListFiles(ctx, "production")
	if err != nil || len(files) != 1 || files[0] != path {
		t.Fatalf("files = %v, %v", files, err)
	}

	before, err := b.ReadFile(ctx, path)
	if err != nil || before.Version == "" {
		t.Fatalf("read = %+v, %v", before, err)
	}

	res, err := b.Write(ctx, storage.ChangeOp{
		Path: path, Key: "flag", BaseVersion: before.Version, Message: "[production] flag: enabled",
		Apply: func(c []byte) ([]byte, error) { return append(c, "flag: {}\n"...), nil },
	}, who)
	if err != nil || res.Version == before.Version {
		t.Fatalf("write = %+v, %v", res, err)
	}

	// The server itself must refuse a stale generation.
	if _, err := b.upload(ctx, path, []byte("stale\n"), before.Version, nil); !errors.Is(err, errPreconditionFailed) {
		t.Fatalf("stale upload: want precondition failure, got %v", err)
	}

	after, _ := b.ReadFile(ctx, path)
	if string(after.Content) != "# seed\nflag: {}\n" {
		t.Fatalf("content = %q", after.Content)
	}

	t.Logf("versioning=%v", b.versioning)
	if b.versioning {
		commits, err := b.History(ctx, path, 10)
		if err != nil || len(commits) < 2 {
			t.Fatalf("history = %+v, %v", commits, err)
		}
		if commits[0].Author != "Zoë Example" || commits[0].Message != "[production] flag: enabled" {
			t.Errorf("newest = %+v", commits[0])
		}
	}
}
