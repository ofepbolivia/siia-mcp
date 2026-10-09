//go:build integration

package manager

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/siia/siia-mcp/internal/config"
	"github.com/siia/siia-mcp/internal/contract"
)

// itConn returns a real PostgreSQL connection config mirroring the adapter
// IT env wiring (SIIASQL_IT_*), so tool handlers run against the reference DB.
func itConn() config.Connection {
	c := config.Connection{
		Name:       "it",
		Engine:     "postgres",
		Mode:       "readonly",
		Initialize: "lazy",
		Host:       "127.0.0.1",
		Port:       55432,
		Database:   "itdb",
		User:       "ituser",
		Password:   "itpass",
		TLS:        config.TLSConfig{Mode: "disable"},
		Pool: config.PoolConfig{
			MaxConnections:        4,
			MaxConnectionLifetime: 30 * time.Minute,
			MaxConnectionIdleTime: 5 * time.Minute,
			HealthCheckPeriod:     30 * time.Second,
		},
		Allow: &config.Allowlist{Schemas: []string{"public"}},
	}
	if h := os.Getenv("SIIASQL_IT_HOST"); h != "" {
		c.Host = h
	}
	if p := os.Getenv("SIIASQL_IT_PORT"); p != "" {
		fmt.Sscanf(p, "%d", &c.Port)
	}
	if u := os.Getenv("SIIASQL_IT_USER"); u != "" {
		c.User = u
	}
	if pw := os.Getenv("SIIASQL_IT_PASSWORD"); pw != "" {
		c.Password = pw
	}
	if d := os.Getenv("SIIASQL_IT_DB"); d != "" {
		c.Database = d
	}
	return c
}

func itManager(t *testing.T) *Manager {
	cfg := testConfig()
	cfg.Server.RequestTimeout = 5 * time.Second
	c := itConn()
	cfg.Connection = &c
	m, err := New(cfg, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return m
}

// TestITToolTimeout verifies the §17.2 effective context: a db_query whose tool
// timeout_ms is far smaller than the query runtime aborts with TIMEOUT via the
// pooled connection, without hanging the request.
func TestITToolTimeout(t *testing.T) {
	m := itManager(t)
	t.Cleanup(func() { m.Close(context.Background()) })
	args := []byte(`{"connection":"it","sql":"SELECT pg_sleep(3)","limits":{"timeout_ms":5000}}`)
	start := time.Now()
	_, err := m.HandleTool(context.Background(), "db_query", args)
	elapsed := time.Since(start)
	pe, ok := err.(*contract.PublicError)
	if !ok || pe.Code != contract.CodeTimeout {
		t.Fatalf("expected TIMEOUT public error, got %#v (elapsed=%v)", err, elapsed)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("tool timeout did not abort promptly: elapsed=%v", elapsed)
	}
	// The pool must remain usable after the aborted query.
	if _, err := m.HandleTool(context.Background(), "db_query",
		[]byte(`{"connection":"it","sql":"SELECT 1"}`)); err != nil {
		t.Fatalf("adapter did not recover after tool timeout: %v", err)
	}
}
