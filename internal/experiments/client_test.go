package experiments

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestResultsAreProxiedAndCached(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/v1/experiments/exp-1/results" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.URL.Query().Get("segments") != "true" || r.URL.Query().Get("as_of") != "2026-10-10" {
			t.Errorf("query = %s", r.URL.RawQuery)
		}
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("auth header = %q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"experiment_key":"exp-1","status":"ok"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL+"/", "secret", srv.Client())
	now := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return now }

	for range 3 {
		body, err := c.Results(context.Background(), "exp-1", "2026-10-10")
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != `{"experiment_key":"exp-1","status":"ok"}` {
			t.Errorf("body = %s", body)
		}
	}
	if calls.Load() != 1 {
		t.Errorf("service called %d times, want 1 (cached)", calls.Load())
	}

	now = now.Add(ResultsTTL + time.Second)
	if _, err := c.Results(context.Background(), "exp-1", "2026-10-10"); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Errorf("an expired entry should be refetched, calls = %d", calls.Load())
	}
}

func TestCacheIsKeyedByAsOf(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "", srv.Client())
	_, _ = c.Results(context.Background(), "exp-1", "")
	_, _ = c.Results(context.Background(), "exp-1", "2026-10-01")
	_, _ = c.Results(context.Background(), "exp-2", "")
	if calls.Load() != 3 {
		t.Errorf("calls = %d, want 3", calls.Load())
	}
	if _, ok := c.Cached("exp-1", ""); !ok {
		t.Error("exp-1 should be cached")
	}
}

func TestUpstreamErrors(t *testing.T) {
	cases := []struct {
		status int
		check  func(error) bool
	}{
		{http.StatusNotFound, func(err error) bool { return errors.Is(err, ErrNoResults) }},
		{http.StatusInternalServerError, func(err error) bool {
			var up *UpstreamError
			return errors.As(err, &up) && up.Status == 500 && up.Message == "boom"
		}},
	}
	for _, tc := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(`{"error":"boom"}`))
		}))
		c := NewClient(srv.URL, "", srv.Client())
		_, err := c.Results(context.Background(), "exp-1", "")
		if !tc.check(err) {
			t.Errorf("status %d gave %v", tc.status, err)
		}
		if _, ok := c.Cached("exp-1", ""); ok {
			t.Errorf("status %d must not be cached", tc.status)
		}
		srv.Close()
	}
}

func TestSlowServiceTimesOut(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)

	c := NewClient(srv.URL, "", srv.Client())
	c.timeout = 50 * time.Millisecond
	start := time.Now()
	_, err := c.Results(context.Background(), "exp-1", "")
	if !errors.Is(err, ErrUnavailable) {
		t.Errorf("err = %v, want ErrUnavailable", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Error("the timeout was not applied")
	}
}

func TestNonJSONIsRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html>proxy error</html>`))
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "", srv.Client())
	var up *UpstreamError
	if _, err := c.Results(context.Background(), "exp-1", ""); !errors.As(err, &up) {
		t.Errorf("err = %v", err)
	}
}

func TestPowerIsProxied(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/experiments/power" {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"mde":0.01}`))
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "", srv.Client())
	body, err := c.Power(context.Background(), PowerRequest{Arms: 2})
	if err != nil || string(body) != `{"mde":0.01}` {
		t.Errorf("body=%s err=%v", body, err)
	}
}
