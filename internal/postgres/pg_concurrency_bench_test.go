//go:build bench && integration

package postgres

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/siia/siia-mcp/internal/engine"
)

// §3.6 concurrency / cancel-storm benches that do NOT depend on the admission
// gate (SDD §21). They verify and measure: pool reuse holds under concurrency
// (parallel success over the fixed pool), and the adapter recovers after
// in-flight cancellations. The saturation/reject-with-SERVER_BUSY path is
// intentionally absent until the §17.1 admission gate exists.

// BenchmarkConcurrentQuery measures throughput under concurrency over the
// shared pool: success over the fixed pool demonstrates no per-request connect.
func BenchmarkConcurrentQuery(b *testing.B) {
	conn := benchConn(b)
	ctx := context.Background()
	req := engine.QueryRequest{SQL: "SELECT 1"}
	var failed atomic.Int64
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := conn.Query(ctx, req); err != nil {
				failed.Add(1)
			}
		}
	})
	if n := failed.Load(); n > 0 {
		b.Fatalf("%d concurrent queries failed", n)
	}
}

// BenchmarkCancelStorm measures the cost of a short cancellation storm and, as
// a correctness gate, asserts the adapter recovers (a control query succeeds).
// Each iteration fires a small burst of queries cancelled mid-flight. Goroutine
// leak is asserted deterministically in cancel_integration_test.go, not here
// (GC timing is too flaky inside a benchmark).
func BenchmarkCancelStorm(b *testing.B) {
	conn := benchConn(b)
	ctx := context.Background()
	if _, err := conn.Ping(ctx); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var wg sync.WaitGroup
		for g := 0; g < 8; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				cctx, cancel := context.WithTimeout(ctx, time.Millisecond)
				_, _ = conn.Query(cctx, engine.QueryRequest{SQL: "SELECT pg_sleep(0.1)"})
				cancel()
			}()
		}
		wg.Wait()
	}
	if _, err := conn.Query(ctx, engine.QueryRequest{SQL: "SELECT 1"}); err != nil {
		b.Fatalf("adapter did not recover after cancel storm: %v", err)
	}
}
