// Package connection manages the lifecycle of logical connections and their
// handles (SDD §7).
package connection

import (
	"context"
	"sync"
	"time"

	"github.com/siia/siia-mcp/internal/engine"
)

// State reflects the lifecycle of a logical connection handle (SDD §7.2).
type State string

const (
	StateUninitialized State = "uninitialized"
	StateInitializing  State = "initializing"
	StateReady         State = "ready"
	StateUnavailable   State = "unavailable"
	StateClosing       State = "closing"
	StateClosed        State = "closed"
)

// lazyBackoff controls retry after a failed lazy init (SDD §7.2).
const (
	lazyBackoffBase = time.Second
	lazyBackoffMax  = 30 * time.Second
)

// Handle wraps an engine.Connection and manages its lifecycle. Each logical
// connection owns exactly one pool (SDD §7.2).
type Handle struct {
	name     string
	required bool
	eager    bool

	mu      sync.Mutex
	state   State
	conn    engine.Connection
	lastTry time.Time
	backoff time.Duration
}

// NewHandle creates a handle for a logical connection.
func NewHandle(name string, required, eager bool) *Handle {
	state := StateUninitialized
	if eager {
		state = StateInitializing
	}
	return &Handle{
		name:     name,
		required: required,
		eager:    eager,
		state:    state,
		backoff:  lazyBackoffBase,
	}
}

// Name returns the logical connection name.
func (h *Handle) Name() string { return h.name }

// State returns the current lifecycle state.
func (h *Handle) State() State {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.state
}

// Required reports whether this connection gates startup.
func (h *Handle) Required() bool { return h.required }

// SetConn attaches the engine connection (adapter) to the handle.
func (h *Handle) SetConn(c engine.Connection) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.conn = c
}

// Init initializes an eager handle; it must be called during startup.
func (h *Handle) Init(ctx context.Context, init func(context.Context) (engine.Connection, error)) error {
	if !h.eager {
		return nil
	}
	h.mu.Lock()
	if h.conn != nil {
		state := h.state
		h.mu.Unlock()
		if state == StateReady {
			return nil
		}
	}
	h.mu.Unlock()

	conn, err := init(ctx)
	if err != nil {
		h.set(StateUnavailable)
		return err
	}
	h.SetConn(conn)
	h.set(StateReady)
	return nil
}

// Connect returns the ready engine connection, or an error if unavailable. For
// lazy handles it triggers initialization (with backoff on failure).
func (h *Handle) Connect(ctx context.Context, init func(context.Context) (engine.Connection, error)) (engine.Connection, error) {
	h.mu.Lock()
	if h.state == StateReady && h.conn != nil {
		conn := h.conn
		h.mu.Unlock()
		return conn, nil
	}
	if h.eager {
		h.mu.Unlock()
		return nil, errUnavailable(h.name)
	}
	if h.state == StateUnavailable {
		if time.Since(h.lastTry) < h.backoff {
			h.mu.Unlock()
			return nil, errUnavailable(h.name)
		}
	}
	h.state = StateInitializing
	h.mu.Unlock()

	conn, err := init(ctx)
	if err != nil {
		h.mu.Lock()
		h.state = StateUnavailable
		h.lastTry = time.Now()
		if h.backoff < lazyBackoffMax {
			h.backoff *= 2
		}
		h.mu.Unlock()
		return nil, errUnavailable(h.name)
	}
	h.SetConn(conn)
	h.mu.Lock()
	h.state = StateReady
	h.backoff = lazyBackoffBase
	h.mu.Unlock()
	return conn, nil
}

// Close transitions the handle to closed and closes its engine connection.
func (h *Handle) Close(ctx context.Context) error {
	h.mu.Lock()
	h.state = StateClosing
	conn := h.conn
	h.mu.Unlock()

	if conn != nil {
		if err := conn.Close(ctx); err != nil {
			h.mu.Lock()
			h.state = StateClosed
			h.mu.Unlock()
			return err
		}
	}
	h.mu.Lock()
	h.state = StateClosed
	h.mu.Unlock()
	return nil
}

func (h *Handle) set(s State) {
	h.mu.Lock()
	h.state = s
	h.mu.Unlock()
}
