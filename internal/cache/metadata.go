// Package cache provides the process-local metadata cache (SDD §16): TTL plus
// LRU eviction by entry count and bytes, with single-flight refresh and
// last-waiter cancellation. No background refresh, polling, or stale serving.
package cache

import (
	"container/list"
	"context"
	"encoding/json"
	"sync"
	"time"
)

// Config holds the cache bounds. All fields must be positive.
type Config struct {
	TTL        time.Duration
	MaxEntries int
	MaxBytes   int
	BudgetTO   time.Duration
}

type entry struct {
	value   any
	size    int
	expires time.Time
	elem    *list.Element
}

type flight struct {
	key     string
	done    chan struct{}
	value   any
	err     error
	waiters int
	cancel  context.CancelFunc
}

// Cache is safe for concurrent use.
type Cache struct {
	mu       sync.Mutex
	cfg      Config
	entries  map[string]*entry
	lru      *list.List
	inflight map[string]*flight
	bytes    int
}

func New(cfg Config) *Cache {
	return &Cache{
		cfg:      cfg,
		entries:  map[string]*entry{},
		lru:      list.New(),
		inflight: map[string]*flight{},
	}
}

// Get returns the cached value for key, or refreshes it through refresh.
// Waiters share a single refresh per key; each waits on its own ctx and the
// refresh is cancelled as soon as the last waiter leaves (SDD §16.3). The
// refresh runs on a budget context derived from Background with BudgetTO, not
// on the first waiter's context.
func (c *Cache) Get(ctx context.Context, key string, refresh func(context.Context) (any, error)) (value any, fromCache bool, err error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	c.mu.Lock()
	if e, ok := c.entries[key]; ok && time.Now().Before(e.expires) {
		c.lru.MoveToFront(e.elem)
		c.mu.Unlock()
		return e.value, true, nil
	}

	var f *flight
	if existing, ok := c.inflight[key]; ok {
		f = existing
	} else {
		budget, cancel := context.WithTimeout(context.Background(), c.cfg.BudgetTO)
		f = &flight{key: key, done: make(chan struct{}), cancel: cancel}
		c.inflight[key] = f
		go c.refresh(f, budget, refresh)
	}
	f.waiters++
	c.mu.Unlock()

	select {
	case <-f.done:
		c.leave(f)
		return f.value, false, f.err
	case <-ctx.Done():
		c.leave(f)
		return nil, false, ctx.Err()
	}
}

// refresh stores a fresh authorized result or, on failure, drops any existing
// entry so stale data is never served after an error (SDD §16.3).
func (c *Cache) refresh(f *flight, budget context.Context, do func(context.Context) (any, error)) {
	val, err := do(budget)
	c.mu.Lock()
	defer c.mu.Unlock()
	if err == nil {
		raw, _ := json.Marshal(val)
		c.storeLocked(f.key, &entry{value: val, size: len(raw), expires: time.Now().Add(c.cfg.TTL)})
	} else {
		if old, ok := c.entries[f.key]; ok {
			delete(c.entries, f.key)
			c.bytes -= old.size
			c.lru.Remove(old.elem)
		}
	}
	f.value, f.err = val, err
	close(f.done)
	delete(c.inflight, f.key)
}

// leave releases a waiter; when the last one leaves while a refresh is still
// running, the shared budget context is cancelled (SDD §16.3).
func (c *Cache) leave(f *flight) {
	c.mu.Lock()
	defer c.mu.Unlock()
	select {
	case <-f.done:
		return
	default:
	}
	f.waiters--
	if f.waiters <= 0 {
		f.cancel()
	}
}

// storeLocked inserts/replaces an entry and enforces LRU bounds by count and
// bytes (SDD §16.2). Callers must hold c.mu.
func (c *Cache) storeLocked(key string, e *entry) {
	if old, ok := c.entries[key]; ok {
		c.lru.Remove(old.elem)
		c.bytes -= old.size
	}
	e.elem = c.lru.PushFront(key)
	c.entries[key] = e
	c.bytes += e.size
	for c.lru.Len() > c.cfg.MaxEntries || c.bytes > c.cfg.MaxBytes {
		back := c.lru.Back()
		if back == nil {
			break
		}
		k := back.Value.(string)
		old := c.entries[k]
		c.lru.Remove(back)
		delete(c.entries, k)
		c.bytes -= old.size
	}
}

// Len returns the number of cached entries.
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// Bytes returns the current approximate cached payload size.
func (c *Cache) Bytes() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.bytes
}
