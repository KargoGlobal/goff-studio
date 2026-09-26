package analysis

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewPicksTheProvider(t *testing.T) {
	cases := []struct {
		cfg     Config
		want    string
		wantErr bool
	}{
		{Config{}, "builtin", false},
		{Config{BaseURL: "https://analysis.example.com"}, "http", false},
		{Config{Provider: "sample"}, "sample", false},
		{Config{Provider: "builtin"}, "builtin", false},
		{Config{Provider: "http"}, "", true},
		{Config{Provider: "magic"}, "", true},
	}
	for _, tc := range cases {
		p, err := New(tc.cfg, time.Now)
		if (err != nil) != tc.wantErr {
			t.Errorf("%+v: err = %v", tc.cfg, err)
			continue
		}
		if err == nil && p.Name() != tc.want {
			t.Errorf("%+v: provider = %s, want %s", tc.cfg, p.Name(), tc.want)
		}
	}
}

func TestBuiltinHasNoResultsButEstimatesPower(t *testing.T) {
	_, err := Builtin{}.Results(context.Background(), ResultsRequest{Key: "x", Spec: spec()})
	if !errors.Is(err, ErrResultsUnsupported) {
		t.Errorf("err = %v", err)
	}
	res, err := Builtin{}.Power(context.Background(), PowerRequest{BaselineMean: 0.4, Variance: 0.24, NPerDay: 1000, Arms: 2, Alpha: 0.05, Power: 0.8})
	if err != nil || res.MDE <= 0 || res.Source != "local" {
		t.Errorf("power = %+v, %v", res, err)
	}
}

func TestCacheStampsTheProvider(t *testing.T) {
	c := NewCache(&Sample{Now: func() time.Time { return spec().Start.Add(72 * time.Hour) }})
	res, err := c.Results(context.Background(), ResultsRequest{Key: "k", Spec: spec()})
	if err != nil || res.Method.Provider != "sample" {
		t.Errorf("provider = %q, %v", res.Method.Provider, err)
	}
}

func TestPeekOnlyWaitsOnLocalProviders(t *testing.T) {
	sample := NewCache(&Sample{Now: func() time.Time { return spec().Start.Add(72 * time.Hour) }})
	if res, ok := sample.Peek(context.Background(), ResultsRequest{Key: "k", Spec: spec()}); !ok || res == nil {
		t.Error("a local provider should answer a peek")
	}
	if _, ok := NewCache(Builtin{}).Peek(context.Background(), ResultsRequest{Key: "k"}); ok {
		t.Error("builtin has nothing to peek at")
	}

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"experiment_key":"k","status":"ok"}`))
	}))
	defer srv.Close()
	remote := NewCache(NewHTTP(srv.URL, "", srv.Client()))
	if _, ok := remote.Peek(context.Background(), ResultsRequest{Key: "k"}); ok || calls.Load() != 0 {
		t.Errorf("a peek must not call a remote service: ok=%v calls=%d", ok, calls.Load())
	}
	if _, err := remote.Results(context.Background(), ResultsRequest{Key: "k"}); err != nil {
		t.Fatal(err)
	}
	if res, ok := remote.Peek(context.Background(), ResultsRequest{Key: "k"}); !ok || res.Method.Provider != "http" {
		t.Error("a cached readout should be peekable")
	}
}
