package cache

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

func testCfg() Config {
	return Config{TTL: time.Hour, MaxEntries: 1000, MaxBytes: 1 << 20, BudgetTO: time.Hour}
}

func TestHitAndRefreshOnce(t *testing.T) {
	c := New(testCfg())
	calls := 0
	refresh := func(context.Context) (any, error) {
		calls++
		return "v1", nil
	}
	v, fromCache, err := c.Get(context.Background(), "k", refresh)
	if err != nil || v != "v1" || fromCache {
		t.Fatalf("first Get = %v, %v, %v", v, fromCache, err)
	}
	v, fromCache, err = c.Get(context.Background(), "k", refresh)
	if err != nil || !fromCache || v != "v1" {
		t.Fatalf("second Get = %v, %v, %v", v, fromCache, err)
	}
	if calls != 1 {
		t.Fatalf("refresh calls = %d, want 1", calls)
	}
}

func TestCancelledContextCannotReadCacheHit(t *testing.T) {
	c := New(testCfg())
	if _, _, err := c.Get(context.Background(), "k", func(context.Context) (any, error) { return "value", nil }); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := c.Get(ctx, "k", func(context.Context) (any, error) { return "unexpected", nil }); err != context.Canceled {
		t.Fatalf("Get error = %v, want context.Canceled", err)
	}
}

func TestEmptyListIsCached(t *testing.T) {
	c := New(testCfg())
	calls := 0
	refresh := func(context.Context) (any, error) {
		calls++
		return []string{}, nil
	}
	if _, _, err := c.Get(context.Background(), "k", refresh); err != nil {
		t.Fatal(err)
	}
	_, fromCache, err := c.Get(context.Background(), "k", refresh)
	if err != nil || !fromCache {
		t.Fatalf("second Get not cached: %v", err)
	}
	if calls != 1 {
		t.Fatalf("refresh calls = %d, want 1", calls)
	}
}

func TestCoalescingSingleRefresh(t *testing.T) {
	c := New(testCfg())
	start := make(chan struct{})
	calls := 0
	var mu sync.Mutex
	refresh := func(context.Context) (any, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		<-start
		return "shared", nil
	}
	var wg sync.WaitGroup
	results := make([]any, 10)
	errs := make([]error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], _, errs[i] = c.Get(context.Background(), "k", refresh)
		}(i)
	}
	// Wait until the refresh has started (single call) then release it.
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		n := calls
		mu.Unlock()
		if n >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("refresh never started")
		}
		time.Sleep(time.Millisecond)
	}
	close(start)
	wg.Wait()
	mu.Lock()
	total := calls
	mu.Unlock()
	if total != 1 {
		t.Fatalf("refresh calls = %d, want 1 (coalesced)", total)
	}
	for i := range results {
		if errs[i] != nil || results[i] != "shared" {
			t.Fatalf("result %d = %v, %v", i, results[i], errs[i])
		}
	}
}

func TestExpiryTriggersRefresh(t *testing.T) {
	c := New(Config{TTL: 20 * time.Millisecond, MaxEntries: 1000, MaxBytes: 1 << 20, BudgetTO: time.Hour})
	calls := 0
	refresh := func(context.Context) (any, error) {
		calls++
		return calls, nil
	}
	v, _, err := c.Get(context.Background(), "k", refresh)
	if err != nil || v != 1 {
		t.Fatalf("first = %v, %v", v, err)
	}
	time.Sleep(40 * time.Millisecond)
	v, _, err = c.Get(context.Background(), "k", refresh)
	if err != nil || v != 2 {
		t.Fatalf("after expiry = %v, %v", v, err)
	}
}

func TestRefreshFailureDropsEntryAndRetries(t *testing.T) {
	c := New(Config{TTL: 10 * time.Millisecond, MaxEntries: 1000, MaxBytes: 1 << 20, BudgetTO: time.Hour})
	calls := 0
	refresh := func(context.Context) (any, error) {
		calls++
		switch calls {
		case 1:
			return "ok", nil
		case 2:
			return nil, errBoom
		default:
			return "ok", nil
		}
	}
	if _, _, err := c.Get(context.Background(), "k", refresh); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
	if _, _, err := c.Get(context.Background(), "k", refresh); err != errBoom {
		t.Fatalf("expired+failed = %v, want errBoom", err)
	}
	// Wait for the failed flight to drain from inflight so the retry starts a
	// fresh refresh instead of inheriting the already-failing one.
	time.Sleep(20 * time.Millisecond)
	// A later call must retry rather than replay the stored error.
	v, _, err := c.Get(context.Background(), "k", refresh)
	if err != nil || v != "ok" {
		t.Fatalf("retry = %v, %v, want ok", v, err)
	}
}

func TestLastWaiterCancelsRefresh(t *testing.T) {
	c := New(testCfg())
	called := make(chan struct{})
	gotCancel := make(chan error, 1)
	refresh := func(ctx context.Context) (any, error) {
		close(called)
		<-ctx.Done()
		gotCancel <- ctx.Err()
		return nil, context.Canceled
	}
	wctx, wcancel := context.WithCancel(context.Background())
	go func() {
		<-called
		wcancel()
	}()
	_, _, err := c.Get(wctx, "k", refresh)
	if err != context.Canceled {
		t.Fatalf("waiter err = %v, want context.Canceled", err)
	}
	select {
	case cerr := <-gotCancel:
		if cerr != context.Canceled {
			t.Fatalf("refresh cancelled with %v", cerr)
		}
	case <-time.After(time.Second):
		t.Fatal("refresh was not cancelled after last waiter left")
	}
}

func TestLRUEvictionByCount(t *testing.T) {
	c := New(Config{TTL: time.Hour, MaxEntries: 2, MaxBytes: 1 << 20, BudgetTO: time.Hour})
	for i := 0; i < 3; i++ {
		k := string(rune('a' + i))
		if _, _, err := c.Get(context.Background(), k, func(context.Context) (any, error) { return k, nil }); err != nil {
			t.Fatal(err)
		}
	}
	if c.Len() != 2 {
		t.Fatalf("len = %d, want 2", c.Len())
	}
	// 'a' was evicted (LRU); it must be re-fetched.
	v, fromCache, err := c.Get(context.Background(), "a", func(context.Context) (any, error) { return "a-new", nil })
	if err != nil || fromCache || v != "a-new" {
		t.Fatalf("evicted key = %v, %v, %v", v, fromCache, err)
	}
}

func TestLRUEvictionByBytes(t *testing.T) {
	c := New(Config{TTL: time.Hour, MaxEntries: 1000, MaxBytes: 64, BudgetTO: time.Hour})
	for i := 0; i < 5; i++ {
		k := string(rune('a' + i))
		fill := strings.Repeat("x", 32)
		if _, _, err := c.Get(context.Background(), k, func(context.Context) (any, error) { return fill, nil }); err != nil {
			t.Fatal(err)
		}
	}
	if c.Bytes() > 64 {
		t.Fatalf("bytes = %d, exceeds budget 64", c.Bytes())
	}
	if c.Len() > 2 {
		t.Fatalf("len = %d, want <= 2", c.Len())
	}
}

var errBoom = context.DeadlineExceeded
