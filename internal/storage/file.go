package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func init() {
	Register("file", func(s Settings) (Backend, error) { return NewFileBackend(s.Path) })
}

type FileBackend struct {
	root string
}

func NewFileBackend(root string) (*FileBackend, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("a root directory is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", abs, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", abs)
	}
	return &FileBackend{root: abs}, nil
}

func (f *FileBackend) Name() string { return "file" }

func (f *FileBackend) Capabilities() Capabilities {
	return Capabilities{History: false, Attribution: false, Review: false}
}

func (f *FileBackend) resolve(path string) (string, error) {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if part == ".." {
			return "", fmt.Errorf("%q is outside the flag directory", path)
		}
	}

	full := filepath.Join(f.root, filepath.Clean("/"+strings.TrimPrefix(path, "/")))
	if full != f.root && !strings.HasPrefix(full, f.root+string(os.PathSeparator)) {
		return "", fmt.Errorf("%q is outside the flag directory", path)
	}
	return full, nil
}

func version(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:8])
}

func (f *FileBackend) ReadFile(_ context.Context, path string) (*File, error) {
	full, err := f.resolve(path)
	if err != nil {
		return nil, err
	}

	content, err := os.ReadFile(full)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, path)
		}
		return nil, err
	}
	return &File{Path: path, Content: content, Version: version(content)}, nil
}

func (f *FileBackend) ListFiles(_ context.Context, dir string) ([]string, error) {
	full, err := f.resolve(dir)
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(full)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, dir)
		}
		return nil, err
	}

	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
			continue
		}
		if dir == "" {
			out = append(out, name)
			continue
		}
		out = append(out, strings.TrimPrefix(dir, "/")+"/"+name)
	}
	sort.Strings(out)
	return out, nil
}

func (f *FileBackend) ListDirectories(_ context.Context, dir string) ([]string, error) {
	full, err := f.resolve(dir)
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(full)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, dir)
		}
		return nil, err
	}

	var out []string
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

func (f *FileBackend) Write(ctx context.Context, op ChangeOp, _ Identity) (*Result, error) {
	current, err := f.ReadFile(ctx, op.Path)
	if err != nil {
		return nil, err
	}

	if op.BaseVersion != "" && current.Version != op.BaseVersion && op.Changed != nil {
		changed, err := op.Changed(current.Content)
		if err != nil {
			return nil, err
		}
		if changed {
			return nil, ErrConflict
		}
	}

	next, err := op.Apply(current.Content)
	if err != nil {
		return nil, err
	}
	if bytes.Equal(next, current.Content) {
		return &Result{Version: current.Version}, nil
	}

	full, err := f.resolve(op.Path)
	if err != nil {
		return nil, err
	}
	if err := writeAtomic(full, next); err != nil {
		return nil, err
	}

	return &Result{Version: version(next), Retried: op.BaseVersion != "" && current.Version != op.BaseVersion}, nil
}

func (f *FileBackend) CreateFile(_ context.Context, path string, content []byte, _ string, _ Identity) error {
	full, err := f.resolve(path)
	if err != nil {
		return err
	}
	if _, err := os.Stat(full); err == nil {
		return fmt.Errorf("%s already exists", path)
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		return err
	}
	return writeAtomic(full, content)
}

func (f *FileBackend) History(context.Context, string, int) ([]Commit, error) {
	return nil, nil
}

func writeAtomic(path string, content []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, content, 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", tmp, err)
	}
	return os.Rename(tmp, path)
}

func (f *FileBackend) Check(context.Context) error {
	probe := filepath.Join(f.root, ".goff-studio-write-probe")
	if err := os.WriteFile(probe, []byte("ok"), 0o600); err != nil {
		return fmt.Errorf("%s is not writable: %w", f.root, err)
	}
	return os.Remove(probe)
}
