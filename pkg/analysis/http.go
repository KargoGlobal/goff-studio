package analysis

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	RequestTimeout = 5 * time.Second
	maxBody        = 8 << 20
)

var errUpstreamNotFound = errors.New("not found")

// HTTP calls a service implementing the v1 contract (schema/v1). It does no
// caching of its own; wrap it in a Cache.
type HTTP struct {
	base    string
	token   string
	http    *http.Client
	timeout time.Duration
}

func NewHTTP(baseURL, token string, hc *http.Client) *HTTP {
	if hc == nil {
		hc = &http.Client{}
	}
	return &HTTP{
		base:    strings.TrimRight(baseURL, "/"),
		token:   token,
		http:    hc,
		timeout: RequestTimeout,
	}
}

func (c *HTTP) Name() string { return "http" }

// Results fetches GET /v1/experiments/{key}/results?segments=true[&as_of=].
func (c *HTTP) Results(ctx context.Context, req ResultsRequest) (*Results, error) {
	q := url.Values{"segments": {"true"}}
	if req.AsOf != "" {
		q.Set("as_of", req.AsOf)
	}
	endpoint := fmt.Sprintf("%s/v1/experiments/%s/results?%s", c.base, url.PathEscape(req.Key), q.Encode())

	body, err := c.do(ctx, http.MethodGet, endpoint, nil)
	if errors.Is(err, errUpstreamNotFound) {
		return nil, ErrNoResults
	}
	if err != nil {
		return nil, err
	}
	var res Results
	if err := json.Unmarshal(body, &res); err != nil {
		return nil, &UpstreamError{Status: http.StatusOK, Message: "the results document did not match the v1 contract: " + err.Error()}
	}
	return &res, nil
}

// Power calls POST /v1/experiments/power. The service works in percentages
// (mde_pct, target_mde_pct); Studio works in relative fractions.
func (c *HTTP) Power(ctx context.Context, req PowerRequest) (*PowerResult, error) {
	payload, err := json.Marshal(req.V1())
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
	var svc PowerResultV1
	if err := json.Unmarshal(body, &svc); err != nil {
		return nil, &UpstreamError{Status: http.StatusOK, Message: "the power estimate did not match the v1 contract: " + err.Error()}
	}
	out := svc.Result()
	out.Source = "analysis"
	return &out, nil
}

func (c *HTTP) do(ctx context.Context, method, endpoint string, payload []byte) ([]byte, error) {
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
