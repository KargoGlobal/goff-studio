package experiments

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	ResultsTTL     = 5 * time.Minute
	RequestTimeout = 5 * time.Second
	maxBody        = 8 << 20
	maxCached      = 512
)

var (
	ErrUnavailable = errors.New("the analysis service did not answer in time")
	ErrNoResults   = errors.New("the analysis service has no results for this experiment yet")
)

type UpstreamError struct {
	Status  int
	Message string
}

func (e *UpstreamError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("the analysis service returned %d: %s", e.Status, e.Message)
	}
	return fmt.Sprintf("the analysis service returned %d", e.Status)
}

// Client proxies the analysis service and caches results documents in memory.
type Client struct {
	base    string
	token   string
	http    *http.Client
	ttl     time.Duration
	timeout time.Duration
	now     func() time.Time

	mu    sync.Mutex
	cache map[string]cached
}

type cached struct {
	body    []byte
	expires time.Time
}

func NewClient(baseURL, token string, hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{}
	}
	return &Client{
		base:    strings.TrimRight(baseURL, "/"),
		token:   token,
		http:    hc,
		ttl:     ResultsTTL,
		timeout: RequestTimeout,
		now:     time.Now,
		cache:   map[string]cached{},
	}
}

// Results returns the raw results JSON for an experiment, from cache when fresh.
func (c *Client) Results(ctx context.Context, key, asOf string) ([]byte, error) {
	cacheKey := key + "|" + asOf
	if body, ok := c.cached(cacheKey); ok {
		return body, nil
	}

	q := url.Values{"segments": {"true"}}
	if asOf != "" {
		q.Set("as_of", asOf)
	}
	endpoint := fmt.Sprintf("%s/v1/experiments/%s/results?%s", c.base, url.PathEscape(key), q.Encode())

	body, err := c.do(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	c.store(cacheKey, body)
	return body, nil
}

func (c *Client) Power(ctx context.Context, req PowerRequest) ([]byte, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	return c.do(ctx, http.MethodPost, c.base+"/v1/experiments/power", payload)
}

// Cached returns a fresh cached document without calling the service.
func (c *Client) Cached(key, asOf string) ([]byte, bool) {
	return c.cached(key + "|" + asOf)
}

func (c *Client) cached(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.cache[key]
	if !ok || c.now().After(entry.expires) {
		return nil, false
	}
	return entry.body, true
}

func (c *Client) store(key string, body []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if len(c.cache) >= maxCached {
		for k, v := range c.cache {
			if now.After(v.expires) {
				delete(c.cache, k)
			}
		}
		if len(c.cache) >= maxCached {
			c.cache = map[string]cached{}
		}
	}
	c.cache[key] = cached{body: body, expires: now.Add(c.ttl)}
}

func (c *Client) do(ctx context.Context, method, endpoint string, payload []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	var reader io.Reader
	if payload != nil {
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	res, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer func() { _ = res.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(res.Body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}

	switch {
	case res.StatusCode == http.StatusNotFound:
		return nil, ErrNoResults
	case res.StatusCode < 200 || res.StatusCode >= 300:
		return nil, &UpstreamError{Status: res.StatusCode, Message: upstreamMessage(body)}
	}
	if !json.Valid(body) {
		return nil, &UpstreamError{Status: res.StatusCode, Message: "the response was not JSON"}
	}
	return body, nil
}

func upstreamMessage(body []byte) string {
	var parsed struct {
		Error   string `json:"error"`
		Message string `json:"message"`
		Detail  string `json:"detail"`
	}
	if json.Unmarshal(body, &parsed) == nil {
		for _, s := range []string{parsed.Error, parsed.Message, parsed.Detail} {
			if s != "" {
				return truncate(s, 200)
			}
		}
	}
	return ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
