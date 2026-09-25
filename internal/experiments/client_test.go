package experiments

import (
	"context"
	"encoding/json"
	"errors"
	"math"
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
		_, _ = w.Write([]byte(`{"status":"ok"}`))
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
	if !errors.Is(err, ErrTimeout) {
		t.Errorf("err = %v, want ErrTimeout", err)
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

func TestPowerIsTranslated(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/experiments/power" {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode request: %v", err)
		}
		_, _ = w.Write([]byte(`{"days":14,"n_per_arm":9333333.4,"mde_pct":0.175,` +
			`"mde_by_week":[{"days":7,"mde_pct":0.25},{"days":14,"mde_pct":0.175}],"days_to_target":2}`))
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "", srv.Client())
	body, err := c.Power(context.Background(), PowerRequest{
		BaselineMean: 0.4, Variance: 0.24, NPerDay: 2e6, Arms: 3, Alpha: 0.05, Power: 0.8, Days: 14, TargetMDE: 0.005,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got["target_mde_pct"] != 0.5 || got["arms"] != float64(3) {
		t.Errorf("request sent = %v, want target_mde_pct 0.5 and arms 3", got)
	}
	var res PowerResult
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatal(err)
	}
	if math.Abs(res.MDE-0.00175) > 1e-12 || res.Days != 14 || res.NPerArm != 9333333 || res.Source != "analysis" {
		t.Errorf("result = %+v", res)
	}
	if res.DaysToMDE == nil || *res.DaysToMDE != 2 {
		t.Errorf("days to MDE = %v, want 2", res.DaysToMDE)
	}
	if len(res.Curve) != 2 || math.Abs(res.Curve[0].MDE-0.0025) > 1e-12 {
		t.Errorf("curve = %+v", res.Curve)
	}
}

func TestUnreachableServiceIsNotATimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	addr := srv.URL
	srv.Close()

	c := NewClient(addr, "", nil)
	_, err := c.Results(context.Background(), "exp-1", "")
	if !errors.Is(err, ErrUnreachable) || errors.Is(err, ErrTimeout) {
		t.Errorf("err = %v, want ErrUnreachable", err)
	}
}

func TestOnlyFinishedResultsAreCached(t *testing.T) {
	for _, status := range []string{"error", "insufficient_data", "ok"} {
		var calls atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			_, _ = w.Write([]byte(`{"status":"` + status + `"}`))
		}))
		c := NewClient(srv.URL, "", srv.Client())
		for range 2 {
			if _, err := c.Results(context.Background(), "exp-1", ""); err != nil {
				t.Fatal(err)
			}
		}
		want := int32(2)
		if status == "ok" {
			want = 1
		}
		if calls.Load() != want {
			t.Errorf("status %q: service called %d times, want %d", status, calls.Load(), want)
		}
		srv.Close()
	}
}

func TestConcurrentRequestsShareOneFetch(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		<-release
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "", srv.Client())

	const n = 20
	errs := make(chan error, n)
	for range n {
		go func() {
			_, err := c.Results(context.Background(), "exp-1", "2026-10-10")
			errs <- err
		}()
	}
	for calls.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
	close(release)
	for range n {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Errorf("service called %d times for %d concurrent requests, want 1", calls.Load(), n)
	}
}

func TestPowerNotFoundIsNotAboutResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "", srv.Client())
	_, err := c.Power(context.Background(), PowerRequest{BaselineMean: 0.4, Variance: 0.24, NPerDay: 1000, Arms: 2, Alpha: 0.05, Power: 0.8})
	if !errors.Is(err, ErrNoPower) || errors.Is(err, ErrNoResults) {
		t.Errorf("err = %v, want ErrNoPower", err)
	}
}
