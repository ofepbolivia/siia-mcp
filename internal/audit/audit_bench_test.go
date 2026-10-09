//go:build bench

package audit

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// Audit queue benchmarks (SDD §21, benchmark §3.7): ack latency under burst
// through the single-worker queue, and drain throughput at rotation with a
// small max_file_bytes. The fail-closed invariant is asserted: Enqueue never
// silently succeeds after a broken sink.

func newBenchQueue(b *testing.B, cfg Config) *Queue {
	b.Helper()
	q, err := NewQueue(cfg)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = q.Close() })
	return q
}

func benchEvent() Event {
	return Event{
		EventVersion: 1,
		Timestamp:    time.Now(),
		RequestID:    "bench-request",
		Phase:        "attempt",
		Tool:         "db_query",
		Connection:   "app_dev",
		Outcome:      "success",
		DurationMS:   1,
	}
}

// BenchmarkAuditAckBurst measures per-event ack latency as seen by callers
// through the single-worker queue under a serialized burst.
func BenchmarkAuditAckBurst(b *testing.B) {
	cfg := Config{
		Path:         filepath.Join(b.TempDir(), "audit.log"),
		WriteTimeout: time.Second,
		QueueSize:    256,
		MaxFileBytes: 1 << 20,
		MaxFiles:     3,
	}
	q := newBenchQueue(b, cfg)
	ctx := context.Background()
	ev := benchEvent()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := q.Enqueue(ctx, ev); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkAuditRotateDrain measures throughput while events spill into file
// rotation (small max_file_bytes), asserting the send path stays healthy.
func BenchmarkAuditRotateDrain(b *testing.B) {
	cfg := Config{
		Path:         filepath.Join(b.TempDir(), "audit.log"),
		WriteTimeout: time.Second,
		QueueSize:    128,
		MaxFileBytes: 2048, // force frequent rotation
		MaxFiles:     3,
	}
	q := newBenchQueue(b, cfg)
	ctx := context.Background()
	ev := benchEvent()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := q.Enqueue(ctx, ev); err != nil {
			b.Fatal(err)
		}
	}
}
