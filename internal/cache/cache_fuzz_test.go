package cache

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"
)

// FuzzBounds drives the metadata cache with adversarial key/value sizes and
// insertion counts (SDD §20.2 cursor/buffer invariant: never exceed bounds).
// It asserts the two hard invariants: entry count never exceeds MaxEntries and
// the byte counter never exceeds MaxBytes, even as values of unbounded size
// are inserted and evicted.
func FuzzBounds(f *testing.F) {
	seeds := []struct {
		maxEntries, maxBytes, iters, seedVal int
		repeatKeys                           bool
	}{
		{4, 1024, 64, 3, false},
		{1, 16, 16, 7, true},
		{16, 256, 128, 11, false},
	}
	for _, s := range seeds {
		f.Add(s.maxEntries, s.maxBytes, s.iters, s.seedVal, s.repeatKeys)
	}
	f.Fuzz(func(t *testing.T, maxEntries, maxBytes, iters, seedVal int, repeatKeys bool) {
		if maxEntries <= 0 || maxEntries > 1024 {
			return
		}
		if maxBytes <= 0 || maxBytes > (1<<20) {
			return
		}
		if iters < 0 || iters > 10000 {
			return
		}
		if seedVal < 0 {
			return
		}

		c := New(Config{
			TTL:        time.Hour,
			MaxEntries: maxEntries,
			MaxBytes:   maxBytes,
			BudgetTO:   time.Second,
		})
		r := rand.New(rand.NewSource(int64(seedVal)))
		for i := 0; i < iters; i++ {
			key := fmt.Sprintf("k%d", i)
			if repeatKeys {
				key = fmt.Sprintf("k%d", r.Intn(maxEntries+2))
			}
			// Unbounded value size but always finite; refresh returns it and
			// the cache sizes it via JSON marshal.
			val := strings.Repeat("x", r.Intn(1<<10))
			_, _, _ = c.Get(context.Background(), key, func(context.Context) (any, error) { return val, nil })
			if c.Len() > maxEntries {
				t.Fatalf("len %d exceeds MaxEntries %d", c.Len(), maxEntries)
			}
			if c.Bytes() > maxBytes {
				t.Fatalf("bytes %d exceeds MaxBytes %d", c.Bytes(), maxBytes)
			}
		}
	})
}
