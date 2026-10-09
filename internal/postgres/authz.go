package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/siia/siia-mcp/internal/config"
	"github.com/siia/siia-mcp/internal/contract"
	"github.com/siia/siia-mcp/internal/postgres/guard"
)

// authorizeRelations validates that every persistent relation referenced by a
// guard result is authorized per-object (SDD §8.3). It resolves each relation's
// kind from pg_catalog so the correct allowlist rule applies.
func (a *adapter) authorizeRelations(ctx context.Context, rels []guard.Relation) error {
	for _, r := range rels {
		if err := a.authorizeRelation(ctx, r); err != nil {
			return err
		}
	}
	return nil
}

func (a *adapter) authorizeRelation(ctx context.Context, r guard.Relation) error {
	schema := r.Schema
	name := r.Name
	if schema == "" || name == "" {
		return notAllowed(`relation must be schema-qualified; reference base tables as "schema"."table"` + a.schemaHint(ctx))
	}
	// System metadata catalogs are exempt from the allowlist and always
	// queryable. The genuinely read-only database role remains the effective
	// boundary over anything in these catalogs it can read (SDD §8.3).
	if schema == "information_schema" || schema == "pg_catalog" {
		return nil
	}
	if strings.HasPrefix(schema, "pg_") {
		return notAllowed(fmt.Sprintf("schema %q is not allowed", schema))
	}
	// With no allowlist configured, every ordinary relation the read-only role
	// can read is authorized (SDD §8.3).
	if a.unrestricted() {
		return nil
	}
	kind := a.resolveKind(ctx, schema, name)
	if a.relationKindAllowed(schema, name, kind) {
		return nil
	}
	return notAllowed(fmt.Sprintf("relation %s.%s is not allowed", schema, name))
}

func (a *adapter) relationKindAllowed(schema, name, kind string) bool {
	if a.unrestricted() {
		return !isSystemSchema(schema)
	}
	switch kind {
	case "r", "p": // table, partitioned table
		if schemaInAllowlist(a.cfg, schema) {
			return true
		}
	case "v": // view
		if hasObjectRef(a.cfg.Allow.Views, schema, name) {
			return true
		}
	case "m": // materialized view
		if hasObjectRef(a.cfg.Allow.MaterializedViews, schema, name) {
			return true
		}
	}
	return false
}

func relkindForContractKind(kind string) string {
	switch kind {
	case "table":
		return "r"
	case "partitioned_table":
		return "p"
	case "view":
		return "v"
	case "materialized_view":
		return "m"
	default:
		return ""
	}
}

func sensitiveColumnName(name string) bool {
	n := strings.ToLower(name)
	for _, marker := range []string{"password", "passwd", "token", "secret", "credential", "dsn", "payload"} {
		if strings.Contains(n, marker) {
			return true
		}
	}
	return false
}

// resolveKind returns pg_catalog relkind char, or "" when not found. The result
// is cached (relation kind is stable absent DDL) so restricted-mode queries do
// not re-query the catalog for every relation on every request.
func (a *adapter) resolveKind(ctx context.Context, schema, name string) string {
	val, err := a.catalogGet(ctx, a.catalogKey("kind", schema, name), func(ctx context.Context) (any, error) {
		var kind string
		qerr := a.pool.QueryRow(ctx, `
SELECT c.relkind
FROM pg_catalog.pg_class c
JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = $1 AND c.relname = $2`, schema, name).Scan(&kind)
		if qerr == pgx.ErrNoRows {
			return "", nil
		}
		if qerr != nil {
			return nil, qerr
		}
		return kind, nil
	})
	if err != nil {
		return ""
	}
	kind, _ := val.(string)
	return kind
}

func schemaInAllowlist(cfg config.Connection, schema string) bool {
	for _, s := range cfg.Allow.Schemas {
		if s == schema {
			return true
		}
	}
	return false
}

func hasObjectRef(refs []config.ObjectRef, schema, name string) bool {
	for _, o := range refs {
		if o.Schema == schema && o.Name == name {
			return true
		}
	}
	return false
}

func notAllowed(msg string) error {
	return &contract.PublicError{Code: contract.CodeObjectNotAllowed, Message: msg}
}

// schemaHint lists the authorized schemas so the agent can immediately pick the
// right qualifier instead of guessing. It returns an empty string when the
// allowlist cannot be resolved or is unrestricted.
func (a *adapter) schemaHint(ctx context.Context) string {
	if a.unrestricted() {
		return ""
	}
	schemas, err := a.allowedSchemas(ctx)
	if err != nil || len(schemas) == 0 {
		return ""
	}
	return "; authorized schemas: " + strings.Join(schemas, ", ")
}

func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}
