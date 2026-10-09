//go:build bench

package admission

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// fillSaturate parks active+queued holds so every ingress token is occupied.
func fillSaturate(g *Gate, active, queued int) (release func()) {
	hold := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < active+queued; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := g.Enter(context.Background()); err != nil {
				return
			}
			<-hold // park holding the slot
			g.Leave()
		}()
	}
	// Block deterministically until ingress is saturated.
	for !g.Saturate() {
	}
	return func() {
		close(hold)
		wg.Wait()
	}
}

// BenchmarkGateSaturate measures the cost of rejecting an ingress attempt with
// SERVER_BUSY once the process is saturated (SDD §17.1, benchmark §3.6). The
// rejection is a bounded-channel TryAcquire with no waiter accumulation, so
// it stays flat under load — no goroutine or wait-channel growth.
func BenchmarkGateSaturate(b *testing.B) {
	const active, queued = 32, 64 // prod-shaped ingress capacity 96
	g := New(active, queued)
	release := fillSaturate(g, active, queued)
	defer release()
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := g.Enter(ctx); !errors.Is(err, ErrBusy) {
			b.Fatalf("want ErrBusy, got %v", err)
		}
	}
	b.StopTimer()
}

// BenchmarkGateEnterLeave measures the overhead of a full successful
// admission cycle (ingress + active acquire, then release) at low occupancy.
func BenchmarkGateEnterLeave(b *testing.B) {
	const active, queued = 32, 64
	g := New(active, queued)
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := g.Enter(ctx); err != nil {
			b.Fatal(err)
		}
		g.Leave()
	}
}
