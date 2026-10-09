//go:build bench

package manager

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"testing"
)

func benchManager(b *testing.B) *Manager {
	m, err := New(testConfig(), slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err != nil {
		b.Fatal(err)
	}
	return m
}

// BenchmarkToolListConnections measures handler + envelope build for a fully
// in-process tool (no PostgreSQL), isolating middleware/audit/metrics overhead
// (SDD §21, benchmark §3.3).
func BenchmarkToolListConnections(b *testing.B) {
	m := benchManager(b)
	ctx := context.Background()
	for i := 0; i < b.N; i++ {
		if _, err := m.HandleTool(ctx, "db_list_connections", nil); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkRejectedQuery measures a rejected db_query (unknown connection,
// fails at lookup without contacting PostgreSQL).
func BenchmarkRejectedQuery(b *testing.B) {
	m := benchManager(b)
	ctx := context.Background()
	args := json.RawMessage(`{"connection":"does_not_exist","sql":"select 1"}`)
	for i := 0; i < b.N; i++ {
		if _, err := m.HandleTool(ctx, "db_query", args); err == nil {
			b.Fatal("expected rejection")
		}
	}
}
