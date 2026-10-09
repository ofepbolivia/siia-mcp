// Package admission implements the global ingress gate (SDD §17.1): every tool
// call, including cache hits and db_list_connections, consumes a global unit.
//
// Two bounded pools model the spec:
//   - ingress has capacity active + queued and is taken without blocking. If it
//     is full the whole process is saturated and the caller rejects with
//     SERVER_BUSY without doing any handler work (a burst cannot accumulate
//     waiters).
//   - active has capacity active; up to active calls run, the overflow (up to
//     queued) waits FIFO on a bounded acquire that honours the request context.
//
// The slot is retained until the caller confirms the final audit event, at
// which point Leave releases both the active and the ingress token.
package admission

import (
	"context"
	"errors"
)

// ErrBusy is returned by Enter when the process is saturated (ingress full):
// the caller should audit an attempt/outcome pair with SERVER_BUSY and reject.
var ErrBusy = errors.New("admission: saturated")

// Error is a distinguishable SERVER_BUSY signal for the caller.
type Error struct{}

// Gate is the global admission gate for the process.
type Gate struct {
	ingress chan struct{}
	active  chan struct{}
}

// New builds a gate with the given active and queued capacities. Both must be
// positive (the config validator enforces >= 1).
func New(active, queued int) *Gate {
	return &Gate{
		ingress: make(chan struct{}, active+queued),
		active:  make(chan struct{}, active),
	}
}

// Reserve takes an ingress token without blocking. The caller retains it until
// Cancel or Leave, including while confirming the audit attempt (§17.1).
func (g *Gate) Reserve(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case g.ingress <- struct{}{}:
		return nil
	default:
		return ErrBusy
	}
}

// Activate waits for an execution slot after the caller confirms the audit
// attempt. A failed activation leaves the ingress token reserved for the caller
// to release after auditing the rejection outcome.
func (g *Gate) Activate(ctx context.Context) error {
	select {
	case g.active <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Cancel releases a reserved ingress token that never became active.
func (g *Gate) Cancel() { <-g.ingress }

// Enter is the combined reservation/activation operation used by callers that
// do not need to insert work between the two admission stages.
func (g *Gate) Enter(ctx context.Context) error {
	if err := g.Reserve(ctx); err != nil {
		return err
	}
	if err := g.Activate(ctx); err != nil {
		g.Cancel()
		return err
	}
	return nil
}

// Leave releases the active slot and the ingress token. It must be called
// exactly once per successful Enter, after the final audit event is confirmed.
func (g *Gate) Leave() {
	<-g.active
	<-g.ingress
}

// Saturate reports whether the gate is currently full (all ingress tokens are
// held), useful for metrics.
func (g *Gate) Saturate() bool {
	return len(g.ingress) == cap(g.ingress)
}
