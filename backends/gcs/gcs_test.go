package gcs

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-feature-flag/studio/internal/storage"
)

type fakeGeneration struct {
	generation int64
	body       string
	meta       map[string]string
	when       time.Time
}

// fakeGCS implements the slice of the GCS JSON API the backend uses.
type fakeGCS struct {
	mu         sync.Mutex
	objects    map[string][]fakeGeneration // oldest first; last is live
	next       int64
	versioning bool
	uploads    int
	reads      int
	mutate     func(f *fakeGCS, reads int)
	pageSize   int // when set, listings return this many items per page
}

func newFake() *fakeGCS {
	f := &fakeGCS{objects: map[string][]fakeGeneration{}, next: 100}
	f.seed("production/flags.goff.yaml", "flag-one:\n  variations:\n    on: true\n  defaultRule:\n    variation: \"on\"\n")
	f.seed("production/growth.goff.yaml", "flag-two:\n  variations:\n    on: true\n  defaultRule:\n    variation: \"on\"\n")
	f.seed("staging/flags.goff.yaml", "flag-three:\n  variations:\n    on: true\n  defaultRule:\n    variation: \"on\"\n")
	return f
}

// seed writes a generation directly; callers outside a request must not hold mu.
func (f *fakeGCS) seed(name, body string) {
	f.put(name, body, nil)
}

func (f *fakeGCS) put(name, body string, meta map[string]string) int64 {
	f.next++
	gen := fakeGeneration{generation: f.next, body: body, meta: meta, when: time.Date(2026, 9, 1, 0, int(f.next-100), 0, 0, time.UTC)}
	if f.versioning {
		f.objects[name] = append(f.objects[name], gen)
	} else {
		f.objects[name] = []fakeGeneration{gen}
	}
	return f.next
}

func (f *fakeGCS) live(name string) (fakeGeneration, bool) {
	gens := f.objects[name]
	if len(gens) == 0 {
		return fakeGeneration{}, false
	}
	return gens[len(gens)-1], true
}

func (f *fakeGCS) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	path := r.URL.EscapedPath()
	q := r.URL.Query()

	switch {
	case r.Method == http.MethodPost && strings.HasPrefix(path, "/upload/storage/v1/b/test-bucket/o"):
		f.upload(w, r)
	case r.Method == http.MethodGet && path == "/storage/v1/b/test-bucket":
		writeJSON(w, map[string]any{"versioning": map[string]bool{"enabled": f.versioning}})
	case r.Method == http.MethodGet && path == "/storage/v1/b/test-bucket/o":
		f.list(w, q)
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/storage/v1/b/test-bucket/o/"):
		name, _ := url.PathUnescape(strings.TrimPrefix(path, "/storage/v1/b/test-bucket/o/"))
		f.reads++
		if f.mutate != nil {
			f.mutate(f, f.reads)
		}
		gen, ok := f.live(name)
		if !ok {
			http.Error(w, "No such object", http.StatusNotFound)
			return
		}
		w.Header().Set("X-Goog-Generation", strconv.FormatInt(gen.generation, 10))
		_, _ = io.WriteString(w, gen.body)
	default:
		http.Error(w, "unexpected "+r.Method+" "+path, http.StatusBadRequest)
	}
}

func (f *fakeGCS) upload(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("uploadType") != "multipart" {
		http.Error(w, "want multipart", http.StatusBadRequest)
		return
	}
	_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	mr := multipart.NewReader(r.Body, params["boundary"])
	first, err := mr.NextPart()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var resource struct {
		Name     string            `json:"name"`
		Metadata map[string]string `json:"metadata"`
	}
	if err := json.NewDecoder(first).Decode(&resource); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	second, err := mr.NextPart()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	body, _ := io.ReadAll(second)

	if match := q.Get("ifGenerationMatch"); match != "" {
		want, _ := strconv.ParseInt(match, 10, 64)
		gen, exists := f.live(resource.Name)
		if (want == 0 && exists) || (want != 0 && (!exists || gen.generation != want)) {
			http.Error(w, "conditionNotMet", http.StatusPreconditionFailed)
			return
		}
	}

	f.uploads++
	g := f.put(resource.Name, string(body), resource.Metadata)
	writeJSON(w, map[string]string{"name": resource.Name, "generation": strconv.FormatInt(g, 10)})
}

