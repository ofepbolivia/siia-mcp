//go:build bench && integration

package postgres

import (
	"fmt"
	"os"

	"github.com/siia/siia-mcp/internal/config"
)

// Shared helpers for PostgreSQL-real benchmarks.
// Factored out of newTestConn so bench code can reuse the same env wiring.

func applyITEnv(cfg *config.Connection) {
	if h := os.Getenv("SIIASQL_IT_HOST"); h != "" {
		cfg.Host = h
	}
	if p := os.Getenv("SIIASQL_IT_PORT"); p != "" {
		var port int
		if _, err := fmt.Sscanf(p, "%d", &port); err == nil {
			cfg.Port = port
		}
	}
	if pw := os.Getenv("SIIASQL_IT_PASSWORD"); pw != "" {
		cfg.Password = pw
	}
	if u := os.Getenv("SIIASQL_IT_USER"); u != "" {
		cfg.User = u
	}
	if d := os.Getenv("SIIASQL_IT_DB"); d != "" {
		cfg.Database = d
	}
}

func itLimits() config.LimitsConfig {
	return config.LimitsConfig{
		QueryBytes:           65536,
		ParameterCount:       64,
		ParameterBytes:       262144,
		ParametersTotalBytes: 1048576,
	}
}
