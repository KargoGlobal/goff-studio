package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tempBackend(t *testing.T) (*FileBackend, string) {
	t.Helper()
	root := t.TempDir()

	if err := os.MkdirAll(filepath.Join(root, "production"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "flag-one:\n  variations:\n    on: true\n  defaultRule:\n    variation: \"on\"\n"
	if err := os.WriteFile(filepath.Join(root, "production", "flags.goff.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	b, err := NewFileBackend(root)
	if err != nil {
		t.Fatal(err)
	}
	return b, root
}

func TestFileBackendReportsNoHistory(t *testing.T) {
	b, _ := tempBackend(t)
	caps := b.Capabilities()
	if caps.History || caps.Attribution || caps.Review {
		t.Errorf("the file backend cannot offer any of these, so the UI must hide them: %+v", caps)
	}
}

func TestFileBackendReadAndList(t *testing.T) {
	b, _ := tempBackend(t)
	ctx := context.Background()

	dirs, err := b.ListDirectories(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) != 1 || dirs[0] != "production" {
		t.Errorf("directories = %v", dirs)
	}

	files, err := b.ListFiles(ctx, "production")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0] != "production/flags.goff.yaml" {
		t.Errorf("files = %v", files)
	}

	f, err := b.ReadFile(ctx, "production/flags.goff.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(f.Content), "flag-one") {
		t.Error("content mismatch")
	}
	if f.Version == "" {
		t.Error("a version is required for optimistic concurrency")
	}
}

func TestFileBackendWrite(t *testing.T) {
	b, root := tempBackend(t)
	ctx := context.Background()

	before, _ := b.ReadFile(ctx, "production/flags.goff.yaml")

	result, err := b.Write(ctx, ChangeOp{
		Path:        "production/flags.goff.yaml",
		Key:         "flag-one",
		BaseVersion: before.Version,
		Apply: func(current []byte) ([]byte, error) {
			return append(current, []byte("  disable: true\n")...), nil
		},
	}, Identity{Name: "Jane", Email: "jane@x.com"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Version == before.Version {
		t.Error("the version should change after a write")
	}

	raw, _ := os.ReadFile(filepath.Join(root, "production", "flags.goff.yaml"))
	if !strings.Contains(string(raw), "disable: true") {
		t.Errorf("write did not land:\n%s", raw)
	}
}

func TestFileBackendNoOpWriteSkips(t *testing.T) {
	b, _ := tempBackend(t)
	ctx := context.Background()

	before, _ := b.ReadFile(ctx, "production/flags.goff.yaml")
	result, err := b.Write(ctx, ChangeOp{
		Path:        "production/flags.goff.yaml",
		BaseVersion: before.Version,
		Apply:       func(current []byte) ([]byte, error) { return current, nil },
	}, Identity{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Version != before.Version {
		t.Error("an unchanged file should keep its version")
	}
}

func TestFileBackendConflictOnSameFlag(t *testing.T) {
	b, _ := tempBackend(t)
	ctx := context.Background()

	_, err := b.Write(ctx, ChangeOp{
		Path:        "production/flags.goff.yaml",
		BaseVersion: "a-version-that-never-existed",
		Changed:     func([]byte) (bool, error) { return true, nil },
		Apply:       func(current []byte) ([]byte, error) { return append(current, 'x'), nil },
	}, Identity{})

	if !errors.Is(err, ErrConflict) {
		t.Errorf("want ErrConflict, got %v", err)
	}
}

func TestFileBackendRefusesPathsOutsideTheRoot(t *testing.T) {
	b, _ := tempBackend(t)
	ctx := context.Background()

	for _, path := range []string{"../escape.yaml", "production/../../escape.yaml"} {
		if _, err := b.ReadFile(ctx, path); err == nil {
			t.Errorf("ReadFile(%q) should be refused", path)
		}
		if err := b.CreateFile(ctx, path, []byte("x"), "", Identity{}); err == nil {
			t.Errorf("CreateFile(%q) should be refused", path)
		}
	}
}

func TestFileBackendCreateFile(t *testing.T) {
	b, root := tempBackend(t)
	ctx := context.Background()

	if err := b.CreateFile(ctx, "staging/flags.goff.yaml", []byte("# new\n"), "", Identity{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "staging", "flags.goff.yaml")); err != nil {
		t.Errorf("file not created: %v", err)
	}

	if err := b.CreateFile(ctx, "staging/flags.goff.yaml", []byte("# again\n"), "", Identity{}); err == nil {
		t.Error("creating over an existing file should fail")
	}
}

func TestFileBackendHistoryIsEmpty(t *testing.T) {
	b, _ := tempBackend(t)
	commits, err := b.History(context.Background(), "production/flags.goff.yaml", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 0 {
		t.Errorf("the file backend has no history, got %d entries", len(commits))
	}
}

func TestFileBackendCheckDetectsUnwritableRoot(t *testing.T) {
	b, root := tempBackend(t)
	if err := b.Check(context.Background()); err != nil {
		t.Fatalf("a writable root should pass: %v", err)
	}

	if err := os.Chmod(root, 0o500); err != nil {
		t.Skip("cannot chmod in this environment")
	}
	defer func() { _ = os.Chmod(root, 0o755) }()

	if err := b.Check(context.Background()); err == nil {
		t.Error("a read-only root must fail the startup check, not surface on the first save")
	}
}

func TestNewFileBackendValidatesRoot(t *testing.T) {
	if _, err := NewFileBackend(""); err == nil {
		t.Error("an empty root should be rejected")
	}
	if _, err := NewFileBackend("/definitely/not/here"); err == nil {
		t.Error("a missing root should be rejected")
	}

	file := filepath.Join(t.TempDir(), "a-file")
	_ = os.WriteFile(file, []byte("x"), 0o644)
	if _, err := NewFileBackend(file); err == nil {
		t.Error("a file rather than a directory should be rejected")
	}
}
