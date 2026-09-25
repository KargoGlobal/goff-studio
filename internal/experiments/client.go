package experiments

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
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
	ErrTimeout     = errors.New("the analysis service did not answer in time")
	ErrUnreachable = errors.New("the analysis service is unreachable")
	ErrNoResults   = errors.New("the analysis service has no results for this experiment yet")
	// ErrNoPower is a 404 from the power endpoint, which is not about any experiment's data.
	ErrNoPower = errors.New("the analysis service does not offer power estimates")

	errUpstreamNotFound = errors.New("not found")
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

	mu       sync.Mutex
	cache    map[string]cached
	inflight map[string]*call
}

// call is one in-flight fetch that concurrent requests for the same key share.
type call struct {
	done chan struct{}
	body []byte
	err  error
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
		base:     strings.TrimRight(baseURL, "/"),
		token:    token,
		http:     hc,
		ttl:      ResultsTTL,
		timeout:  RequestTimeout,
		now:      time.Now,
		cache:    map[string]cached{},
		inflight: map[string]*call{},
	}
}

// Results returns the raw results JSON for an experiment, from cache when fresh.
func (c *Client) Results(ctx context.Context, key, asOf string) ([]byte, error) {
	cacheKey := key + "|" + asOf
	if body, ok := c.cached(cacheKey); ok {
		return body, nil
	}
	return c.shared(cacheKey, func() ([]byte, error) {
		q := url.Values{"segments": {"true"}}
		if asOf != "" {
			q.Set("as_of", asOf)
		}
		endpoint := fmt.Sprintf("%s/v1/experiments/%s/results?%s", c.base, url.PathEscape(key), q.Encode())

		body, err := c.do(ctx, http.MethodGet, endpoint, nil)
		if errors.Is(err, errUpstreamNotFound) {
			return nil, ErrNoResults
		}
		if err != nil {
			return nil, err
		}
		// Only a finished readout is worth keeping: "error" and "insufficient_data" can change any minute.
		if resultStatus(body) == "ok" {
			c.store(cacheKey, body)
		}
		return body, nil
	})
}

// shared runs fetch once per key at a time; concurrent callers wait for its result.
func (c *Client) shared(key string, fetch func() ([]byte, error)) ([]byte, error) {
	c.mu.Lock()
	if inflight, ok := c.inflight[key]; ok {
		c.mu.Unlock()
		<-inflight.done
		return inflight.body, inflight.err
	}
	cl := &call{done: make(chan struct{})}
	c.inflight[key] = cl
	c.mu.Unlock()

	cl.body, cl.err = fetch()

	c.mu.Lock()
	delete(c.inflight, key)
	c.mu.Unlock()
	close(cl.done)
	return cl.body, cl.err
}

func resultStatus(body []byte) string {
	var doc struct {
		Status string `json:"status"`
	}
	if json.Unmarshal(body, &doc) != nil {
		return ""
	}
	return doc.Status
}

// Power asks the analysis service for an MDE estimate and returns it in
// Studio's PowerResult shape. The service works in percentages
// (mde_pct, target_mde_pct); Studio works in relative fractions.
func (c *Client) Power(ctx context.Context, req PowerRequest) ([]byte, error) {
	payload, err := json.Marshal(servicePowerRequest{
		BaselineMean: req.BaselineMean,
		Variance:     req.Variance,
		NPerDay:      req.NPerDay,
		Arms:         req.Arms,
		Alpha:        req.Alpha,
		Power:        req.Power,
		CUPEDRho2:    req.CUPEDRho2,
		Days:         req.Days,
		TargetMDEPct: req.TargetMDE * 100,
	})
	if err != nil {
		return nil, err
	}
	body, err := c.do(ctx, http.MethodPost, c.base+"/v1/experiments/power", payload)
	if errors.Is(err, errUpstreamNotFound) {
		return nil, ErrNoPower
	}
	if err != nil {
		return nil, err
	}
	var svc servicePowerResult
	if err := json.Unmarshal(body, &svc); err != nil {
		return nil, fmt.Errorf("analysis service returned an unreadable power estimate: %w", err)
	}
	return json.Marshal(svc.toResult())
}

type servicePowerRequest struct {
	BaselineMean float64 `json:"baseline_mean"`
	Variance     float64 `json:"variance"`
	NPerDay      float64 `json:"n_per_day"`
	Arms         int     `json:"arms"`
	Alpha        float64 `json:"alpha"`
	Power        float64 `json:"power"`
	CUPEDRho2    float64 `json:"cuped_rho2"`
	Days         int     `json:"days,omitempty"`
	TargetMDEPct float64 `json:"target_mde_pct,omitempty"`
}

type servicePowerPoint struct {
	Days   int     `json:"days"`
	MDEPct float64 `json:"mde_pct"`
}

type servicePowerResult struct {
	Days         int                 `json:"days"`
	NPerArm      float64             `json:"n_per_arm"`
	MDEPct       float64             `json:"mde_pct"`
	ByWeek       []servicePowerPoint `json:"mde_by_week"`
	DaysToTarget *int                `json:"days_to_target"`
}

func (r servicePowerResult) toResult() PowerResult {
	out := PowerResult{
		MDE:       r.MDEPct / 100,
		Days:      r.Days,
		DaysToMDE: r.DaysToTarget,
		NPerArm:   math.Round(r.NPerArm),
		Source:    "analysis",
	}
	for _, p := range r.ByWeek {
		out.Curve = append(out.Curve, PowerPoint{Days: p.Days, MDE: p.MDEPct / 100})
	}
	return out
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
		return nil, transportError(ctx, err)
	}
	defer func() { _ = res.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(res.Body, maxBody))
	if err != nil {
		return nil, transportError(ctx, err)
	}

	switch {
	case res.StatusCode == http.StatusNotFound:
		return nil, errUpstreamNotFound
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

// transportError separates "too slow" (504) from "could not connect" (502).
func transportError(ctx context.Context, err error) error {
	var netErr net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) ||
		(errors.As(err, &netErr) && netErr.Timeout()) {
		return fmt.Errorf("%w: %v", ErrTimeout, err)
	}
	return fmt.Errorf("%w: %v", ErrUnreachable, err)
}
