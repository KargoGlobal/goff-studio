package analysis

import (
	"context"
	"sync"
	"time"
)

const (
	ResultsTTL = 5 * time.Minute
	maxCached  = 512
)

// Local marks a provider that computes in-process without I/O, so a list page
// may ask it for every experiment instead of only reading the cache.
type Local interface {
	Local() bool
}

// Cache sits in front of any provider. It keeps finished readouts (status
// "ok") for ResultsTTL, shares one fetch between concurrent callers of the
// same key, and stamps method.provider on every readout it returns.
type Cache struct {
	p   Provider
	ttl time.Duration
	now func() time.Time

	mu       sync.Mutex
	cache    map[string]cached
	inflight map[string]*call
}

type cached struct {
	res     *Results
	expires time.Time
}

// call is one in-flight fetch that concurrent requests for the same key share.
type call struct {
	done chan struct{}
	res  *Results
	err  error
}

func NewCache(p Provider) *Cache {
	return &Cache{
		p:        p,
		ttl:      ResultsTTL,
		now:      time.Now,
		cache:    map[string]cached{},
		inflight: map[string]*call{},
	}
}

func (c *Cache) Name() string { return c.p.Name() }

// Results returns a fresh cached readout or asks the provider.
func (c *Cache) Results(ctx context.Context, req ResultsRequest) (*Results, error) {
	key := req.Key + "|" + req.AsOf
	if res, ok := c.cached(key); ok {
		return res, nil
	}
	return c.shared(key, func() (*Results, error) {
		res, err := c.p.Results(ctx, req)
		if err != nil {
			return nil, err
		}
		res.Method.Provider = c.p.Name()
		// Only a finished readout is worth keeping: "error" and "insufficient_data" can change any minute.
		if res.Status == "ok" {
			c.store(key, res)
		}
		return res, nil
	})
}

// Peek returns a readout without waiting on a remote service: the cached one,
// or a fresh one from a Local provider.
func (c *Cache) Peek(ctx context.Context, req ResultsRequest) (*Results, bool) {
	if res, ok := c.cached(req.Key + "|" + req.AsOf); ok {
		return res, true
	}
	if l, ok := c.p.(Local); ok && l.Local() {
		res, err := c.Results(ctx, req)
		return res, err == nil
	}
	return nil, false
}

func (c *Cache) Power(ctx context.Context, req PowerRequest) (*PowerResult, error) {
	res, err := c.p.Power(ctx, req)
	if err != nil {
		return nil, err
	}
	if res.Source == "" {
		res.Source = c.p.Name()
	}
	return res, nil
}

// shared runs fetch once per key at a time; concurrent callers wait for its result.
func (c *Cache) shared(key string, fetch func() (*Results, error)) (*Results, error) {
	c.mu.Lock()
	if inflight, ok := c.inflight[key]; ok {
		c.mu.Unlock()
		<-inflight.done
		return inflight.res, inflight.err
	}
	cl := &call{done: make(chan struct{})}
	c.inflight[key] = cl
	c.mu.Unlock()

	cl.res, cl.err = fetch()

	c.mu.Lock()
	delete(c.inflight, key)
	c.mu.Unlock()
	close(cl.done)
	return cl.res, cl.err
}

func (c *Cache) cached(key string) (*Results, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.cache[key]
	if !ok || c.now().After(entry.expires) {
		return nil, false
	}
	return entry.res, true
}

func (c *Cache) store(key string, res *Results) {
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
	c.cache[key] = cached{res: res, expires: now.Add(c.ttl)}
}
