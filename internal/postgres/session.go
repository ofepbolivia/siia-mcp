package postgres

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/siia/siia-mcp/internal/config"
)

// sessionSetStatements are the session-level invariants (not SET LOCAL) that
// persist across the pool acquire/release cycle on each physical connection
// (SDD §7.3). They are applied together in a single round-trip.
var sessionSetStatements = []string{
	`SET application_name = 'siia'`,
	`SET default_transaction_read_only = on`,
	`SET statement_timeout = 60000`,
	`SET idle_in_transaction_session_timeout = 5000`,
	`SET client_encoding = 'UTF8'`,
	`SET timezone = 'UTC'`,
	`SET datestyle = 'ISO, YMD'`,
	`SET intervalstyle = 'iso_8601'`,
	`SET bytea_output = 'hex'`,
	`SET extra_float_digits = 3`,
}

// sessionResolver memoizes the search_path resolution. The collision check
// (hasTableNameCollisions) is a property of the database plus the schema set and
// is invariant across requests, so it is computed at most once per distinct
// schema set instead of on every request reset.
type sessionResolver struct {
	mu    sync.Mutex
	cache map[string]string
}

func newSessionResolver() *sessionResolver {
	return &sessionResolver{cache: map[string]string{}}
}

// searchPath returns the SET search_path value for a schema set, computing the
// table-name collision check only on a cache miss.
func (r *sessionResolver) searchPath(ctx context.Context, conn *pgx.Conn, schemas []string) (string, error) {
	key := schemaSetKey(schemas)
	r.mu.Lock()
	sp, ok := r.cache[key]
	r.mu.Unlock()
	if ok {
		return sp, nil
	}
	sp = "pg_catalog"
	if len(schemas) > 0 && !hasTableNameCollisions(ctx, conn, schemas) {
		for _, s := range schemas {
			sp += ", " + s
		}
	}
	r.mu.Lock()
	r.cache[key] = sp
	r.mu.Unlock()
	return sp, nil
}

func schemaSetKey(schemas []string) string {
	if len(schemas) == 0 {
		return ""
	}
	cp := append([]string(nil), schemas...)
	slices.Sort(cp)
	return strings.Join(cp, "\x00")
}

// setupSession enforces PostgreSQL session invariants immediately after a new
// physical connection is established (SDD §7.3). A connection that rejects any
// invariant is discarded. When schemas is non-empty and there are no table name
// collisions across them, search_path is configured so agents can reference
// tables without schema qualification (agent fluency improvement). When
// collisions exist, search_path stays restricted to pg_catalog to avoid
// ambiguity and require explicit schema qualification.
func setupSession(ctx context.Context, conn *pgx.Conn, resolver *sessionResolver, schemas []string) error {
	if resolver == nil {
		resolver = newSessionResolver()
	}
	searchPath, err := resolver.searchPath(ctx, conn, schemas)
	if err != nil {
		return err
	}
	return applySessionInvariants(ctx, conn, searchPath)
}

// applySessionInvariants sends every SET plus search_path in ONE round-trip.
// With the pool's simple-protocol exec mode, pgx concatenates the queued
// statements into a single simple query, so the reset cost drops from ~11
// round-trips to one.
func applySessionInvariants(ctx context.Context, conn *pgx.Conn, searchPath string) error {
	stmts := make([]string, 0, len(sessionSetStatements)+1)
	stmts = append(stmts, sessionSetStatements...)
	stmts = append(stmts, "SET search_path = "+searchPath)

	batch := &pgx.Batch{}
	for _, stmt := range stmts {
		batch.Queue(stmt)
	}
	br := conn.SendBatch(ctx, batch)
	var firstErr error
	for i := range stmts {
		if _, err := br.Exec(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("session invariant %q: %w", stmts[i], err)
		}
	}
	closeErr := br.Close()
	if firstErr != nil {
		return firstErr
	}
	return closeErr
}

// searchPathSchemas returns the user schemas to place on search_path, derived
// from the connection allowlist: explicit schemas plus the schemas of allowed
// views and materialized views. It is the single source of truth for the
// search_path schema list, used both at connection setup (AfterConnect) and
// after each request resets the session. It returns nil when the allowlist is
// empty (nil or empty) — unrestricted connections resolve their user schemas
// dynamically via the adapter's allowedSchemas at request time.
func searchPathSchemas(allow *config.Allowlist) []string {
	if allow == nil || allow.Empty() {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(allow.Schemas))
	add := func(s string) {
		if s == "" {
			return
		}
		if _, ok := seen[s]; ok {
			return
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	for _, s := range allow.Schemas {
		add(s)
	}
	for _, v := range allow.Views {
		add(v.Schema)
	}
	for _, v := range allow.MaterializedViews {
		add(v.Schema)
	}
	return out
}

// hasTableNameCollisions checks if any table name appears in more than one
// of the allowed schemas. Returns true if collisions exist.
func hasTableNameCollisions(ctx context.Context, conn *pgx.Conn, schemas []string) bool {
	query := `
SELECT relname, COUNT(DISTINCT nspname) AS schema_count
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = ANY($1)
  AND c.relkind IN ('r','v','m','p')
GROUP BY relname
HAVING COUNT(DISTINCT nspname) > 1
LIMIT 1`
	row := conn.QueryRow(ctx, query, schemas)
	var relname string
	var count int
	err := row.Scan(&relname, &count)
	// If there's any row, there's a collision
	return err == nil
}
