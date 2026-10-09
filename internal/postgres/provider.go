// Package postgres implements the engine.Connection contract for PostgreSQL
// using pgx/pgxpool (SDD §17).
package postgres

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/siia/siia-mcp/internal/cache"
	"github.com/siia/siia-mcp/internal/config"
	"github.com/siia/siia-mcp/internal/cursor"
	"github.com/siia/siia-mcp/internal/engine"
	"github.com/siia/siia-mcp/internal/postgres/guard"
)

// poolConfig builds the pgxpool config from the logical connection config.
// Password is never stored outside the in-memory pool config.
func poolConfig(cfg config.Connection, resolver *sessionResolver) (*pgxpool.Config, error) {
	poolCfg, err := pgxpool.ParseConfig("")
	if err != nil {
		return nil, err
	}
	poolCfg.ConnConfig.Host = cfg.Host
	poolCfg.ConnConfig.Port = uint16(cfg.Port)
	poolCfg.ConnConfig.Database = cfg.Database
	poolCfg.ConnConfig.User = cfg.User
	poolCfg.ConnConfig.Password = cfg.Password

	if cfg.TLS.Mode == "verify-full" {
		tlsCfg := &tls.Config{ServerName: cfg.TLS.ServerName, MinVersion: tls.VersionTLS12}
		if cfg.TLS.RootCA != "" {
			pem, err := os.ReadFile(cfg.TLS.RootCA)
			if err != nil {
				return nil, fmt.Errorf("root_ca: %w", err)
			}
			roots := x509.NewCertPool()
			if !roots.AppendCertsFromPEM(pem) {
				return nil, fmt.Errorf("root_ca: no certificates parsed")
			}
			tlsCfg.RootCAs = roots
		}
		poolCfg.ConnConfig.TLSConfig = tlsCfg
	}

	poolCfg.ConnConfig.ConnectTimeout = 10 * time.Second
	// Simple protocol returns result values as exact PostgreSQL text, which the
	// encoder interprets by OID (SDD §14.1). It also makes session-level
	// DISCARD ALL cleanup valid (SDD §9.4).
	poolCfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol

	poolCfg.MaxConns = int32(cfg.Pool.MaxConnections)
	poolCfg.MinConns = int32(cfg.Pool.MinConnections)
	poolCfg.MaxConnLifetime = cfg.Pool.MaxConnectionLifetime
	poolCfg.MaxConnIdleTime = cfg.Pool.MaxConnectionIdleTime
	poolCfg.HealthCheckPeriod = cfg.Pool.HealthCheckPeriod

	// search_path schemas come from a single source (searchPathSchemas); for
	// unrestricted connections the adapter resolves them dynamically per request.
	allowedSchemas := searchPathSchemas(cfg.Allow)
	poolCfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		return setupSession(ctx, conn, resolver, allowedSchemas)
	}
	return poolCfg, nil
}

// New builds an engine connection backed by a pgxpool. The pool connects
// lazily on first use; the caller (handle init) pings to validate reachability.
// catalog, when non-nil, memoizes stable catalog lookups (relation kind, primary
// keys, user-schema list) that are otherwise re-queried per request.
func New(ctx context.Context, cfg config.Connection, global config.LimitsConfig, catalog *cache.Cache) (engine.Connection, error) {
	resolver := newSessionResolver()
	pc, err := poolConfig(cfg, resolver)
	if err != nil {
		return nil, err
	}
	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		return nil, err
	}
	key := make([]byte, sha256.Size)
	if _, err := rand.Read(key); err != nil {
		pool.Close()
		return nil, fmt.Errorf("cursor key: %w", err)
	}
	codec, err := cursor.New(key)
	if err != nil {
		pool.Close()
		return nil, err
	}
	policy, err := json.Marshal(cfg.Allow)
	if err != nil {
		pool.Close()
		return nil, err
	}
	digest := sha256.Sum256(policy)
	return &adapter{
		name:         cfg.Name,
		pool:         pool,
		cfg:          cfg,
		limit:        resolveLimits(cfg, global),
		cursors:      codec,
		policyDigest: digest[:],
		checker:      guard.NewChecker(0),
		session:      resolver,
		catalog:      catalog,
	}, nil
}
