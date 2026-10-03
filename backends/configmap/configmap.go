// Package configmap stores flag files in Kubernetes ConfigMaps.
//
// Each environment is one ConfigMap and each team file is one key in it:
// "production/growth.goff.yaml" is key "growth.goff.yaml" of the ConfigMap
// "<prefix>production". That is the shape GO Feature Flag's Kubernetes
// retriever reads, one key of one ConfigMap.
//
// It talks to the Kubernetes API over plain HTTP rather than through
// client-go: the handful of calls an editor needs do not justify client-go's
// dependency tree, and a plain JSON API is easy to fake in tests.
package configmap

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/go-feature-flag/studio/internal/storage"
)

func init() {
	storage.Register(storage.KindConfigMap, func(s storage.Settings) (storage.Backend, error) {
		return New(Config{
			Namespace: s.Options["namespace"],
			Prefix:    s.Prefix,
			APIServer: s.Options["apiServer"],
		})
	})
}

const (
	serviceAccountDir = "/var/run/secrets/kubernetes.io/serviceaccount"
	// requestTimeout bounds each API call, since the server sets no request
	// deadline and a stalled API server would otherwise hang a handler.
	requestTimeout = 30 * time.Second
	// fieldManager names Studio in the ConfigMap's managedFields.
	fieldManager = "goff-studio"
)

type Config struct {
	// Namespace holding the ConfigMaps. Defaults to the pod's own namespace.
	Namespace string
	// Prefix is prepended to the environment name to form the ConfigMap name.
	Prefix string
	// APIServer, when set, is used as is and without credentials; it is for
	// `kubectl proxy` in local development. Otherwise Studio uses the pod's
	// service account.
	APIServer string
}

type Backend struct {
	client    *http.Client
	server    string
	namespace string
	prefix    string
	token     func() (string, error)
}

