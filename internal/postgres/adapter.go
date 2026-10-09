package postgres

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/siia/siia-mcp/internal/cache"
	"github.com/siia/siia-mcp/internal/config"
	"github.com/siia/siia-mcp/internal/contract"
	"github.com/siia/siia-mcp/internal/cursor"
	"github.com/siia/siia-mcp/internal/engine"
	"github.com/siia/siia-mcp/internal/postgres/guard"
)

// adapter implements engine.Connection against a shared pgxpool.
type adapter struct {
	name         string
	pool         *pgxpool.Pool
	cfg          config.Connection
	limit        limits
	cursors      *cursor.Codec
	policyDigest []byte
	checker      *guard.Checker
	session      *sessionResolver
	catalog      *cache.Cache
}

var _ engine.Connection = (*adapter)(nil)

// allowedSchemas returns the set of schema names to expose for a connection.
// When no allowlist is configured (nil or empty) the connection is
// unrestricted and every user schema is listed (SDD §8.3). Otherwise the
// schema list is derived from the single searchPathSchemas source (explicit
// schemas plus the schemas of allowed views/matviews).
func (a *adapter) allowedSchemas(ctx context.Context) ([]string, error) {
	if a.unrestricted() {
		return a.allUserSchemas(ctx)
	}
	return searchPathSchemas(a.cfg.Allow), nil
}

// allUserSchemas returns every non-system schema in the database. System
// schemas (pg_*, information_schema) are internal and never listed. The result
// is cached because it only changes on DDL, and a copy is returned so callers
// may sort it without mutating the cached slice.
func (a *adapter) allUserSchemas(ctx context.Context) ([]string, error) {
	val, err := a.catalogGet(ctx, a.catalogKey("user_schemas"), func(ctx context.Context) (any, error) {
		rows, err := a.pool.Query(ctx, `
SELECT nspname
FROM pg_namespace
WHERE nspname NOT LIKE 'pg\_%' AND nspname <> 'information_schema'
ORDER BY nspname COLLATE "C"`)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := make([]string, 0, 8)
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				return nil, err
			}
			out = append(out, s)
		}
		return out, rows.Err()
	})
	if err != nil {
		return nil, err
	}
	schemas, _ := val.([]string)
	return append([]string(nil), schemas...), nil
}

func (a *adapter) Ping(ctx context.Context) (engine.PingResult, error) {
	var version string
	if err := a.pool.QueryRow(ctx, `SELECT version()`).Scan(&version); err != nil {
		return engine.PingResult{}, err
	}
	return engine.PingResult{OK: true, ServerMajor: parseServerMajor(version)}, nil
}

func (a *adapter) ListSchemas(ctx context.Context, page engine.PageRequest) (contract.Page[contract.SchemaItem], error) {
	schemas, err := a.allowedSchemas(ctx)
	if err != nil {
		return contract.Page[contract.SchemaItem]{}, err
	}
	slices.Sort(schemas)
	start, limit, err := a.paginationStart(page, "db_list_schemas", nil, 1)
	if err != nil {
		return contract.Page[contract.SchemaItem]{}, err
	}
	items := make([]keyedItem[contract.SchemaItem], 0, limit+1)
	for _, s := range schemas {
		key := cursor.SortKey{Values: []string{s}}
		if !afterCursor(key, start) {
			continue
		}
		items = append(items, keyedItem[contract.SchemaItem]{item: contract.SchemaItem{Name: s}, key: key})
		if len(items) > limit {
			break
		}
	}
	return finishPage(a, items, limit, "db_list_schemas", nil)
}

