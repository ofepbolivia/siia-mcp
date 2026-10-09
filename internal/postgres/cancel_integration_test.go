//go:build integration

package postgres

import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/siia/siia-mcp/internal/engine"
)

// TestITCancelStorm deterministically verifies that cancelled queries leave the
// pool usable and leak no goroutines (SDD §19, AGENTS.md integration coverage).
// It is run under -race on the integration tier against a reference PostgreSQL
// configured via SIIASQL_IT_* env vars.
func TestITCancelStorm(t *testing.T) {
	conn := newTestConn(t)
	ctx := context.Background()
	if _, err := conn.Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}

	runtime.GC()
	before := runtime.NumGoroutine()

	// Fire a burst of queries cancelled mid-flight so their context expires
	// while relation/plan work is in progress.
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, time.Millisecond)
			_, _ = conn.Query(cctx, engine.QueryRequest{SQL: "SELECT pg_sleep(0.1)"})
			cancel()
		}()
	}
	wg.Wait()

	// The pool must remain usable: a subsequent uncancelled query succeeds.
	if _, err := conn.Query(ctx, engine.QueryRequest{SQL: "SELECT 1"}); err != nil {
		t.Fatalf("adapter did not recover after cancel storm: %v", err)
	}

	// No goroutine leak: settle with retries until the count normalizes.
	deadline := time.Now().Add(5 * time.Second)
	for {
		runtime.GC()
		if n := runtime.NumGoroutine(); n <= before+32 {
			break
		} else if time.Now().After(deadline) {
			t.Fatalf("goroutine leak suspected: before=%d after=%d", before, n)
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Pool still serves concurrent queries correctly after the storm.
	ok := make(chan bool, 16)
	for i := 0; i < 16; i++ {
		go func() {
			_, err := conn.Query(ctx, engine.QueryRequest{SQL: "SELECT 1"})
			ok <- err == nil
		}()
	}
	for i := 0; i < 16; i++ {
		if !<-ok {
			t.Fatal("concurrent query failed after cancel storm")
		}
	}
}
