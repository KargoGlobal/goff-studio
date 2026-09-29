//go:build live

package azblob

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/bloberror"
	"github.com/go-feature-flag/studio/internal/storage"
)

// Run against real Azure (DefaultAzureCredential) or Azurite:
//
//	LIVE_AZBLOB_CONTAINER=flags LIVE_AZBLOB_ACCOUNT_URL=https://acct.blob.core.windows.net \
//	  go test -tags live -run TestLive -v ./...
//
// For Azurite, set AZURE_STORAGE_CONNECTION_STRING instead of the account URL.
// Every run writes under its own run-<timestamp> prefix.
func liveBackend(t *testing.T) *Backend {
	t.Helper()
	name := os.Getenv("LIVE_AZBLOB_CONTAINER")
	if name == "" {
		t.Skip("set LIVE_AZBLOB_CONTAINER to run against Azure or Azurite")
	}
	cfg := Config{
		Container:  name,
		Prefix:     fmt.Sprintf("%s/run-%d", strings.Trim(os.Getenv("LIVE_AZBLOB_PREFIX"), "/"), time.Now().UnixNano()),
		AccountURL: os.Getenv("LIVE_AZBLOB_ACCOUNT_URL"),
	}
	b, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("LIVE_AZBLOB_CREATE_CONTAINER") != "" {
		_, err := b.api.(*sdkAPI).client.Create(context.Background(), nil)
		if err != nil && !bloberror.HasCode(err, bloberror.ContainerAlreadyExists) {
			t.Fatal(err)
		}
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
		t.Fatal("CreateFile overwrote an existing blob")
	}
	if err := b.CreateFile(ctx, "production/eu/flags.goff.yaml", []byte("# eu\n"), "create", who); err != nil {
		t.Fatal(err)
	}

	dirs, err := b.ListDirectories(ctx, "")
	if err != nil || len(dirs) != 1 || dirs[0] != "production" {
		t.Fatalf("dirs = %v, %v", dirs, err)
	}
	sub, err := b.ListDirectories(ctx, "production")
	if err != nil || len(sub) != 1 || sub[0] != "eu" {
		t.Fatalf("subdirectories = %v, %v", sub, err)
	}
	files, err := b.ListFiles(ctx, "production")
	if err != nil || len(files) != 1 || files[0] != path {
		t.Fatalf("files = %v, %v", files, err)
	}

	before, err := b.ReadFile(ctx, path)
	if err != nil || before.Version == "" {
		t.Fatalf("read = %+v, %v", before, err)
	}
	if _, err := b.ReadFile(ctx, "production/missing.yaml"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("missing blob: want ErrNotFound, got %v", err)
	}

	res, err := b.Write(ctx, storage.ChangeOp{
		Path: path, Key: "flag", BaseVersion: before.Version, Message: "[production] flag: enabled",
		Apply: func(c []byte) ([]byte, error) { return append(c, "flag: {}\n"...), nil },
	}, who)
	if err != nil || res.Version == before.Version {
		t.Fatalf("write = %+v, %v", res, err)
	}

	// The service itself must refuse a stale ETag.
	if _, err := b.upload(ctx, path, []byte("stale\n"), before.Version, nil); !errors.Is(err, errPreconditionFailed) {
		t.Fatalf("stale upload: want a precondition failure, got %v", err)
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
