package configmap

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/go-feature-flag/studio/internal/storage"
)

// fakeAPI implements the ConfigMap slice of the Kubernetes API, including
// resourceVersion optimistic concurrency: a PUT whose resourceVersion is
// stale gets 409, and a POST for an existing name gets 409.
type fakeAPI struct {
	mu        sync.Mutex
	namespace string
	maps      map[string]configMap
	rv        int
	puts      int
	gets      int
	tokens    []string
	pageSize  int
	mutate    func(f *fakeAPI, gets int)
}

func newFake() *fakeAPI {
	f := &fakeAPI{namespace: "flags", maps: map[string]configMap{}}
	f.set("goff-production", map[string]string{
		"payments.goff.yaml": "flag-one:\n  variations:\n    on: true\n  defaultRule:\n    variation: \"on\"\n",
		"growth.goff.yaml":   "flag-two:\n  variations:\n    on: true\n  defaultRule:\n    variation: \"on\"\n",
	})
	f.set("goff-staging", map[string]string{"payments.goff.yaml": "flag-three: {}\n"})
	f.set("kube-root-ca.crt", map[string]string{"ca.crt": "-----BEGIN CERTIFICATE-----"})
	f.set("goff-notes", map[string]string{"README": "not flags"})
	return f
}

func (f *fakeAPI) set(name string, data map[string]string) {
	f.rv++
	f.maps[name] = configMap{
		APIVersion: "v1", Kind: "ConfigMap",
		Metadata: objectMeta{Name: name, Namespace: f.namespace, ResourceVersion: strconv.Itoa(f.rv)},
		Data:     data,
	}
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tokens = append(f.tokens, r.Header.Get("Authorization"))

	base := "/api/v1/namespaces/" + f.namespace + "/configmaps"
	name := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, base), "/")
	if !strings.HasPrefix(r.URL.Path, base) {
		http.Error(w, `{"message":"wrong namespace"}`, http.StatusForbidden)
		return
	}

	switch {
	case r.Method == http.MethodGet && name == "":
		names := make([]string, 0, len(f.maps))
		for n := range f.maps {
			names = append(names, n)
		}
		sort.Strings(names)
		start, _ := strconv.Atoi(r.URL.Query().Get("continue"))
		end := len(names)
		if f.pageSize > 0 && start+f.pageSize < end {
			end = start + f.pageSize
		}
		list := configMapList{}
		for _, n := range names[start:end] {
			list.Items = append(list.Items, f.maps[n])
		}
		if end < len(names) {
			list.Metadata.Continue = strconv.Itoa(end)
		}
		writeJSON(w, list)

	case r.Method == http.MethodGet:
		f.gets++
		if f.mutate != nil {
			f.mutate(f, f.gets)
		}
		cm, ok := f.maps[name]
		if !ok {
			http.Error(w, `{"message":"configmaps not found"}`, http.StatusNotFound)
			return
		}
		writeJSON(w, cm)

	case r.Method == http.MethodPut:
		var cm configMap
		_ = json.NewDecoder(r.Body).Decode(&cm)
		cur, ok := f.maps[name]
		if !ok {
			http.Error(w, `{"message":"not found"}`, http.StatusNotFound)
			return
		}
		if cm.Metadata.ResourceVersion != cur.Metadata.ResourceVersion {
			http.Error(w, `{"message":"the object has been modified"}`, http.StatusConflict)
			return
		}
		f.puts++
		f.set(name, cm.Data)
		writeJSON(w, f.maps[name])

	case r.Method == http.MethodPost:
		var cm configMap
		_ = json.NewDecoder(r.Body).Decode(&cm)
		if _, exists := f.maps[cm.Metadata.Name]; exists {
			http.Error(w, `{"message":"already exists"}`, http.StatusConflict)
			return
		}
		f.puts++
		f.set(cm.Metadata.Name, cm.Data)
		w.WriteHeader(http.StatusCreated)
		writeJSON(w, f.maps[cm.Metadata.Name])

	default:
		http.Error(w, `{"message":"unexpected"}`, http.StatusMethodNotAllowed)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func backend(t *testing.T, f *fakeAPI) *Backend {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	b := NewWithClient(srv.Client(), srv.URL, f.namespace, "goff-", func() (string, error) { return "sa-token", nil })
	if err := b.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	return b
}

func data(f *fakeAPI, name, key string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.maps[name].Data[key]
}

func TestRegisteredUnderConfigmap(t *testing.T) {
	if !storage.Registered("configmap") {
		t.Fatal("importing this package must register the configmap backend")
	}
}

func TestReadFileMapsPathsToConfigMapKeys(t *testing.T) {
	f := newFake()
	file, err := backend(t, f).ReadFile(context.Background(), "production/payments.goff.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(file.Content), "flag-one") {
		t.Errorf("content = %q", file.Content)
	}
	if file.Version != f.maps["goff-production"].Metadata.ResourceVersion {
		t.Errorf("version = %q, want the ConfigMap's resourceVersion", file.Version)
	}
	for _, token := range f.tokens {
		if token != "Bearer sa-token" {
			t.Errorf("request sent Authorization %q, want the service account token", token)
		}
	}
}

func TestMissingFilesAndConfigMapsAreNotFound(t *testing.T) {
	b := backend(t, newFake())
	for _, p := range []string{"production/nope.goff.yaml", "nowhere/payments.goff.yaml"} {
		if _, err := b.ReadFile(context.Background(), p); !errors.Is(err, storage.ErrNotFound) {
			t.Errorf("ReadFile(%q): want ErrNotFound, got %v", p, err)
		}
	}
}

func TestRefusesPathsThatAreNotEnvironmentSlashFile(t *testing.T) {
	b := backend(t, newFake())
	for _, p := range []string{"payments.goff.yaml", "production/eu/payments.goff.yaml", "../x/payments.goff.yaml", "production/.."} {
		if _, err := b.ReadFile(context.Background(), p); err == nil || errors.Is(err, storage.ErrNotFound) {
			t.Errorf("ReadFile(%q) should be refused as an invalid path, got %v", p, err)
		}
	}
}

func TestListingShowsOnlyPrefixedConfigMapsWithFlagFiles(t *testing.T) {
	f := newFake()
	f.pageSize = 1 // exercise the continue token
	b := backend(t, f)
	ctx := context.Background()

	dirs, err := b.ListDirectories(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) != 2 || dirs[0] != "production" || dirs[1] != "staging" {
		t.Errorf("environments = %v, want [production staging]; kube-root-ca.crt and goff-notes hold no flag files", dirs)
	}

	sub, err := b.ListDirectories(ctx, "production")
	if err != nil || len(sub) != 0 {
		t.Errorf("an environment has no subdirectories, got %v, %v", sub, err)
	}

	files, err := b.ListFiles(ctx, "production")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files[0] != "production/growth.goff.yaml" || files[1] != "production/payments.goff.yaml" {
		t.Errorf("files = %v", files)
	}
}

func TestWriteUsesResourceVersionSoAStaleVersionCannotClobber(t *testing.T) {
	f := newFake()
	b := backend(t, f)
	ctx := context.Background()

	before, _ := b.ReadFile(ctx, "production/payments.goff.yaml")
	result, err := b.Write(ctx, storage.ChangeOp{
		Path:        "production/payments.goff.yaml",
		Key:         "flag-one",
		BaseVersion: before.Version,
		Apply: func(c []byte) ([]byte, error) {
			return append(c, []byte("  disable: true\n")...), nil
		},
	}, storage.Identity{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Version == before.Version {
		t.Error("the version should change after a write")
	}
	if !strings.Contains(data(f, "goff-production", "payments.goff.yaml"), "disable: true") {
		t.Error("write did not land")
	}
	if data(f, "goff-production", "growth.goff.yaml") == "" {
		t.Error("writing one key must keep the ConfigMap's other keys")
	}

	stale := f.maps["goff-production"]
	stale.Metadata.ResourceVersion = before.Version
	if _, err := b.put(ctx, &stale); !errors.Is(err, errConflict) {
		t.Errorf("a PUT with a stale resourceVersion must conflict, got %v", err)
	}
}

func TestSameFlagChangedElsewhereConflicts(t *testing.T) {
	f := newFake()
	_, err := backend(t, f).Write(context.Background(), storage.ChangeOp{
		Path:        "production/payments.goff.yaml",
		BaseVersion: "a-version-that-never-existed",
		Changed:     func([]byte) (bool, error) { return true, nil },
		Apply:       func(c []byte) ([]byte, error) { return append(c, 'x'), nil },
	}, storage.Identity{})
	if !errors.Is(err, storage.ErrConflict) {
		t.Errorf("want ErrConflict, got %v", err)
	}
	if f.puts != 0 {
		t.Error("nothing should have been written on conflict")
	}
}

func TestAnotherFilesChangeInTheSameEnvironmentRetriesSilently(t *testing.T) {
	f := newFake()
	b := backend(t, f)
	ctx := context.Background()

	before, _ := b.ReadFile(ctx, "production/payments.goff.yaml")
	// Another writer changes growth.goff.yaml between the backend's read and
	// its PUT, which moves the shared resourceVersion.
	f.mutate = func(f *fakeAPI, gets int) {
		if gets == 2 {
			cm := f.maps["goff-production"]
			next := map[string]string{}
			for k, v := range cm.Data {
				next[k] = v
			}
			next["growth.goff.yaml"] += "unrelated: {}\n"
			f.set("goff-production", next)
		}
	}

	result, err := b.Write(ctx, storage.ChangeOp{
		Path:        "production/payments.goff.yaml",
		BaseVersion: before.Version,
		Changed:     func([]byte) (bool, error) { return false, nil },
		Apply: func(c []byte) ([]byte, error) {
			return append(c, []byte("  disable: true\n")...), nil
		},
	}, storage.Identity{})
	if err != nil {
		t.Fatalf("a change to another file should retry silently: %v", err)
	}
	if !result.Retried {
		t.Error("the result should record the retry")
	}
	if !strings.Contains(data(f, "goff-production", "payments.goff.yaml"), "disable: true") {
		t.Error("the user's change was lost")
	}
	if !strings.Contains(data(f, "goff-production", "growth.goff.yaml"), "unrelated") {
		t.Error("the other writer's change was clobbered")
	}
}

func TestNoOpWriteSkipsThePut(t *testing.T) {
	f := newFake()
	_, err := backend(t, f).Write(context.Background(), storage.ChangeOp{
		Path:  "production/payments.goff.yaml",
		Apply: func(c []byte) ([]byte, error) { return c, nil },
	}, storage.Identity{})
	if err != nil {
		t.Fatal(err)
	}
	if f.puts != 0 {
		t.Error("an unchanged file should not be written")
	}
}

func TestCreateFileAddsAKeyOrAConfigMapAndNeverOverwrites(t *testing.T) {
	f := newFake()
	b := backend(t, f)
	ctx := context.Background()

	if err := b.CreateFile(ctx, "production/search.goff.yaml", []byte("# new\n"), "", storage.Identity{}); err != nil {
		t.Fatal(err)
	}
	if data(f, "goff-production", "search.goff.yaml") != "# new\n" || data(f, "goff-production", "payments.goff.yaml") == "" {
		t.Error("a new file should be a new key beside the existing ones")
	}

	if err := b.CreateFile(ctx, "dev/payments.goff.yaml", []byte("# dev\n"), "", storage.Identity{}); err != nil {
		t.Fatal(err)
	}
	if data(f, "goff-dev", "payments.goff.yaml") != "# dev\n" {
		t.Error("a file in a new environment should create its ConfigMap")
	}

	if err := b.CreateFile(ctx, "production/search.goff.yaml", []byte("# again\n"), "", storage.Identity{}); err == nil {
		t.Error("creating over an existing key must fail, not silently overwrite")
	}
	if data(f, "goff-production", "search.goff.yaml") != "# new\n" {
		t.Error("the existing key was overwritten")
	}
}

func TestNoHistoryAndNoCapabilities(t *testing.T) {
	b := backend(t, newFake())
	if caps := b.Capabilities(); caps.History || caps.Attribution || caps.Review {
		t.Errorf("capabilities = %+v, want none: ConfigMaps keep no history", caps)
	}
	if commits, err := b.History(context.Background(), "production/payments.goff.yaml", 10); err != nil || len(commits) != 0 {
		t.Errorf("history = %v, %v", commits, err)
	}
}

func TestAPIErrorsCarryTheServersMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"message":"configmaps is forbidden: User \"system:serviceaccount:flags:studio\" cannot list"}`, http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)
	err := NewWithClient(srv.Client(), srv.URL, "flags", "goff-", nil).Check(context.Background())
	if err == nil || !strings.Contains(err.Error(), "cannot list") {
		t.Errorf("an RBAC error should say what was refused, got %v", err)
	}
}

func TestEveryClientGetsATimeout(t *testing.T) {
	caller := &http.Client{}
	b := NewWithClient(caller, "http://localhost", "flags", "", nil)
	if b.client.Timeout != requestTimeout || caller.Timeout != 0 {
		t.Errorf("timeout = %s (caller's %s), want %s without modifying the caller's client", b.client.Timeout, caller.Timeout, requestTimeout)
	}
}

func TestNewOutsideAClusterNeedsAnAPIServerAndNamespace(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	t.Setenv("KUBERNETES_SERVICE_PORT", "")
	if _, err := New(Config{}); err == nil {
		t.Error("outside a pod, storage.options.apiServer is required")
	}
	if _, err := New(Config{APIServer: "http://127.0.0.1:8001"}); err == nil {
		t.Error("with apiServer, a namespace is required")
	}
	if _, err := New(Config{APIServer: "http://127.0.0.1:8001", Namespace: "flags"}); err != nil {
		t.Errorf("apiServer plus namespace should be enough: %v", err)
	}
}
