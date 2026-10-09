# Repository Instructions

## Current State

- This is SIIA's MCP server (`siiasql`), a read-only PostgreSQL process that exposes a
  guarded, read-only SQL query surface over SER v3.0 to the SIIA AI orchestrator.
- The Go module is `github.com/siia/siia-mcp`; the binary is `siiasql`
  (`cmd/siiasql`). Config is YAML passed explicitly with `--config`; secrets are read
  from an env file (or the process environment).
- The consumer is the SIIA AI orchestrator, not end users and not developers. There is
  no interactive setup wizard, no MCP client installer, and no multi-engine abstraction.

## Product Boundaries

- `stdio` is the only transport. Keep `stdout` reserved for MCP protocol frames; logs and
  errors go to `stderr`.
- Read-only only. The SQL guard and validation are defense-in-depth and do not replace a
  genuinely read-only PostgreSQL role.
- Free read-only SQL is allowed through `db_query` and `db_explain`. Every statement must
  pass the AST guard: exactly one `SELECT`, no DDL/DML, no locking clauses, no `SELECT INTO`.
  Anything outside a single read-only `SELECT` is rejected fail-closed.
- Metadata introspection tools (`db_list_schemas`, `db_list_tables`, `db_describe_table`,
  `db_list_indexes`, `db_list_relationships`, `db_suggest_relationships`, `db_search_columns`,
  `db_find_references`, `db_sample_rows`, `db_count_rows`) are available so the caller can
  discover the schema and build queries. The caller is the trusted orchestrator; end users
  never reach the MCP.
- The connection `allow` allowlist (`schemas`, `views`, `materialized_views`) restricts the
  objects a query may reach. When it is empty, every object the PostgreSQL role can read is
  reachable, so the role's grants are the real boundary.
- Never return, log, audit, or commit credentials or DSNs.

## Safety Constraints

- Enforce the configured query timeout, row count, payload size, and concurrency limits.
  Request overrides may only reduce effective limits.
- Keep SQL, parameters, and result values out of audit events; use a hash or fingerprint when
  correlation is required.
- Preserve exact numeric values: serialize `NUMERIC`/`DECIMAL` as strings when JSON numbers
  would lose precision.
- Return full rows or a complete truncation boundary; never partial rows or values.

## Verification

- Fast checks: `make fast` (gofmt, vet, tests, race, build).
- Protocol-level: `make e2e` (build tag `e2e`).
- Integration: `make it` (build tag `integration`) against a real PostgreSQL configured via
  `SIIASQL_IT_HOST/PORT/USER/PASSWORD/DB`.
