package admission

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestEnterLeaveBalance ensures a full enter/leave cycle releases both tokens.
func TestEnterLeaveBalance(t *testing.T) {
	g := New(2, 0) // active=2, queued=0 -> ingress cap = 2
	for i := 0; i < 10; i++ {
		for j := 0; j < 2; j++ {
			if err := g.Enter(context.Background()); err != nil {
				t.Fatalf("enter %d.%d: %v", i, j, err)
			}
		}
		if !g.Saturate() {
			t.Fatalf("expected saturation after %d enters (cap=2)", i)
		}
		g.Leave()
		g.Leave()
		if g.Saturate() {
			t.Fatalf("gate should not be saturated after both leaves (iter %d)", i)
		}
	}
}

// TestErrBusyWhenSaturated asserts an additional enter is rejected without
// blocking when the process is saturated (SDD §17.1). With active=2, queued=0
// the ingress capacity is 2, so a third enter is rejected.
func TestErrBusyWhenSaturated(t *testing.T) {
	g := New(2, 0) // ingress cap = 2
	if err := g.Enter(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := g.Enter(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := g.Enter(context.Background()); !errors.Is(err, ErrBusy) {
		t.Fatalf("want ErrBusy, got %v", err)
	}
	g.Leave()
	g.Leave()
	if err := g.Enter(context.Background()); err != nil {
		t.Fatalf("enter after leave: %v", err)
	}
}

// TestCancelWhileWaiting asserts that a caller cancelled while waiting for an
// active slot releases its ingress token (no leak) and returns ctx.Err().
func TestCancelWhileWaiting(t *testing.T) {
	g := New(1, 1) // ingress cap = 2
	if err := g.Enter(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Second enter takes the ingress token but must wait for the active slot.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := g.Enter(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want context error, got %v", err)
	}
	// The cancelled waiter released its ingress token, so only the first
	// call's token remains: the gate is not saturated.
	if g.Saturate() {
		t.Fatal("cancelled waiter leaked its ingress token")
	}
	g.Leave()
}

func TestCancelledContextIsNeverAdmitted(t *testing.T) {
	g := New(1, 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := g.Enter(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Enter error = %v, want context.Canceled", err)
	}
	if g.Saturate() {
		t.Fatal("cancelled request consumed an ingress token")
	}
}

// TestActiveBound asserts that at most `active` calls run simultaneously: the
// overflow waits on the active slot rather than running concurrently. The peak
// of simultaneous holders is tracked atomically and must never exceed active.
func TestActiveBound(t *testing.T) {
	g := New(2, 4) // active = 2, ingress cap = 6
	var cur, peak atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := g.Enter(context.Background()); err != nil {
				return
			}
			n := cur.Add(1)
			for {
				m := peak.Load()
				if n <= m || peak.CompareAndSwap(m, n) {
					break
				}
			}
			time.Sleep(time.Millisecond) // hold the slot briefly
			cur.Add(-1)
			g.Leave()
		}()
	}
	wg.Wait()
	if peak.Load() > 2 {
		t.Fatalf("active bound exceeded: peak=%d want <= 2", peak.Load())
	}
}
