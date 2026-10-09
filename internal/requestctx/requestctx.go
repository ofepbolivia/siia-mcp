// Package requestctx carries request-stage deadlines across engine boundaries.
package requestctx

import (
	"context"
	"time"
)

const MaxCleanupDuration = 2 * time.Second

type cleanupDeadlineKey struct{}

// WithCleanupDeadline records the absolute cutoff reserved for rollback and
// connection reset. The value survives cancellation of the working context.
func WithCleanupDeadline(ctx context.Context, deadline time.Time) context.Context {
	return context.WithValue(ctx, cleanupDeadlineKey{}, deadline)
}

// CleanupContext detaches request cancellation while preserving the reserved
// cleanup cutoff and the two-second maximum cleanup duration.
func CleanupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	deadline := time.Now().Add(MaxCleanupDuration)
	if cutoff, ok := ctx.Value(cleanupDeadlineKey{}).(time.Time); ok {
		if cutoff.Before(deadline) {
			deadline = cutoff
		}
	} else if parentDeadline, ok := ctx.Deadline(); ok && parentDeadline.Before(deadline) {
		deadline = parentDeadline
	}
	return context.WithDeadline(context.Background(), deadline)
}

// DetachedContext allows mandatory cleanup or audit work to survive request
// cancellation without running longer than maxDuration or the request deadline.
func DetachedContext(ctx context.Context, maxDuration time.Duration) (context.Context, context.CancelFunc) {
	deadline := time.Now().Add(maxDuration)
	if parentDeadline, ok := ctx.Deadline(); ok && parentDeadline.Before(deadline) {
		deadline = parentDeadline
	}
	return context.WithDeadline(context.Background(), deadline)
}
