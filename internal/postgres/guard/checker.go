package guard

import (
	"container/list"
	"crypto/sha256"
	"sync"
)

// defaultCheckerEntries bounds the guard verdict cache when no explicit size is
// given.
const defaultCheckerEntries = 512

// Checker memoizes guard verdicts for repeated SQL text. Agents typically
// reissue the same parameterized statement many times with different parameter
// values; the verdict depends only on the SQL text (binding values are not part
// of the parsed tree), so reuse within the process is safe. A cached *Result is
// immutable after Check, so it may be shared across callers. Only successful
// verdicts are cached; rejections are re-evaluated fail-closed.
type Checker struct {
	mu      sync.Mutex
	max     int
	lru     *list.List
	entries map[[sha256.Size]byte]*list.Element
}

type checkerEntry struct {
	key    [sha256.Size]byte
	result *Result
}

// NewChecker builds a bounded LRU guard checker. A non-positive size selects
// defaultCheckerEntries.
func NewChecker(maxEntries int) *Checker {
	if maxEntries < 1 {
		maxEntries = defaultCheckerEntries
	}
	return &Checker{
		max:     maxEntries,
		lru:     list.New(),
		entries: map[[sha256.Size]byte]*list.Element{},
	}
}

// Check returns the cached verdict for sql or computes and stores one.
func (c *Checker) Check(sql string) (*Result, error) {
	key := sha256.Sum256([]byte(sql))
	c.mu.Lock()
	if el, ok := c.entries[key]; ok {
		c.lru.MoveToFront(el)
		res := el.Value.(*checkerEntry).result
		c.mu.Unlock()
		return res, nil
	}
	c.mu.Unlock()

	res, err := Check(sql)
	if err != nil {
		return nil, err
	}
	c.store(key, res)
	return res, nil
}

func (c *Checker) store(key [sha256.Size]byte, res *Result) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.entries[key]; ok {
		c.lru.MoveToFront(el)
		return
	}
	el := c.lru.PushFront(&checkerEntry{key: key, result: res})
	c.entries[key] = el
	if c.lru.Len() > c.max {
		if back := c.lru.Back(); back != nil {
			entry := back.Value.(*checkerEntry)
			c.lru.Remove(back)
			delete(c.entries, entry.key)
		}
	}
}

// Len reports the number of cached verdicts.
func (c *Checker) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lru.Len()
}
