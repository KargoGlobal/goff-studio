package azblob

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-feature-flag/studio/internal/storage"
)

type fakeVersion struct {
	id   string
	body string
	etag string
	meta map[string]string
	when time.Time
}

// fakeAzure implements API with the same conditional-write rules as the
// service: If-Match on the ETag, If-None-Match: * for creates.
type fakeAzure struct {
	mu         sync.Mutex
	blobs      map[string][]fakeVersion // oldest first; last is current
	versioning bool
	seq        int
	uploads    int
	reads      int
	mutate     func(f *fakeAzure, reads int)
}

func newFake() *fakeAzure {
	f := &fakeAzure{blobs: map[string][]fakeVersion{}}
	f.put("production/flags.goff.yaml", "flag-one:\n  variations:\n    on: true\n  defaultRule:\n    variation: \"on\"\n", nil)
	f.put("production/growth.goff.yaml", "flag-two:\n  variations:\n    on: true\n  defaultRule:\n    variation: \"on\"\n", nil)
	f.put("staging/flags.goff.yaml", "flag-three:\n  variations:\n    on: true\n  defaultRule:\n    variation: \"on\"\n", nil)
	return f
}

func (f *fakeAzure) put(name, body string, meta map[string]string) fakeVersion {
	f.seq++
	when := time.Date(2026, 9, 1, 0, f.seq, 0, 0, time.UTC)
	v := fakeVersion{
		id:   when.Format("2006-01-02T15:04:05.0000000Z"),
		body: body,
		etag: fmt.Sprintf("0x8D%04d", f.seq),
		meta: meta,
		when: when,
	}
	if f.versioning {
		f.blobs[name] = append(f.blobs[name], v)
	} else {
		f.blobs[name] = []fakeVersion{v}
	}
	return v
}

func (f *fakeAzure) current(name string) (fakeVersion, bool) {
	vs := f.blobs[name]
	if len(vs) == 0 {
		return fakeVersion{}, false
	}
	return vs[len(vs)-1], true
}

func (f *fakeAzure) Download(_ context.Context, name string) ([]byte, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	if f.mutate != nil {
		f.mutate(f, f.reads)
	}
	v, ok := f.current(name)
	if !ok {
		return nil, "", errNotFound
	}
	return []byte(v.body), v.etag, nil
}

func (f *fakeAzure) Upload(_ context.Context, name string, content []byte, ifMatch string, meta map[string]string) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cur, exists := f.current(name)
	if ifMatch == "" && exists {
		return "", "", errPreconditionFailed
	}
	if ifMatch != "" && (!exists || cur.etag != ifMatch) {
		return "", "", errPreconditionFailed
	}
	f.uploads++
	v := f.put(name, string(content), meta)
	if !f.versioning {
		return v.etag, "", nil
	}
	return v.etag, v.id, nil
}

func (f *fakeAzure) List(_ context.Context, prefix, delimiter string, versions bool) ([]Item, []string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	names := make([]string, 0, len(f.blobs))
	for name := range f.blobs {
		names = append(names, name)
	}
	sort.Strings(names)

	var items []Item
	var prefixes []string
	seen := map[string]bool{}
	for _, name := range names {
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		if delimiter != "" {
			if i := strings.Index(name[len(prefix):], delimiter); i >= 0 {
				p := name[:len(prefix)+i+1]
				if !seen[p] {
					seen[p] = true
					prefixes = append(prefixes, p)
				}
				continue
			}
		}
		vs := f.blobs[name]
		if !versions {
			vs = vs[len(vs)-1:]
		}
		for _, v := range vs {
			item := Item{Name: name, Modified: v.when}
			if versions {
				item.Metadata = v.meta
				if f.versioning {
					item.VersionID = v.id
				}
			}
			items = append(items, item)
		}
	}
	return items, prefixes, nil
}

func backendWithPrefix(t *testing.T, f *fakeAzure, prefix string) *Backend {
	t.Helper()
	b := NewWithAPI(f, "flags", prefix)
	if err := b.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	return b
}

func backend(t *testing.T, f *fakeAzure) *Backend { return backendWithPrefix(t, f, "") }

func body(f *fakeAzure, name string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, _ := f.current(name)
	return v.body
}

func TestRegisteredUnderAzblob(t *testing.T) {
	if !storage.Registered("azblob") {
		t.Fatal("importing this package must register the azblob backend")
	}
}

func TestReadFileReturnsETagAsVersion(t *testing.T) {
	f := newFake()
	file, err := backend(t, f).ReadFile(context.Background(), "production/flags.goff.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(file.Content), "flag-one") {
		t.Error("content mismatch")
	}
	v, _ := f.current("production/flags.goff.yaml")
	if file.Version != v.etag {
		t.Errorf("version = %q, want the ETag %q", file.Version, v.etag)
	}
}

