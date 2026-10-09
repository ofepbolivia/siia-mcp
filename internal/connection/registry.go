package connection

import (
	"context"
	"fmt"
	"sync"

	"github.com/siia/siia-mcp/internal/contract"
	"github.com/siia/siia-mcp/internal/engine"
)

// Registry holds the logical connection handles keyed by opaque name.
type Registry struct {
	mu      sync.RWMutex
	handles map[string]*Handle
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{handles: map[string]*Handle{}}
}

// Add registers a handle under its name.
func (r *Registry) Add(h *Handle) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.handles[h.Name()]; dup {
		return fmt.Errorf("duplicate connection %q", h.Name())
	}
	r.handles[h.Name()] = h
	return nil
}

// Get returns the handle for a logical connection name.
func (r *Registry) Get(name string) (*Handle, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	h, ok := r.handles[name]
	return h, ok
}

// Lookup returns a ready engine connection for a name, or CONNECTION_NOT_FOUND
// when the name is not configured. Initialization of lazy handles happens via
// the provided init func.
func (r *Registry) Lookup(ctx context.Context, name string, init func(context.Context) (engine.Connection, error)) (engine.Connection, error) {
	h, ok := r.Get(name)
	if !ok {
		return nil, &contract.PublicError{
			Code:    contract.CodeConnectionNotFound,
			Message: "connection not found",
		}
	}
	return h.Connect(ctx, init)
}

// List returns all handles.
func (r *Registry) List() []*Handle {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Handle, 0, len(r.handles))
	for _, h := range r.handles {
		out = append(out, h)
	}
	return out
}

// CloseAll closes every handle.
func (r *Registry) CloseAll(ctx context.Context) {
	for _, h := range r.List() {
		_ = h.Close(ctx)
	}
}