func (a *adapter) ListTables(ctx context.Context, req engine.ListTablesRequest) (contract.Page[contract.TableItem], error) {
	schemas, err := a.allowedSchemas(ctx)
	if err != nil {
		return contract.Page[contract.TableItem]{}, err
	}
	if req.Schema != "" {
		if !a.schemaAllowed(ctx, req.Schema) {
			return contract.Page[contract.TableItem]{}, &contract.PublicError{
				Code:    contract.CodeObjectNotAllowed,
				Message: "schema is not authorized",
			}
		}
		schemas = []string{req.Schema}
	}
	filters := metadataFilters(req.Schema, nil)
	start, limit, err := a.paginationStart(req.Page, "db_list_tables", filters, 3)
	if err != nil {
		return contract.Page[contract.TableItem]{}, err
	}

	// Track explicitly allowed views/matviews that may live outside the schema
	// allowlist (only relevant in restricted mode).
	viewRefs := map[string]bool{}
	matRefs := map[string]bool{}
	if a.cfg.Allow != nil {
		for _, v := range a.cfg.Allow.Views {
			viewRefs[v.Schema+"."+v.Name] = true
		}
		for _, v := range a.cfg.Allow.MaterializedViews {
			matRefs[v.Schema+"."+v.Name] = true
		}
	}

	query := `
SELECT oid, nspname, relname, kind, security_invoker, security_barrier
FROM (
 SELECT c.oid, n.nspname, c.relname,
        CASE c.relkind
         WHEN 'r' THEN 'table'
         WHEN 'v' THEN 'view'
         WHEN 'm' THEN 'materialized_view'
         WHEN 'p' THEN 'partitioned_table'
         ELSE 'other'
        END AS kind,
        c.reloptions IS NOT NULL
        AND EXISTS (SELECT 1 FROM unnest(c.reloptions) o WHERE o = 'security_invoker=on')
          AS security_invoker,
        c.reloptions IS NOT NULL
        AND EXISTS (SELECT 1 FROM unnest(c.reloptions) o WHERE o = 'security_barrier=on')
          AS security_barrier
 FROM pg_class c
 JOIN pg_namespace n ON n.oid = c.relnamespace
 WHERE n.nspname = ANY($1)
   AND c.relkind IN ('r','v','m','p')
) AS relations
ORDER BY nspname COLLATE "C", relname COLLATE "C", kind COLLATE "C", oid`

	rows, err := a.pool.Query(ctx, query, schemas)
	if err != nil {
		return contract.Page[contract.TableItem]{}, err
	}
	defer rows.Close()

	items := make([]keyedItem[contract.TableItem], 0, limit+1)
	for rows.Next() {
		var it contract.TableItem
		var oid uint32
		var invoker, barrier *bool
		if err := rows.Scan(&oid, &it.Schema, &it.Name, &it.Kind, &invoker, &barrier); err != nil {
			return contract.Page[contract.TableItem]{}, err
		}
		unrestricted := a.unrestricted()
		// Narrow by explicit table/view/matview allowlist when the item is not
		// a plain table in an allowed schema. When unrestricted there is no
		// allowlist, so every ordinary relation gathered above is exposed.
		allowKey := it.Schema + "." + it.Name
		switch it.Kind {
		case "table", "partitioned_table":
			if !unrestricted && !schemaAllowsTable(a.cfg, it.Schema) {
				continue
			}
		case "view":
			if !unrestricted && !viewRefs[allowKey] {
				continue
			}
		case "materialized_view":
			if !unrestricted && !matRefs[allowKey] {
				continue
			}
		default:
			continue
		}
		it.SecurityInvoker = invoker
		it.SecurityBarrier = barrier
		key := cursor.SortKey{Values: []string{it.Schema, it.Name, it.Kind}, OID: oid}
		if !afterCursor(key, start) {
			continue
		}
		items = append(items, keyedItem[contract.TableItem]{item: it, key: key})
		if len(items) > limit {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return contract.Page[contract.TableItem]{}, err
	}
	return finishPage(a, items, limit, "db_list_tables", filters)
}

func (a *adapter) DescribeTable(ctx context.Context, ref engine.TableRef) (contract.TableDescription, error) {
	if err := a.authorizeRelation(ctx, guard.Relation{Schema: ref.Schema, Name: ref.Table}); err != nil {
		return contract.TableDescription{}, err
	}
	rel, err := a.relationMeta(ctx, ref)
	if err != nil {
		return contract.TableDescription{}, err
	}

	var desc contract.TableDescription
	desc.Schema = ref.Schema
	desc.Name = ref.Table
	desc.Kind = rel.kind

	// Columns.
	cols, err := a.pool.Query(ctx, `
SELECT a.attname, a.attnum,
       COALESCE(t.typname, ''),
       a.attnotnull,
       a.atthasdef,
       a.attgenerated <> '' AS generated,
       a.attidentity <> '' AS identity
FROM pg_attribute a
JOIN pg_type t ON t.oid = a.atttypid
WHERE a.attrelid = $1::regclass::oid
  AND a.attnum > 0
  AND NOT a.attisdropped
ORDER BY a.attnum`, ref.Schema+"."+ref.Table)
	if err != nil {
		return contract.TableDescription{}, err
	}
	defer cols.Close()
	for cols.Next() {
		var ci contract.ColumnInfo
		var notnull, hasdef, generated, identity bool
		if err := cols.Scan(&ci.Name, &ci.Ordinal, &ci.PostgresType, &notnull, &hasdef, &generated, &identity); err != nil {
			return contract.TableDescription{}, err
		}
		ci.Nullable = !notnull
		ci.HasDefault = hasdef
		ci.Generated = generated
		ci.Identity = identity
		ci.Sensitive = sensitiveColumnName(ci.Name)
		desc.Columns = append(desc.Columns, ci)
	}
	if err := cols.Err(); err != nil {
		return contract.TableDescription{}, err
	}

	desc.PrimaryKey, err = a.constraintPrimaryKey(ctx, ref)
	if err != nil {
		return contract.TableDescription{}, err
	}
	desc.UniqueConstraints, desc.CheckConstraints, desc.ExclusionConstraints, err = a.constraints(ctx, ref)
	if err != nil {
		return contract.TableDescription{}, err
	}
	desc.ForeignKeyConstraints, err = a.foreignKeyConstraints(ctx, ref)
	if err != nil {
		return contract.TableDescription{}, err
	}
	return desc, nil
}

func (a *adapter) ListIndexes(ctx context.Context, ref engine.TableRef, page engine.PageRequest) (contract.Page[contract.IndexItem], error) {
	if err := a.authorizeRelation(ctx, guard.Relation{Schema: ref.Schema, Name: ref.Table}); err != nil {
		return contract.Page[contract.IndexItem]{}, err
	}
	filters := metadataFilters(ref.Schema, &ref.Table)
	start, limit, err := a.paginationStart(page, "db_list_indexes", filters, 3)
	if err != nil {
		return contract.Page[contract.IndexItem]{}, err
	}
	rows, err := a.pool.Query(ctx, `
SELECT i.oid, i.relname,
       idx.indisunique,
       idx.indisprimary,
       idx.indisvalid,
       am.amname,
       idx.indkey::text,
       idx.indnkeyatts,
       pg_get_expr(idx.indexprs, idx.indrelid) AS exprs,
       COALESCE(idx.indpred <> '', false) AS partial
FROM pg_index idx
JOIN pg_class i ON i.oid = idx.indexrelid
JOIN pg_class t ON t.oid = idx.indrelid
JOIN pg_namespace n ON n.oid = t.relnamespace
JOIN pg_am am ON am.oid = i.relam
WHERE n.nspname = $1 AND t.relname = $2
ORDER BY n.nspname COLLATE "C", t.relname COLLATE "C", i.relname COLLATE "C", i.oid`, ref.Schema, ref.Table)
	if err != nil {
		return contract.Page[contract.IndexItem]{}, err
	}
	defer rows.Close()

	items := make([]keyedItem[contract.IndexItem], 0, limit+1)
	for rows.Next() {
		var it contract.IndexItem
		var oid uint32
		var indkeyStr string
		var nkeyatts int16
		var exprs *string
		if err := rows.Scan(&oid, &it.Name, &it.Unique, &it.Primary, &it.Valid, &it.Method,
			&indkeyStr, &nkeyatts, &exprs, &it.Partial); err != nil {
			return contract.Page[contract.IndexItem]{}, err
		}
		it.Keys = a.expandKeys(ctx, ref, indkeyStr, nkeyatts, exprs)
		key := cursor.SortKey{Values: []string{ref.Schema, ref.Table, it.Name}, OID: oid}
		if !afterCursor(key, start) {
			continue
		}
		items = append(items, keyedItem[contract.IndexItem]{item: it, key: key})
		if len(items) > limit {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return contract.Page[contract.IndexItem]{}, err
	}
	return finishPage(a, items, limit, "db_list_indexes", filters)
}

func (a *adapter) ListRelationships(ctx context.Context, req engine.RelationshipRequest) (contract.Page[contract.RelationshipItem], error) {
	if req.Table != nil && req.Schema == nil {
		return contract.Page[contract.RelationshipItem]{}, &contract.PublicError{Code: contract.CodeInvalidRequest, Message: "schema is required when table is provided"}
	}
	if req.ToTable != nil && req.ToSchema == nil {
		return contract.Page[contract.RelationshipItem]{}, &contract.PublicError{Code: contract.CodeInvalidRequest, Message: "to_schema is required when to_table is provided"}
	}
	if req.Schema != nil && !a.schemaAllowed(ctx, *req.Schema) {
		return contract.Page[contract.RelationshipItem]{}, &contract.PublicError{Code: contract.CodeObjectNotAllowed, Message: "schema is not authorized"}
	}
	if req.ToSchema != nil && !a.schemaAllowed(ctx, *req.ToSchema) {
		return contract.Page[contract.RelationshipItem]{}, &contract.PublicError{Code: contract.CodeObjectNotAllowed, Message: "to_schema is not authorized"}
	}
	if req.Table != nil {
		if err := a.authorizeRelation(ctx, guard.Relation{Schema: *req.Schema, Name: *req.Table}); err != nil {
			return contract.Page[contract.RelationshipItem]{}, err
		}
	}
	if req.ToTable != nil {
		if err := a.authorizeRelation(ctx, guard.Relation{Schema: *req.ToSchema, Name: *req.ToTable}); err != nil {
			return contract.Page[contract.RelationshipItem]{}, err
		}
	}
	schemaRef := req.Schema
	tableRef := req.Table
	filters := metadataFilters(derefString(schemaRef), tableRef)
	addMetadataFilter(filters, "to_schema", req.ToSchema)
	addMetadataFilter(filters, "to_table", req.ToTable)
	addMetadataFilter(filters, "column", req.Column)
	start, limit, err := a.paginationStart(req.Page, "db_list_relationships", filters, 5)
	if err != nil {
		return contract.Page[contract.RelationshipItem]{}, err
	}
	query := `
SELECT c.oid, c.conname,
       ns.nspname, cl.relname,
       nf.nspname, fcl.relname,
       ARRAY(SELECT a.attname FROM unnest(c.conkey) WITH ORDINALITY k(attnum, ord)
             JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = k.attnum
             ORDER BY k.ord),
       ARRAY(SELECT a.attname FROM unnest(c.confkey) WITH ORDINALITY k(attnum, ord)
             JOIN pg_attribute a ON a.attrelid = c.confrelid AND a.attnum = k.attnum
             ORDER BY k.ord),
       cl.relkind, fcl.relkind,
       c.confupdtype, c.confdeltype,
       c.condeferrable, c.condeferred
FROM pg_constraint c
JOIN pg_class cl ON cl.oid = c.conrelid
JOIN pg_namespace ns ON ns.oid = cl.relnamespace
JOIN pg_class fcl ON fcl.oid = c.confrelid
JOIN pg_namespace nf ON nf.oid = fcl.relnamespace
WHERE c.contype = 'f'`
	args := []any{}
	if schemaRef != nil {
		query += ` AND ns.nspname = $` + fmt.Sprint(len(args)+1)
		args = append(args, *schemaRef)
	}
	if tableRef != nil {
		query += ` AND cl.relname = $` + fmt.Sprint(len(args)+1)
		args = append(args, *tableRef)
	}
	if req.ToSchema != nil {
		query += ` AND nf.nspname = $` + fmt.Sprint(len(args)+1)
		args = append(args, *req.ToSchema)
	}
	if req.ToTable != nil {
		query += ` AND fcl.relname = $` + fmt.Sprint(len(args)+1)
		args = append(args, *req.ToTable)
	}
	if req.Column != nil {
		query += ` AND EXISTS (
  SELECT 1
  FROM unnest(c.conkey) k(attnum)
  JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = k.attnum
  WHERE a.attname = $` + fmt.Sprint(len(args)+1) + `
)`
		args = append(args, *req.Column)
	}
	query += ` ORDER BY ns.nspname COLLATE "C", cl.relname COLLATE "C", c.conname COLLATE "C", nf.nspname COLLATE "C", fcl.relname COLLATE "C", c.oid`

	rows, err := a.pool.Query(ctx, query, args...)
	if err != nil {
		return contract.Page[contract.RelationshipItem]{}, err
	}
	defer rows.Close()

	items := make([]keyedItem[contract.RelationshipItem], 0, limit+1)
	for rows.Next() {
		var ri contract.RelationshipItem
		var oid uint32
		var fromKind, toKind string
		var upd, del byte
		if err := rows.Scan(&oid, &ri.Name,
			&ri.From.Schema, &ri.From.Name,
			&ri.To.Schema, &ri.To.Name,
			&ri.FromColumns, &ri.ToColumns,
			&fromKind, &toKind,
			&upd, &del,
			&ri.Deferrable, &ri.InitiallyDeferred); err != nil {
			return contract.Page[contract.RelationshipItem]{}, err
		}
		if !a.relationKindAllowed(ri.From.Schema, ri.From.Name, fromKind) || !a.relationKindAllowed(ri.To.Schema, ri.To.Name, toKind) {
			continue
		}
		ri.OnUpdate = referentialAction(upd)
		ri.OnDelete = referentialAction(del)
		key := cursor.SortKey{Values: []string{ri.From.Schema, ri.From.Name, ri.Name, ri.To.Schema, ri.To.Name}, OID: oid}
		if !afterCursor(key, start) {
			continue
		}
		items = append(items, keyedItem[contract.RelationshipItem]{item: ri, key: key})
		if len(items) > limit {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return contract.Page[contract.RelationshipItem]{}, err
	}
	return finishPage(a, items, limit, "db_list_relationships", filters)
}

func (a *adapter) SuggestRelationships(ctx context.Context, req engine.SuggestRelationshipsRequest) (contract.Page[contract.SuggestedRelationship], error) {
	if req.Table != nil && req.Schema == nil {
		return contract.Page[contract.SuggestedRelationship]{}, &contract.PublicError{Code: contract.CodeInvalidRequest, Message: "schema is required when table is provided"}
	}
	if req.Schema != nil && !a.schemaAllowed(ctx, *req.Schema) {
		return contract.Page[contract.SuggestedRelationship]{}, &contract.PublicError{Code: contract.CodeObjectNotAllowed, Message: "schema is not authorized"}
	}
	if req.Table != nil {
		if err := a.authorizeRelation(ctx, guard.Relation{Schema: *req.Schema, Name: *req.Table}); err != nil {
			return contract.Page[contract.SuggestedRelationship]{}, err
		}
	}
	filters := metadataFilters(derefString(req.Schema), req.Table)
	start, limit, err := a.paginationStart(req.Page, "db_suggest_relationships", filters, 5)
	if err != nil {
		return contract.Page[contract.SuggestedRelationship]{}, err
	}

	schemas, err := a.allowedSchemas(ctx)
	if err != nil {
		return contract.Page[contract.SuggestedRelationship]{}, err
	}
	if req.Schema != nil {
		schemas = []string{*req.Schema}
	}

	// Generic, ubiquitous columns are excluded: they name no entity and would
	// only produce noise (e.g. every table has "id"/"created_at").
	query := `
SELECT a1.attname,
       n1.nspname, c1.relname, n2.nspname, c2.relname,
       format_type(a1.atttypid, a1.atttypmod),
       c1.oid
FROM pg_attribute a1
JOIN pg_class c1 ON c1.oid = a1.attrelid
JOIN pg_namespace n1 ON n1.oid = c1.relnamespace
JOIN pg_attribute a2 ON a2.attname = a1.attname AND a2.atttypid = a1.atttypid
JOIN pg_class c2 ON c2.oid = a2.attrelid
JOIN pg_namespace n2 ON n2.oid = c2.relnamespace
WHERE n1.nspname = ANY($1)
  AND n2.nspname = ANY($1)
  AND c1.relkind IN ('r','v','m','p')
  AND c2.relkind IN ('r','v','m','p')
  AND a1.attnum > 0 AND NOT a1.attisdropped
  AND a2.attnum > 0 AND NOT a2.attisdropped
  AND c1.oid < c2.oid
  AND lower(a1.attname) NOT IN ('id','created_at','updated_at','deleted_at','created','modified')`
	args := []any{schemas}
	if req.Table != nil {
		query += ` AND ( (n1.nspname = $2 AND c1.relname = $3) OR (n2.nspname = $2 AND c2.relname = $3) )`
		args = append(args, *req.Schema, *req.Table)
	}
	query += `
ORDER BY lower(a1.attname) COLLATE "C", n1.nspname COLLATE "C", c1.relname COLLATE "C", n2.nspname COLLATE "C", c2.relname COLLATE "C", c1.oid`

	rows, err := a.pool.Query(ctx, query, args...)
	if err != nil {
		return contract.Page[contract.SuggestedRelationship]{}, err
	}
	defer rows.Close()

	items := make([]keyedItem[contract.SuggestedRelationship], 0, limit+1)
	for rows.Next() {
		var it contract.SuggestedRelationship
		var oid uint32
		if err := rows.Scan(&it.Column, &it.Source.Schema, &it.Source.Name, &it.Target.Schema, &it.Target.Name, &it.PostgresType, &oid); err != nil {
			return contract.Page[contract.SuggestedRelationship]{}, err
		}
		if !a.relationKindAllowed(it.Source.Schema, it.Source.Name, "r") || !a.relationKindAllowed(it.Target.Schema, it.Target.Name, "r") {
			continue
		}
		it.SourceColumn = it.Column
		it.TargetColumn = it.Column
		key := cursor.SortKey{Values: []string{it.Column, it.Source.Schema, it.Source.Name, it.Target.Schema, it.Target.Name}, OID: oid}
		if !afterCursor(key, start) {
			continue
		}
		items = append(items, keyedItem[contract.SuggestedRelationship]{item: it, key: key})
		if len(items) > limit {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return contract.Page[contract.SuggestedRelationship]{}, err
	}
	return finishPage(a, items, limit, "db_suggest_relationships", filters)
}

func (a *adapter) SearchColumns(ctx context.Context, req engine.SearchColumnsRequest) (contract.Page[contract.ColumnSearchItem], error) {
	if req.Pattern == "" {
		return contract.Page[contract.ColumnSearchItem]{}, &contract.PublicError{Code: contract.CodeInvalidRequest, Message: "pattern is required"}
	}
	if req.Schema != nil && !a.schemaAllowed(ctx, *req.Schema) {
		return contract.Page[contract.ColumnSearchItem]{}, &contract.PublicError{Code: contract.CodeObjectNotAllowed, Message: "schema is not authorized"}
	}
	if req.Table != nil && req.Schema == nil {
		return contract.Page[contract.ColumnSearchItem]{}, &contract.PublicError{Code: contract.CodeInvalidRequest, Message: "schema is required when table is provided"}
	}
	if req.Table != nil {
		if err := a.authorizeRelation(ctx, guard.Relation{Schema: *req.Schema, Name: *req.Table}); err != nil {
			return contract.Page[contract.ColumnSearchItem]{}, err
		}
	}
	filters := metadataFilters(derefString(req.Schema), req.Table)
	filters["pattern"] = req.Pattern
	start, limit, err := a.paginationStart(req.Page, "db_search_columns", filters, 3)
	if err != nil {
		return contract.Page[contract.ColumnSearchItem]{}, err
	}

	schemas, err := a.allowedSchemas(ctx)
	if err != nil {
		return contract.Page[contract.ColumnSearchItem]{}, err
	}
	if req.Schema != nil {
		schemas = []string{*req.Schema}
	}
	pattern := columnPatternLike(req.Pattern)
	query := `
SELECT c.oid, n.nspname, c.relname,
       CASE c.relkind
        WHEN 'r' THEN 'table'
        WHEN 'v' THEN 'view'
        WHEN 'm' THEN 'materialized_view'
        WHEN 'p' THEN 'partitioned_table'
        ELSE 'other'
       END AS kind,
       a.attname, a.attnum, format_type(a.atttypid, a.atttypmod), NOT a.attnotnull
FROM pg_attribute a
JOIN pg_class c ON c.oid = a.attrelid
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = ANY($1)
  AND c.relkind IN ('r','v','m','p')
  AND a.attnum > 0
  AND NOT a.attisdropped
  AND a.attname ILIKE $2 ESCAPE '\'`
	args := []any{schemas, pattern}
	if req.Table != nil {
		query += ` AND c.relname = $3`
		args = append(args, *req.Table)
	}
	query += `
ORDER BY n.nspname COLLATE "C", c.relname COLLATE "C", a.attname COLLATE "C", c.oid`
	rows, err := a.pool.Query(ctx, query, args...)
	if err != nil {
		return contract.Page[contract.ColumnSearchItem]{}, err
	}
	defer rows.Close()

	items := make([]keyedItem[contract.ColumnSearchItem], 0, limit+1)
	for rows.Next() {
		var it contract.ColumnSearchItem
		var oid uint32
		if err := rows.Scan(&oid, &it.Schema, &it.Table, &it.Kind, &it.Column, &it.Ordinal, &it.PostgresType, &it.Nullable); err != nil {
			return contract.Page[contract.ColumnSearchItem]{}, err
		}
		if !a.relationKindAllowed(it.Schema, it.Table, relkindForContractKind(it.Kind)) {
			continue
		}
		it.Sensitive = sensitiveColumnName(it.Column)
		key := cursor.SortKey{Values: []string{it.Schema, it.Table, it.Column}, OID: oid}
		if !afterCursor(key, start) {
			continue
		}
		items = append(items, keyedItem[contract.ColumnSearchItem]{item: it, key: key})
		if len(items) > limit {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return contract.Page[contract.ColumnSearchItem]{}, err
	}
	return finishPage(a, items, limit, "db_search_columns", filters)
}

// defaultReferencePatterns are the column-name substrings scanned by
// db_find_references when the caller supplies no explicit patterns. They cover
// the common identity/actor columns (users, persons, requesters, reviewers,
// processors) across the supported schemas.
var defaultReferencePatterns = []string{
	"%user%", "%usuario%", "%persona%", "%solicitado%", "%revisado%",
	"%responsable%", "%procesado%",
}

// FindReferences discovers, among authorized columns whose names match the
// requested patterns, where a given value appears, returning a match count per
// column. It is a discovery convenience: each match is an exact text equality
// (or an exact bigint equality for integer-typed columns) and is bounded by the
// metadata page size so a single call stays fast and predictable.
func (a *adapter) FindReferences(ctx context.Context, req engine.FindReferencesRequest) ([]contract.ReferenceMatch, error) {
	value := strings.TrimSpace(req.Value)
	if value == "" {
		return nil, &contract.PublicError{Code: contract.CodeInvalidRequest, Message: "value is required"}
	}
	if req.Schema != nil && !a.schemaAllowed(ctx, *req.Schema) {
		return nil, &contract.PublicError{Code: contract.CodeObjectNotAllowed, Message: "schema is not authorized"}
	}
	patterns := req.Patterns
	if len(patterns) == 0 {
		patterns = defaultReferencePatterns
	}
	likes := make([]string, 0, len(patterns))
	for _, p := range patterns {
		likes = append(likes, columnPatternLike(p))
	}

	schemas, err := a.allowedSchemas(ctx)
	if err != nil {
		return nil, err
	}
	if req.Schema != nil {
		schemas = []string{*req.Schema}
	}

	capColumns := a.limit.MetadataPageSize
	if capColumns <= 0 {
		capColumns = 100
	}

	type candidate struct {
		schema  string
		table   string
		column  string
		kind    string
		colType string
	}
	rows, err := a.pool.Query(ctx, `
SELECT n.nspname, c.relname,
       CASE c.relkind
        WHEN 'r' THEN 'table'
        WHEN 'v' THEN 'view'
        WHEN 'm' THEN 'materialized_view'
        WHEN 'p' THEN 'partitioned_table'
        ELSE 'other'
       END AS kind,
       a.attname, format_type(a.atttypid, a.atttypmod)
FROM pg_attribute a
JOIN pg_class c ON c.oid = a.attrelid
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = ANY($1)
  AND c.relkind IN ('r','v','m','p')
  AND a.attnum > 0
  AND NOT a.attisdropped
  AND a.attname ILIKE ANY($2)
ORDER BY n.nspname COLLATE "C", c.relname COLLATE "C", a.attname COLLATE "C"`, schemas, likes)
	if err != nil {
		return nil, err
	}
	var candidates []candidate
	for rows.Next() {
		var cd candidate
		if err := rows.Scan(&cd.schema, &cd.table, &cd.kind, &cd.column, &cd.colType); err != nil {
			rows.Close()
			return nil, err
		}
		if !a.relationKindAllowed(cd.schema, cd.table, relkindForContractKind(cd.kind)) {
			continue
		}
		candidates = append(candidates, cd)
		if len(candidates) >= capColumns {
			break
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	type scored struct {
		item contract.ReferenceMatch
		n    int64
	}
	out := make([]scored, 0, len(candidates))
	params := []contract.ParameterInput{{Type: "text", Value: value}}
	for _, cd := range candidates {
		sql := buildReferenceCountSQL(cd.schema, cd.table, cd.column, cd.colType, value)
		p, perr := a.prepare(ctx, sql, params)
		if perr != nil {
			continue
		}
		res, qerr := a.executeSelect(ctx, p, 1, a.limit.ResponseBytes)
		if qerr != nil {
			continue
		}
		if len(res.Rows) == 0 || len(res.Rows[0]) == 0 {
			continue
		}
		countText, ok := res.Rows[0][0].(string)
		if !ok {
			continue
		}
		n, perr := strconv.ParseInt(countText, 10, 64)
		if perr != nil || n == 0 {
			continue
		}
		out = append(out, scored{
			item: contract.ReferenceMatch{Schema: cd.schema, Table: cd.table, Column: cd.column, Matches: countText},
			n:    n,
		})
	}
	slices.SortFunc(out, func(x, y scored) int {
		if x.n != y.n {
			if x.n > y.n {
				return -1
			}
			return 1
		}
		if x.item.Schema != y.item.Schema {
			return strings.Compare(x.item.Schema, y.item.Schema)
		}
		if x.item.Table != y.item.Table {
			return strings.Compare(x.item.Table, y.item.Table)
		}
		return strings.Compare(x.item.Column, y.item.Column)
	})
	result := make([]contract.ReferenceMatch, len(out))
	for i, s := range out {
		result[i] = s.item
	}
	return result, nil
}

func (a *adapter) Close(ctx context.Context) error {
	a.pool.Close()
	return nil
}

func (a *adapter) schemaAllowed(ctx context.Context, schema string) bool {
	if a.unrestricted() {
		return !isSystemSchema(schema)
	}
	schemas, err := a.allowedSchemas(ctx)
	if err != nil {
		return false
	}
	for _, s := range schemas {
		if s == schema {
			return true
		}
	}
	return false
}

// unrestricted reports whether the connection exposes everything the role can
// read because no allowlist is configured (SDD §8.3).
func (a *adapter) unrestricted() bool {
	return a.cfg.Allow == nil || a.cfg.Allow.Empty()
}

// isSystemSchema reports whether a schema is an internal PostgreSQL schema
// that is never exposed as user metadata.
func isSystemSchema(schema string) bool {
	return schema == "information_schema" || strings.HasPrefix(schema, "pg_")
}

func schemaAllowsTable(cfg config.Connection, schema string) bool {
	for _, s := range cfg.Allow.Schemas {
		if s == schema {
			return true
		}
	}
	return false
}

func parseServerMajor(version string) int {
	// e.g. "PostgreSQL 16.3 (Debian ...) on ..."
	fields := strings.Fields(version)
	if len(fields) < 2 || fields[0] != "PostgreSQL" {
		return 0
	}
	parts := strings.SplitN(fields[1], ".", 2)
	major := 0
	fmt.Sscanf(parts[0], "%d", &major)
	return major
}

func referentialAction(b byte) string {
	switch b {
	case 'a':
		return "NO ACTION"
	case 'r':
		return "RESTRICT"
	case 'c':
		return "CASCADE"
	case 'n':
		return "SET NULL"
	case 'd':
		return "SET DEFAULT"
	default:
		return "NO ACTION"
	}
}

type relationMeta struct {
	oid  uint32
	kind string
}

func (a *adapter) relationMeta(ctx context.Context, ref engine.TableRef) (relationMeta, error) {
	var oid uint32
	var kind string
	err := a.pool.QueryRow(ctx, `
SELECT c.oid,
       CASE c.relkind
         WHEN 'r' THEN 'table'
         WHEN 'v' THEN 'view'
         WHEN 'm' THEN 'materialized_view'
         WHEN 'p' THEN 'partitioned_table'
         ELSE 'other'
       END
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = $1 AND c.relname = $2`, ref.Schema, ref.Table).Scan(&oid, &kind)
	return relationMeta{oid: oid, kind: kind}, err
}

func (a *adapter) constraintPrimaryKey(ctx context.Context, ref engine.TableRef) (*contract.KeyConstraint, error) {
	var kc contract.KeyConstraint
	err := a.pool.QueryRow(ctx, `
SELECT c.conname,
       ARRAY(SELECT a.attname FROM unnest(c.conkey) WITH ORDINALITY k(attnum, ord)
             JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = k.attnum
             ORDER BY k.ord),
       c.condeferrable, c.condeferred
FROM pg_constraint c
JOIN pg_class cl ON cl.oid = c.conrelid
JOIN pg_namespace n ON n.oid = cl.relnamespace
WHERE n.nspname = $1 AND cl.relname = $2 AND c.contype = 'p'`,
		ref.Schema, ref.Table).Scan(&kc.Name, &kc.Columns, &kc.Deferrable, &kc.InitiallyDeferred)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &kc, nil
}

func (a *adapter) constraints(ctx context.Context, ref engine.TableRef) (unique []contract.KeyConstraint, checks, exclusions []contract.CheckConstraint, err error) {
	rows, err := a.pool.Query(ctx, `
SELECT c.conname, c.contype,
       ARRAY(SELECT a.attname FROM unnest(c.conkey) WITH ORDINALITY k(attnum, ord)
             JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = k.attnum
             ORDER BY k.ord),
       c.condeferrable, c.condeferred,
       c.convalidated
FROM pg_constraint c
JOIN pg_class cl ON cl.oid = c.conrelid
JOIN pg_namespace n ON n.oid = cl.relnamespace
WHERE n.nspname = $1 AND cl.relname = $2
  AND c.contype IN ('u','c','x')`, ref.Schema, ref.Table)
	if err != nil {
		return nil, nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var typ byte
		var cols []string
		var deferrable, deferred, validated bool
		if err := rows.Scan(&name, &typ, &cols, &deferrable, &deferred, &validated); err != nil {
			return nil, nil, nil, err
		}
		switch typ {
		case 'u':
			unique = append(unique, contract.KeyConstraint{Name: name, Columns: cols, Deferrable: deferrable, InitiallyDeferred: deferred})
		case 'c':
			checks = append(checks, contract.CheckConstraint{Name: name, Validated: validated})
		case 'x':
			exclusions = append(exclusions, contract.CheckConstraint{Name: name, Validated: validated})
		}
	}
	return unique, checks, exclusions, rows.Err()
}

func (a *adapter) foreignKeyConstraints(ctx context.Context, ref engine.TableRef) ([]contract.RelationshipItem, error) {
	rows, err := a.pool.Query(ctx, `
SELECT c.conname,
       ns.nspname, cl.relname,
       nf.nspname, fcl.relname,
       fcl.relkind,
       c.confupdtype, c.confdeltype,
       c.condeferrable, c.condeferred
FROM pg_constraint c
JOIN pg_class cl ON cl.oid = c.conrelid
JOIN pg_namespace ns ON ns.oid = cl.relnamespace
JOIN pg_class fcl ON fcl.oid = c.confrelid
JOIN pg_namespace nf ON nf.oid = fcl.relnamespace
WHERE c.contype = 'f' AND ns.nspname = $1 AND cl.relname = $2
ORDER BY c.conname`, ref.Schema, ref.Table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []contract.RelationshipItem{}
	for rows.Next() {
		var ri contract.RelationshipItem
		var toKind string
		var upd, del byte
		if err := rows.Scan(&ri.Name,
			&ri.From.Schema, &ri.From.Name,
			&ri.To.Schema, &ri.To.Name,
			&toKind,
			&upd, &del, &ri.Deferrable, &ri.InitiallyDeferred); err != nil {
			return nil, err
		}
		if !a.relationKindAllowed(ri.To.Schema, ri.To.Name, toKind) {
			continue
		}
		ri.OnUpdate = referentialAction(upd)
		ri.OnDelete = referentialAction(del)
		out = append(out, ri)
	}
	return out, rows.Err()
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// expandKeys maps the raw int2vector text (space-separated attribute numbers)
// into the contract's IndexKey list. "0" marks an expression key.
func (a *adapter) expandKeys(ctx context.Context, ref engine.TableRef, indkeyStr string, nkeyatts int16, exprs *string) []contract.IndexKey {
	_ = exprs
	fields := strings.Fields(indkeyStr)
	keys := make([]contract.IndexKey, 0, len(fields))
	for i, f := range fields {
		if int16(i) >= nkeyatts {
			break
		}
		if f == "0" {
			keys = append(keys, contract.IndexKey{Expression: true})
			continue
		}
		var attnum int
		fmt.Sscanf(f, "%d", &attnum)
		var col string
		err := a.pool.QueryRow(ctx, `
SELECT a.attname FROM pg_attribute a
WHERE a.attrelid = $1::regclass::oid AND a.attnum = $2`,
			ref.Schema+"."+ref.Table, attnum).Scan(&col)
		if err != nil {
			keys = append(keys, contract.IndexKey{Expression: true})
			continue
		}
		keys = append(keys, contract.IndexKey{Column: &col})
	}
	return keys
}

// columnPatternLike turns a caller-supplied column name pattern into a literal
// substring ILIKE pattern. Leading/trailing "%" are stripped so callers can
// pass "%name%" out of LIKE habit; the remaining "%", "_" and "\" are escaped
// so they match literally rather than acting as wildcards.
func columnPatternLike(pattern string) string {
	s := strings.TrimSpace(pattern)
	s = strings.Trim(s, "%")
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return "%" + s + "%"
}

// isIntegerType reports whether a format_type label is an integer-family type.
func isIntegerType(t string) bool {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "smallint", "integer", "bigint", "int2", "int4", "int8":
		return true
	default:
		return false
	}
}

// buildReferenceCountSQL builds a single-column COUNT query for
// db_find_references. Integer-typed columns compare against a bigint cast
// (index-friendly) when the value parses as an integer; every other column
// compares by text equality so no type-cast error can abort the scan.
func buildReferenceCountSQL(schema, table, column, colType, value string) string {
	ref := quoteIdent(schema) + "." + quoteIdent(table)
	colRef := quoteIdent(column)
	if isIntegerType(colType) {
		if _, err := strconv.ParseInt(value, 10, 64); err == nil {
			return fmt.Sprintf("SELECT count(*)::text FROM %s WHERE %s = $1::bigint", ref, colRef)
		}
	}
	return fmt.Sprintf("SELECT count(*)::text FROM %s WHERE %s::text = $1::text", ref, colRef)
}
