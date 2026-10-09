package requestctx

import (
	"context"
	"testing"
	"time"
)

func TestCleanupContextUsesReservedCutoffAfterCancellation(t *testing.T) {
	work, cancelWork := context.WithCancel(context.Background())
	cutoff := time.Now().Add(500 * time.Millisecond)
	work = WithCleanupDeadline(work, cutoff)
	cancelWork()

	cleanup, cancelCleanup := CleanupContext(work)
	defer cancelCleanup()
	if err := cleanup.Err(); err != nil {
		t.Fatalf("cleanup context error = %v, want active context", err)
	}
	got, ok := cleanup.Deadline()
	if !ok || got.Sub(cutoff) > 10*time.Millisecond || cutoff.Sub(got) > 10*time.Millisecond {
		t.Fatalf("cleanup deadline = %v, want %v", got, cutoff)
	}
}

func TestCleanupContextCapsDuration(t *testing.T) {
	work := WithCleanupDeadline(context.Background(), time.Now().Add(time.Minute))
	cleanup, cancel := CleanupContext(work)
	defer cancel()
	deadline, ok := cleanup.Deadline()
	if !ok {
		t.Fatal("cleanup context has no deadline")
	}
	remaining := time.Until(deadline)
	if remaining <= 0 || remaining > MaxCleanupDuration {
		t.Fatalf("cleanup duration = %s, want (0, %s]", remaining, MaxCleanupDuration)
	}
}

func TestCleanupContextIgnoresExpiredWorkDeadline(t *testing.T) {
	work, cancelWork := context.WithDeadline(context.Background(), time.Now().Add(10*time.Millisecond))
	cutoff := time.Now().Add(500 * time.Millisecond)
	work = WithCleanupDeadline(work, cutoff)
	cancelWork()

	cleanup, cancelCleanup := CleanupContext(work)
	defer cancelCleanup()
	got, ok := cleanup.Deadline()
	if !ok || got.Sub(cutoff) > 10*time.Millisecond || cutoff.Sub(got) > 10*time.Millisecond {
		t.Fatalf("cleanup deadline = %v, want reserved cutoff %v", got, cutoff)
	}
}

func TestDetachedContextPreservesDeadlineAfterCancellation(t *testing.T) {
	parent, cancelParent := context.WithDeadline(context.Background(), time.Now().Add(time.Second))
	want, _ := parent.Deadline()
	cancelParent()

	detached, cancelDetached := DetachedContext(parent, time.Minute)
	defer cancelDetached()
	if err := detached.Err(); err != nil {
		t.Fatalf("detached context error = %v, want active context", err)
	}
	got, ok := detached.Deadline()
	if !ok || got != want {
		t.Fatalf("detached deadline = %v, want %v", got, want)
	}
}
