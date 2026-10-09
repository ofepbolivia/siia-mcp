package postgres

import (
	"context"
	"strings"
)

// catalogKey namespaces a stable catalog lookup under the adapter identity so
// results never collide with metadata-tool cache entries (which use their own
// connection|op|policy|schema-version key shape).
func (a *adapter) catalogKey(kind string, parts ...string) string {
	var b strings.Builder
	b.WriteString(a.name)
	b.WriteString("|cat:")
	b.WriteString(kind)
	for _, p := range parts {
		b.WriteByte('|')
		b.WriteString(p)
	}
	return b.String()
}

// catalogGet reads or refreshes a stable catalog value through the shared cache
// when present, or computes it directly otherwise (adapters built by tests may
// omit the cache). A refresh error is not cached.
func (a *adapter) catalogGet(ctx context.Context, key string, refresh func(context.Context) (any, error)) (any, error) {
	if a.catalog == nil {
		return refresh(ctx)
	}
	val, _, err := a.catalog.Get(ctx, key, refresh)
	return val, err
}
