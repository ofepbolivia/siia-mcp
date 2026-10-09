//go:build bench && integration

package postgres

import (
	"context"
	"testing"

	"github.com/siia/siia-mcp/internal/engine"
)

// PostgreSQL-real baselines (SDD §21, benchmark §3.5). Run with both the `bench`
// and `integration` tags against a reference PostgreSQL configured via
// SIIASQL_IT_* env vars. Covers ping, schema listing, 1/100-row queries, and
// metadata cache hit vs miss.

func benchConn(b *testing.B) engine.Connection {
	b.Helper()
	cfg := testITConfig()
	applyITEnv(&cfg)
	global := itLimits()
	conn, err := New(context.Background(), cfg, global, nil)
	if err != nil {
		b.Fatalf("New: %v", err)
	}
	b.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn
}

func BenchmarkPing(b *testing.B) {
	conn := benchConn(b)
	ctx := context.Background()
	for i := 0; i < b.N; i++ {
		if _, err := conn.Ping(ctx); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkListSchemas(b *testing.B) {
	conn := benchConn(b)
	ctx := context.Background()
	for i := 0; i < b.N; i++ {
		if _, err := conn.ListSchemas(ctx, engine.PageRequest{Limit: 50}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkQuery1Row(b *testing.B) {
	conn := benchConn(b)
	ctx := context.Background()
	req := engine.QueryRequest{SQL: "SELECT * FROM public.books WHERE id = 1"}
	for i := 0; i < b.N; i++ {
		if _, err := conn.Query(ctx, req); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkQuery100Rows(b *testing.B) {
	conn := benchConn(b)
	ctx := context.Background()
	req := engine.QueryRequest{SQL: "SELECT * FROM public.books ORDER BY id LIMIT 100"}
	for i := 0; i < b.N; i++ {
		if _, err := conn.Query(ctx, req); err != nil {
			b.Fatal(err)
		}
	}
}
