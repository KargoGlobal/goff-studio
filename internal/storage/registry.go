package storage

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Kind names match GO Feature Flag's retriever kinds where one exists.
const (
	KindGitHub           = "github"
	KindFile             = "file"
	KindS3               = "s3"
	KindGoogleStorage    = "googleStorage"
	KindAzureBlobStorage = "azureBlobStorage"
	KindConfigMap        = "configmap"
)

var builtinKinds = []string{KindGitHub, KindFile, KindS3, KindGoogleStorage, KindAzureBlobStorage, KindConfigMap}

var legacyKinds = map[string]string{
	"gcs":    KindGoogleStorage,
	"azblob": KindAzureBlobStorage,
}

type Settings struct {
	Kind    string
	Path    string
	Bucket  string
	Region  string
	Prefix  string
	Options map[string]string
}

type Factory func(Settings) (Backend, error)

var (
	registryMu sync.RWMutex
	registry   = map[string]Factory{}
)

func Register(kind string, factory Factory) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[kind] = factory
}

func Canonical(kind string) string {
	kind = strings.TrimSpace(kind)
	if renamed, ok := LegacyKind(kind); ok {
		return renamed
	}
	for _, known := range builtinKinds {
		if strings.EqualFold(kind, known) {
			return known
		}
	}
	registryMu.RLock()
	defer registryMu.RUnlock()
	for known := range registry {
		if strings.EqualFold(kind, known) {
			return known
		}
	}
	return kind
}

func LegacyKind(kind string) (string, bool) {
	renamed, ok := legacyKinds[strings.ToLower(strings.TrimSpace(kind))]
	return renamed, ok
}

func Open(s Settings) (Backend, error) {
	kind := Canonical(s.Kind)
	registryMu.RLock()
	factory, ok := registry[kind]
	registryMu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("%q is not a known storage kind; built with: %s", s.Kind, joined(Available()))
	}
	return factory(s)
}

func Available() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()

	out := make([]string, 0, len(registry))
	for name := range registry {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func Registered(kind string) bool {
	registryMu.RLock()
	defer registryMu.RUnlock()
	_, ok := registry[kind]
	return ok
}

func joined(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}