func (f *fakeGCS) list(w http.ResponseWriter, q url.Values) {
	prefix, delim := q.Get("prefix"), q.Get("delimiter")
	versions := q.Get("versions") == "true"

	names := make([]string, 0, len(f.objects))
	for name := range f.objects {
		names = append(names, name)
	}
	sort.Strings(names)

	var items []map[string]any
	seen := map[string]bool{}
	var prefixes []string
	for _, name := range names {
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		rest := strings.TrimPrefix(name, prefix)
		if delim != "" {
			if i := strings.Index(rest, delim); i >= 0 {
				p := prefix + rest[:i+1]
				if !seen[p] {
					seen[p] = true
					prefixes = append(prefixes, p)
				}
				continue
			}
		}
		gens := f.objects[name]
		if !versions {
			gens = gens[len(gens)-1:]
		}
		for _, g := range gens {
			items = append(items, map[string]any{
				"name":       name,
				"generation": strconv.FormatInt(g.generation, 10),
				"updated":    g.when.Format(time.RFC3339),
				"metadata":   g.meta,
			})
		}
	}
	resp := map[string]any{"prefixes": prefixes}
	if f.pageSize > 0 {
		start, _ := strconv.Atoi(q.Get("pageToken"))
		end := min(start+f.pageSize, len(items))
		if end < len(items) {
			resp["nextPageToken"] = strconv.Itoa(end)
		}
		items = items[start:end]
	}
	resp["items"] = items
	writeJSON(w, resp)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func backendWithPrefix(t *testing.T, f *fakeGCS, prefix string) *Backend {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	b := NewWithClient(srv.Client(), srv.URL, "test-bucket", prefix)
	if err := b.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	return b
}

func backend(t *testing.T, f *fakeGCS) *Backend { return backendWithPrefix(t, f, "") }

func liveBody(f *fakeGCS, name string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	g, _ := f.live(name)
	return g.body
}

func TestRegisteredUnderGCS(t *testing.T) {
	if !storage.Registered("gcs") {
		t.Fatal("importing this package must register the gcs backend")
	}
}

func TestReadFileReturnsGenerationAsVersion(t *testing.T) {
	f := newFake()
	b := backend(t, f)

	file, err := b.ReadFile(context.Background(), "production/flags.goff.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(file.Content), "flag-one") {
		t.Error("content mismatch")
	}
	g, _ := f.live("production/flags.goff.yaml")
	if file.Version != strconv.FormatInt(g.generation, 10) {
		t.Errorf("version should be the object generation, got %q", file.Version)
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
	if len(files) != 2 || files[0] != "production/flags.goff.yaml" {
		t.Fatalf("files = %v", files)
	}
}

func TestWriteUsesGenerationMatchSoAStaleVersionCannotClobber(t *testing.T) {
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
	if !strings.Contains(liveBody(f, "production/flags.goff.yaml"), "disable: true") {
		t.Error("write did not land")
	}
	if f.uploads != 1 {
		t.Errorf("uploads = %d, want 1", f.uploads)
	}
}

func TestStaleGenerationIsRejectedByTheServer(t *testing.T) {
	f := newFake()
	b := backend(t, f)

	_, err := b.upload(context.Background(), "production/flags.goff.yaml", []byte("x"), "1", nil)
	if !errors.Is(err, errPreconditionFailed) {
		t.Errorf("want errPreconditionFailed, got %v", err)
	}
}

func TestSameFlagChangedElsewhereConflicts(t *testing.T) {
	f := newFake()
	b := backend(t, f)

	_, err := b.Write(context.Background(), storage.ChangeOp{
		Path:        "production/flags.goff.yaml",
		BaseVersion: "1",
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
	f.mutate = func(f *fakeGCS, reads int) {
		if reads == 2 {
			g, _ := f.live("production/flags.goff.yaml")
			f.put("production/flags.goff.yaml", g.body+"unrelated:\n  variations:\n    on: true\n", nil)
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

	body := liveBody(f, "production/flags.goff.yaml")
	if !strings.Contains(body, "disable: true") {
		t.Error("the user's change was lost")
	}
	if !strings.Contains(body, "unrelated:") {
		t.Error("the other writer's change was clobbered")
	}
}

func TestNoOpWriteSkipsTheUpload(t *testing.T) {
	f := newFake()
	b := backend(t, f)

	_, err := b.Write(context.Background(), storage.ChangeOp{
		Path:  "production/flags.goff.yaml",
		Apply: func(current []byte) ([]byte, error) { return current, nil },
	}, storage.Identity{})
	if err != nil {
		t.Fatal(err)
	}
	if f.uploads != 0 {
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
	if liveBody(f, "dev/flags.goff.yaml") != "# new\n" {
		t.Error("object not created")
	}

	err := b.CreateFile(ctx, "dev/flags.goff.yaml", []byte("# again\n"), "", storage.Identity{})
	if err == nil {
		t.Error("creating over an existing object must fail, not silently overwrite")
	}
	if liveBody(f, "dev/flags.goff.yaml") != "# new\n" {
		t.Error("the existing object was overwritten")
	}
}

func TestPrefixIsAppliedAndStripped(t *testing.T) {
	f := &fakeGCS{objects: map[string][]fakeGeneration{}, next: 100}
	f.seed("flags/production/app.goff.yaml", "a:\n  variations:\n    on: true\n")
	b := backendWithPrefix(t, f, "flags")
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

func TestHistoryFollowsObjectVersioningAndCarriesAttribution(t *testing.T) {
	ctx := context.Background()

	off := backend(t, newFake())
	if off.Capabilities().History || off.Capabilities().Attribution {
		t.Error("without object versioning there is no history, so the UI must hide the panel")
	}
	if commits, _ := off.History(ctx, "production/flags.goff.yaml", 10); len(commits) != 0 {
		t.Errorf("history should be empty, got %d", len(commits))
	}

	f := newFake()
	f.versioning = true
	on := backend(t, f)
	caps := on.Capabilities()
	if !caps.History || !caps.Attribution || caps.Review {
		t.Errorf("capabilities = %+v, want history and attribution, never review", caps)
	}

	who := storage.Identity{Name: "Zoë Example", Email: "zoe@example.com", Subject: "sub-1"}
	_, err := on.Write(ctx, storage.ChangeOp{
		Path:    "production/flags.goff.yaml",
		Key:     "flag-one",
		Message: "[production] flag-one: disabled",
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
		t.Fatalf("commits = %+v, want the seeded and the new generation", commits)
	}
	if commits[0].Author != "Zoë Example" || commits[0].Email != "zoe@example.com" || commits[0].Message != "[production] flag-one: disabled" {
		t.Errorf("newest = %+v, want the writer's attribution, UTF-8 intact", commits[0])
	}
	if commits[1].Author != unknownAuthor {
		t.Errorf("a generation written without metadata should show an unknown author, got %q", commits[1].Author)
	}
}

func TestHistoryReadsEveryPageBecauseGCSListsOldestFirst(t *testing.T) {
	f := newFake()
	f.versioning = true
	for i := range 15 {
		f.put("production/flags.goff.yaml", "v", map[string]string{metaMessage: "change " + strconv.Itoa(i)})
	}
	f.pageSize = 1
	b := backend(t, f)

	commits, err := b.History(context.Background(), "production/flags.goff.yaml", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 3 || commits[0].Message != "change 14" {
		t.Errorf("commits = %+v, want the newest three, starting with change 14", commits)
	}
}

func TestAttributionFitsTheMetadataLimit(t *testing.T) {
	long := strings.Repeat("é", 10000)
	meta := attributionMetadata(storage.Identity{Name: long, Email: long, Subject: long}, long)
	total := 0
	for k, v := range meta {
		total += len(k) + len(v)
		if !strings.HasSuffix(v, ellipsis) {
			t.Errorf("%s should be truncated with an ellipsis", k)
		}
	}
	if total > metadataLimit {
		t.Errorf("metadata is %d bytes, over the %d byte limit", total, metadataLimit)
	}
}

func TestNewRequiresABucket(t *testing.T) {
	if _, err := New(context.Background(), Config{}); err == nil {
		t.Error("a bucket is required")
	}
}