func New(cfg Config) (*Backend, error) {
	if cfg.APIServer != "" {
		if cfg.Namespace == "" {
			return nil, fmt.Errorf("storage.options.namespace is required with storage.options.apiServer")
		}
		return NewWithClient(&http.Client{}, cfg.APIServer, cfg.Namespace, cfg.Prefix, nil), nil
	}

	host, port := os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT")
	if host == "" || port == "" {
		return nil, fmt.Errorf("not running in a Kubernetes pod; set storage.options.apiServer (for example to a `kubectl proxy` address) and storage.options.namespace")
	}

	namespace := cfg.Namespace
	if namespace == "" {
		raw, err := os.ReadFile(serviceAccountDir + "/namespace")
		if err != nil {
			return nil, fmt.Errorf("reading the pod namespace (set storage.options.namespace instead): %w", err)
		}
		namespace = strings.TrimSpace(string(raw))
	}

	caPEM, err := os.ReadFile(serviceAccountDir + "/ca.crt")
	if err != nil {
		return nil, fmt.Errorf("reading the cluster CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("the cluster CA in %s/ca.crt holds no certificates", serviceAccountDir)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}

	// Projected service account tokens rotate, so read the file per request.
	token := func() (string, error) {
		raw, err := os.ReadFile(serviceAccountDir + "/token")
		if err != nil {
			return "", fmt.Errorf("reading the service account token: %w", err)
		}
		return strings.TrimSpace(string(raw)), nil
	}

	server := "https://" + net.JoinHostPort(host, port)
	return NewWithClient(&http.Client{Transport: transport}, server, namespace, cfg.Prefix, token), nil
}

// NewWithClient uses client as given, except that a client with no timeout
// gets requestTimeout; the caller's client is not modified. token may be nil
// for an API server that needs no credentials.
func NewWithClient(client *http.Client, server, namespace, prefix string, token func() (string, error)) *Backend {
	if client.Timeout == 0 {
		bounded := *client
		bounded.Timeout = requestTimeout
		client = &bounded
	}
	return &Backend{
		client:    client,
		server:    strings.TrimSuffix(server, "/"),
		namespace: namespace,
		prefix:    prefix,
		token:     token,
	}
}

func (b *Backend) Name() string { return storage.KindConfigMap }

// Capabilities are all false: a ConfigMap keeps no history, so there is
// nothing to attribute and no review step.
func (b *Backend) Capabilities() storage.Capabilities { return storage.Capabilities{} }

type configMap struct {
	APIVersion string            `json:"apiVersion"`
	Kind       string            `json:"kind"`
	Metadata   objectMeta        `json:"metadata"`
	Data       map[string]string `json:"data,omitempty"`
}

type objectMeta struct {
	Name            string `json:"name"`
	Namespace       string `json:"namespace,omitempty"`
	ResourceVersion string `json:"resourceVersion,omitempty"`
}

type configMapList struct {
	Items    []configMap `json:"items"`
	Metadata struct {
		Continue string `json:"continue"`
	} `json:"metadata"`
}

var (
	errNotFound = errors.New("configmap not found")
	errConflict = errors.New("configmap changed or already exists")
)

type apiError struct {
	status int
	body   string
}

func (e *apiError) Error() string {
	var status struct {
		Message string `json:"message"`
	}
	if json.Unmarshal([]byte(e.body), &status) == nil && status.Message != "" {
		return fmt.Sprintf("kubernetes API returned %d: %s", e.status, status.Message)
	}
	return fmt.Sprintf("kubernetes API returned %d: %s", e.status, strings.TrimSpace(e.body))
}

func (b *Backend) collectionURL(query url.Values) string {
	u := fmt.Sprintf("%s/api/v1/namespaces/%s/configmaps", b.server, url.PathEscape(b.namespace))
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	return u
}

func (b *Backend) objectURL(name string) string {
	return b.collectionURL(nil) + "/" + url.PathEscape(name)
}

func (b *Backend) do(ctx context.Context, method, u string, body any, into any) error {
	return b.doWithContentType(ctx, method, u, "application/json", body, into)
}

func (b *Backend) doWithContentType(ctx context.Context, method, u, contentType string, body any, into any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", contentType)
	}
	if b.token != nil {
		token, err := b.token()
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := b.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	switch {
	case resp.StatusCode == http.StatusNotFound:
		return errNotFound
	case resp.StatusCode == http.StatusConflict:
		return errConflict
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return &apiError{status: resp.StatusCode, body: string(raw)}
	}
	if into != nil {
		if err := json.Unmarshal(raw, into); err != nil {
			return fmt.Errorf("decoding kubernetes API response: %w", err)
		}
	}
	return nil
}

// split maps "production/growth.goff.yaml" to the ConfigMap and key holding
// it. ConfigMaps are flat, so a file is always exactly one level deep.
func (b *Backend) split(p string) (name, key string, err error) {
	parts := strings.Split(strings.Trim(p, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("%q is not <environment>/<file>; ConfigMaps hold one level of files", p)
	}
	for _, part := range parts {
		if part == "." || part == ".." {
			return "", "", fmt.Errorf("%q is not a valid flag file path", p)
		}
	}
	return b.prefix + parts[0], parts[1], nil
}

func (b *Backend) get(ctx context.Context, name string) (*configMap, error) {
	var cm configMap
	if err := b.do(ctx, http.MethodGet, b.objectURL(name), nil, &cm); err != nil {
		return nil, err
	}
	return &cm, nil
}

// patch sets one key with a JSON merge patch that carries the resourceVersion
// the caller read, and returns the new resourceVersion. A merge patch touches
// only that key, so fields Studio does not model (annotations, labels,
// binaryData, ownerReferences, finalizers) are left exactly as they were, and
// the API server rejects it with 409 if the ConfigMap changed since the read.
func (b *Backend) patch(ctx context.Context, name, resourceVersion, key, value string) (string, error) {
	body := map[string]any{
		"metadata": map[string]string{"resourceVersion": resourceVersion},
		"data":     map[string]string{key: value},
	}
	var out configMap
	u := b.objectURL(name) + "?" + url.Values{"fieldManager": {fieldManager}}.Encode()
	if err := b.doWithContentType(ctx, http.MethodPatch, u, "application/merge-patch+json", body, &out); err != nil {
		return "", err
	}
	return out.Metadata.ResourceVersion, nil
}

func (b *Backend) create(ctx context.Context, cm *configMap) error {
	cm.APIVersion, cm.Kind = "v1", "ConfigMap"
	return b.do(ctx, http.MethodPost, b.collectionURL(url.Values{"fieldManager": {fieldManager}}), cm, nil)
}

// ReadFile returns the key's content. The version is the ConfigMap's
// resourceVersion, which also moves when another file in the same
// environment changes; Write re-checks the flag itself in that case.
func (b *Backend) ReadFile(ctx context.Context, p string) (*storage.File, error) {
	name, key, err := b.split(p)
	if err != nil {
		return nil, err
	}
	cm, err := b.get(ctx, name)
	if err != nil {
		if errors.Is(err, errNotFound) {
			return nil, fmt.Errorf("%w: %s", storage.ErrNotFound, p)
		}
		return nil, fmt.Errorf("reading configmap %s/%s: %w", b.namespace, name, err)
	}
	content, ok := cm.Data[key]
	if !ok {
		return nil, fmt.Errorf("%w: %s", storage.ErrNotFound, p)
	}
	return &storage.File{Path: p, Content: []byte(content), Version: cm.Metadata.ResourceVersion}, nil
}

func (b *Backend) ListFiles(ctx context.Context, dir string) ([]string, error) {
	env := strings.Trim(dir, "/")
	if env == "" || strings.Contains(env, "/") {
		return nil, fmt.Errorf("%w: %s", storage.ErrNotFound, dir)
	}
	cm, err := b.get(ctx, b.prefix+env)
	if err != nil {
		if errors.Is(err, errNotFound) {
			return nil, fmt.Errorf("%w: %s", storage.ErrNotFound, dir)
		}
		return nil, fmt.Errorf("reading configmap %s/%s: %w", b.namespace, b.prefix+env, err)
	}
	var out []string
	for key := range cm.Data {
		if isFlagFile(key) {
			out = append(out, env+"/"+key)
		}
	}
	sort.Strings(out)
	return out, nil
}

// ListDirectories lists environments: ConfigMaps whose name starts with the
// prefix and that hold at least one YAML key, so unrelated ConfigMaps in the
// namespace (kube-root-ca.crt and the like) are never shown. Environments
// have no subdirectories.
func (b *Backend) ListDirectories(ctx context.Context, dir string) ([]string, error) {
	if strings.Trim(dir, "/") != "" {
		return nil, nil
	}

	var out []string
	token := ""
	for {
		query := url.Values{"limit": {"500"}}
		if token != "" {
			query.Set("continue", token)
		}
		var list configMapList
		if err := b.do(ctx, http.MethodGet, b.collectionURL(query), nil, &list); err != nil {
			return nil, fmt.Errorf("listing configmaps in %s: %w", b.namespace, err)
		}
		for _, cm := range list.Items {
			env, ok := strings.CutPrefix(cm.Metadata.Name, b.prefix)
			if !ok || env == "" || strings.HasPrefix(env, ".") {
				continue
			}
			for key := range cm.Data {
				if isFlagFile(key) {
					out = append(out, env)
					break
				}
			}
		}
		if list.Metadata.Continue == "" {
			break
		}
		token = list.Metadata.Continue
	}

	if len(out) == 0 {
		return nil, fmt.Errorf("%w: no ConfigMaps named %s<environment> with flag files in %s", storage.ErrNotFound, b.prefix, b.namespace)
	}
	sort.Strings(out)
	return out, nil
}

func isFlagFile(key string) bool {
	return strings.HasSuffix(key, ".yaml") || strings.HasSuffix(key, ".yml")
}

func (b *Backend) Write(ctx context.Context, op storage.ChangeOp, _ storage.Identity) (*storage.Result, error) {
	name, key, err := b.split(op.Path)
	if err != nil {
		return nil, err
	}

	attempts := op.MaxAttempts
	if attempts <= 0 {
		attempts = 3
	}

	retried := false
	for attempt := 1; attempt <= attempts; attempt++ {
		cm, err := b.get(ctx, name)
		if err != nil {
			if errors.Is(err, errNotFound) {
				return nil, fmt.Errorf("%w: %s", storage.ErrNotFound, op.Path)
			}
			return nil, fmt.Errorf("reading configmap %s/%s: %w", b.namespace, name, err)
		}
		current, ok := cm.Data[key]
		if !ok {
			return nil, fmt.Errorf("%w: %s", storage.ErrNotFound, op.Path)
		}

		if op.BaseVersion != "" && cm.Metadata.ResourceVersion != op.BaseVersion {
			retried = true
			if op.Changed != nil {
				changed, err := op.Changed([]byte(current))
				if err != nil {
					return nil, err
				}
				if changed {
					return nil, storage.ErrConflict
				}
			}
		}

		next, err := op.Apply([]byte(current))
		if err != nil {
			return nil, err
		}
		if bytes.Equal(next, []byte(current)) {
			return &storage.Result{Version: cm.Metadata.ResourceVersion}, nil
		}

		version, err := b.patch(ctx, name, cm.Metadata.ResourceVersion, key, string(next))
		if err == nil {
			return &storage.Result{Version: version, Retried: retried}, nil
		}
		if !errors.Is(err, errConflict) {
			return nil, fmt.Errorf("writing configmap %s/%s: %w", b.namespace, name, err)
		}
		retried = true
	}

	return nil, fmt.Errorf("could not write %s after %d attempts because the configmap kept changing", op.Path, attempts)
}

// CreateFile adds the key, creating the environment's ConfigMap if needed. It
// never overwrites an existing key.
func (b *Backend) CreateFile(ctx context.Context, p string, content []byte, _ string, _ storage.Identity) error {
	name, key, err := b.split(p)
	if err != nil {
		return err
	}

	for attempt := 1; attempt <= 3; attempt++ {
		cm, err := b.get(ctx, name)
		switch {
		case errors.Is(err, errNotFound):
			err = b.create(ctx, &configMap{
				Metadata: objectMeta{Name: name, Namespace: b.namespace},
				Data:     map[string]string{key: string(content)},
			})
		case err != nil:
			return fmt.Errorf("reading configmap %s/%s: %w", b.namespace, name, err)
		default:
			if _, exists := cm.Data[key]; exists {
				return fmt.Errorf("%s already exists", p)
			}
			_, err = b.patch(ctx, name, cm.Metadata.ResourceVersion, key, string(content))
		}
		if err == nil {
			return nil
		}
		if !errors.Is(err, errConflict) {
			return fmt.Errorf("writing configmap %s/%s: %w", b.namespace, name, err)
		}
	}
	return fmt.Errorf("could not create %s because the configmap kept changing", p)
}

// History is always empty: a ConfigMap keeps no history.
func (b *Backend) History(context.Context, string, int) ([]storage.Commit, error) { return nil, nil }

// Check confirms the namespace's ConfigMaps can be listed.
func (b *Backend) Check(ctx context.Context) error {
	var list configMapList
	if err := b.do(ctx, http.MethodGet, b.collectionURL(url.Values{"limit": {"1"}}), nil, &list); err != nil {
		return fmt.Errorf("cannot list configmaps in %s: %w", b.namespace, err)
	}
	return nil
}
