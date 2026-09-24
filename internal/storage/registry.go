package storage

import (
	"fmt"
	"sort"
	"sync"
)

type Settings struct {
	Backend string
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

func Register(name string, factory Factory) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[name] = factory
}

func Open(s Settings) (Backend, error) {
	registryMu.RLock()
	factory, ok := registry[s.Backend]
	registryMu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("%q is not a known storage backend; built with: %s", s.Backend, joined(Available()))
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

func Registered(name string) bool {
	registryMu.RLock()
	defer registryMu.RUnlock()
	_, ok := registry[name]
	return ok
}

func joined(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	out := names[0]
	for _, n := range names[1:] {
		out += ", " + n
	}
	return out
}
