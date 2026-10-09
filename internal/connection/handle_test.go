package connection

import (
	"context"
	"errors"
	"testing"

	"github.com/siia/siia-mcp/internal/engine"
)

type fakeConn struct{ engine.Connection }

func TestEagerInitSuccess(t *testing.T) {
	h := NewHandle("c", true, true)
	done := false
	err := h.Init(context.Background(), func(context.Context) (engine.Connection, error) {
		done = true
		return fakeConn{}, nil
	})
	if err != nil {
		t.Fatalf("init error = %v", err)
	}
	if !done {
		t.Fatal("init func not called")
	}
	if h.State() != StateReady {
		t.Fatalf("state = %s, want ready", h.State())
	}
}

func TestEagerInitFailureBecomesUnavailable(t *testing.T) {
	h := NewHandle("c", false, true)
	err := h.Init(context.Background(), func(context.Context) (engine.Connection, error) {
		return nil, errors.New("boom")
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if h.State() != StateUnavailable {
		t.Fatalf("state = %s, want unavailable", h.State())
	}
}

func TestLazyConnectRetriesAfterBackoff(t *testing.T) {
	h := NewHandle("c", false, false)
	attempts := 0
	_, err := h.Connect(context.Background(), func(context.Context) (engine.Connection, error) {
		attempts++
		return nil, errors.New("down")
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if h.State() != StateUnavailable {
		t.Fatalf("state = %s, want unavailable", h.State())
	}
	// Immediate retry is suppressed by backoff.
	_, err = h.Connect(context.Background(), func(context.Context) (engine.Connection, error) {
		attempts++
		return fakeConn{}, nil
	})
	if err == nil {
		t.Fatal("expected backoff to suppress immediate retry")
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1 (no retry during backoff)", attempts)
	}
}