func TestMissingBlobIsNotFound(t *testing.T) {
	_, err := backend(t, newFake()).ReadFile(context.Background(), "production/nope.yaml")
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
	if len(files) != 2 || files[0] != "production/flags.goff.yaml" {
		t.Errorf("files = %v", files)
	}
}

func TestListDirectoriesReturnsNamesRelativeToDir(t *testing.T) {
	for _, prefix := range []string{"", "flags"} {
		f := &fakeAzure{blobs: map[string][]fakeVersion{}}
		name := "production/eu/flags.goff.yaml"
		if prefix != "" {
			name = prefix + "/" + name
		}
		f.put(name, "a: {}\n", nil)

		dirs, err := backendWithPrefix(t, f, prefix).ListDirectories(context.Background(), "production")
		if err != nil {
			t.Fatal(err)
		}
		if len(dirs) != 1 || dirs[0] != "eu" {
			t.Errorf("prefix %q: dirs = %v, want [eu] like the file and github backends", prefix, dirs)
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
	if !strings.Contains(body(f, "production/flags.goff.yaml"), "disable: true") {
		t.Error("write did not land")
	}
	if f.uploads != 1 {
		t.Errorf("uploads = %d, want 1", f.uploads)
	}

	if _, err := b.upload(ctx, "production/flags.goff.yaml", []byte("x"), before.Version, nil); !errors.Is(err, errPreconditionFailed) {
		t.Errorf("an upload against a stale ETag must fail its precondition, got %v", err)
	}
}

func TestSameFlagChangedElsewhereConflicts(t *testing.T) {
	f := newFake()
	_, err := backend(t, f).Write(context.Background(), storage.ChangeOp{
		Path:        "production/flags.goff.yaml",
		BaseVersion: "an-etag-that-never-existed",
		Changed:     func([]byte) (bool, error) { return true, nil },
		Apply:       func(current []byte) ([]byte, error) { return append(current, 'x'), nil },
	}, storage.Identity{})
	if !errors.Is(err, storage.ErrConflict) {
		t.Errorf("want ErrConflict, got %v", err)
	}
	if f.uploads != 0 {
		t.Error("nothing should have been written on conflict")
	}
}

func TestDifferentFlagChangedRetriesSilently(t *testing.T) {
	f := newFake()
	b := backend(t, f)
	ctx := context.Background()

	before, _ := b.ReadFile(ctx, "production/flags.goff.yaml")
	// Another writer lands after the caller read the file.
	f.mutate = func(f *fakeAzure, reads int) {
		if reads == 2 {
			v, _ := f.current("production/flags.goff.yaml")
			f.put("production/flags.goff.yaml", v.body+"unrelated:\n  variations:\n    on: true\n", nil)
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
	got := body(f, "production/flags.goff.yaml")
	if !strings.Contains(got, "disable: true") || !strings.Contains(got, "unrelated:") {
		t.Errorf("both changes should survive, got %q", got)
	}
}

func TestNoOpWriteSkipsTheUpload(t *testing.T) {
	f := newFake()
	_, err := backend(t, f).Write(context.Background(), storage.ChangeOp{
		Path:  "production/flags.goff.yaml",
		Apply: func(current []byte) ([]byte, error) { return current, nil },
	}, storage.Identity{})
	if err != nil {
		t.Fatal(err)
	}
	if f.uploads != 0 {
		t.Error("an unchanged blob should not be written")
	}
}

func TestCreateFileRefusesToOverwrite(t *testing.T) {
	f := newFake()
	b := backend(t, f)
	ctx := context.Background()

	if err := b.CreateFile(ctx, "dev/flags.goff.yaml", []byte("# new\n"), "", storage.Identity{}); err != nil {
		t.Fatal(err)
	}
	if err := b.CreateFile(ctx, "dev/flags.goff.yaml", []byte("# again\n"), "", storage.Identity{}); err == nil {
		t.Error("creating over an existing blob must fail, not silently overwrite")
	}
	if body(f, "dev/flags.goff.yaml") != "# new\n" {
		t.Error("the existing blob was overwritten")
	}
}

func TestPrefixIsAppliedAndStripped(t *testing.T) {
	f := &fakeAzure{blobs: map[string][]fakeVersion{}}
	f.put("flags/production/app.goff.yaml", "a:\n  variations:\n    on: true\n", nil)
	b := backendWithPrefix(t, f, "flags")
	ctx := context.Background()

	dirs, _ := b.ListDirectories(ctx, "")
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
	for _, p := range []string{"../escape.yaml", "production/../../escape.yaml"} {
		if _, err := b.ReadFile(context.Background(), p); err == nil {
			t.Errorf("ReadFile(%q) should be refused", p)
		}
	}
}

func TestVersioningIsDetectedAndHistoryCarriesAttribution(t *testing.T) {
	ctx := context.Background()

	off := backend(t, newFake())
	if off.Capabilities().History || off.Capabilities().Attribution {
		t.Error("without blob versioning there is no history, so the UI must hide the panel")
	}
	if commits, _ := off.History(ctx, "production/flags.goff.yaml", 10); len(commits) != 0 {
		t.Errorf("history should be empty, got %d", len(commits))
	}

	f := &fakeAzure{blobs: map[string][]fakeVersion{}, versioning: true}
	f.put("production/flags.goff.yaml", "a: {}\n", nil)
	on := backend(t, f)
	caps := on.Capabilities()
	if !caps.History || !caps.Attribution || caps.Review {
		t.Fatalf("capabilities = %+v, want history and attribution, never review", caps)
	}

	who := storage.Identity{Name: "Zoë Example", Email: "zoe@example.com", Subject: "sub-1"}
	_, err := on.Write(ctx, storage.ChangeOp{
		Path:    "production/flags.goff.yaml",
		Key:     "a",
		Message: "[production] a: disabled",
		Apply:   func(c []byte) ([]byte, error) { return append(c, '\n'), nil },
	}, who)
	if err != nil {
		t.Fatal(err)
	}

	commits, err := on.History(ctx, "production/flags.goff.yaml", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 2 {
		t.Fatalf("commits = %+v, want the seeded and the new version", commits)
	}
	if commits[0].Author != "Zoë Example" || commits[0].Email != "zoe@example.com" || commits[0].Message != "[production] a: disabled" {
		t.Errorf("newest = %+v, want the writer's attribution, UTF-8 intact", commits[0])
	}
	if commits[1].Author != unknownAuthor {
		t.Errorf("a version written without metadata should show an unknown author, got %q", commits[1].Author)
	}
}

func TestFirstVersionedWriteTurnsHistoryOn(t *testing.T) {
	// An empty container shows no version IDs at startup, so Check cannot
	// tell; the first upload's version ID must switch history on.
	f := &fakeAzure{blobs: map[string][]fakeVersion{}, versioning: true}
	b := backend(t, f)
	if b.Capabilities().History {
		t.Fatal("nothing listed yet, so versioning is unknown")
	}
	if err := b.CreateFile(context.Background(), "dev/flags.goff.yaml", []byte("# new\n"), "", storage.Identity{}); err != nil {
		t.Fatal(err)
	}
	if !b.Capabilities().History {
		t.Error("an upload that returned a version ID proves versioning is on")
	}
}

func TestHistoryIsNewestFirstAndLimited(t *testing.T) {
	f := &fakeAzure{blobs: map[string][]fakeVersion{}, versioning: true}
	for i := range 15 {
		f.put("production/flags.goff.yaml", "v", map[string]string{metaMessage: fmt.Sprintf("change %d", i)})
	}
	commits, err := backend(t, f).History(context.Background(), "production/flags.goff.yaml", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 3 || commits[0].Message != "change 14" || commits[2].Message != "change 12" {
		t.Errorf("commits = %+v, want changes 14, 13, 12", commits)
	}
}

func TestAttributionUsesValidAzureMetadataKeys(t *testing.T) {
	meta := attributionMetadata(storage.Identity{Name: "Zoë", Email: "z@example.com", Subject: "s"}, "msg")
	total := 0
	for k, v := range meta {
		// Azure metadata names must be valid C# identifiers.
		if strings.ContainsAny(k, "-. ") {
			t.Errorf("metadata key %q is not a valid Azure metadata name", k)
		}
		for i := range len(v) {
			if v[i] > 0x7e {
				t.Errorf("metadata value %q must be ASCII to travel as an HTTP header", v)
				break
			}
		}
		total += len(k) + len(v)
	}
	if total > metadataLimit {
		t.Errorf("metadata is %d bytes, over the %d byte limit", total, metadataLimit)
	}
}

func TestNewRequiresAContainerAndAWayToAuthenticate(t *testing.T) {
	t.Setenv(connectionStringEnv, "")
	if _, err := New(context.Background(), Config{AccountURL: "https://example.blob.core.windows.net"}); err == nil {
		t.Error("a container is required")
	}
	if _, err := New(context.Background(), Config{Container: "flags"}); err == nil {
		t.Error("an account URL or a connection string is required")
	}
}
