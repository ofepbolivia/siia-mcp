package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/siia/siia-mcp/internal/contract"
	"github.com/siia/siia-mcp/internal/engine"
	"github.com/siia/siia-mcp/internal/postgres/guard"
	"github.com/siia/siia-mcp/internal/requestctx"
)

// preparedQuery bundles the validated components of a read-only request.
type preparedQuery struct {
	sql  string
	args []any
	gres *guard.Result
}

// checkSQL runs the AST guard through the per-adapter cache when present, or
// directly otherwise (adapter values built by tests may omit the cache).
func (a *adapter) checkSQL(sql string) (*guard.Result, error) {
	if a.checker != nil {
		return a.checker.Check(sql)
	}
	return guard.Check(sql)
}

// prepare validates one SQL request end-to-end: byte limit, guard, object
// policy, and parameters.
func (a *adapter) prepare(ctx context.Context, sql string, params []contract.ParameterInput) (*preparedQuery, error) {
	if len(sql) > a.limit.QueryBytes {
		return nil, &contract.PublicError{Code: contract.CodeInputLimitExceeded, Message: "query exceeds byte limit"}
	}
	gres, err := a.checkSQL(sql)
	if err != nil {
		return nil, err
	}
	if err := a.authorizeRelations(ctx, gres.Relations); err != nil {
		return nil, err
	}
	args, err := buildArgs(params, a.limit)
	if err != nil {
		return nil, err
	}
	if err := validatePlaceholders(gres, len(args)); err != nil {
		return nil, err
	}
	return &preparedQuery{sql: sql, args: args, gres: gres}, nil
}

// executeSelect runs a prepared read-only SELECT in an explicit read-only
// transaction and encodes the result (SDD §9.4, §14).
func (a *adapter) executeSelect(ctx context.Context, p *preparedQuery, maxRows, maxPayload int) (contract.QueryResult, error) {
	conn, err := a.pool.Acquire(ctx)
	if err != nil {
		return contract.QueryResult{}, wrapDB(err)
	}
	rollbackFailed := false
	defer func() {
		cleanup, cancel := requestctx.CleanupContext(ctx)
		defer cancel()
		if rollbackFailed {
			_ = conn.Hijack().Close(cleanup)
			return
		}
		if _, err := conn.Conn().Exec(cleanup, "DISCARD ALL"); err != nil {
			_ = conn.Hijack().Close(cleanup)
			return
		}
		allowedSchemas, _ := a.allowedSchemas(cleanup)
		if err := setupSession(cleanup, conn.Conn(), a.session, allowedSchemas); err != nil {
			_ = conn.Hijack().Close(cleanup)
			return
		}
		conn.Release()
	}()

	tx, err := conn.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return contract.QueryResult{}, wrapDB(err)
	}
	defer func() {
		cleanup, cancel := requestctx.CleanupContext(ctx)
		defer cancel()
		if err := tx.Rollback(cleanup); err != nil {
			rollbackFailed = true
		}
	}()

	rows, err := tx.Query(ctx, p.sql, p.args...)
	if err != nil {
		return contract.QueryResult{}, a.enrichColumnError(ctx, err, p.gres.Relations)
	}
	defer rows.Close()

	fds := rows.FieldDescriptions()
	cols := make([]contract.Column, len(fds))
	for i, fd := range fds {
		oid := fd.DataTypeOID
		cols[i] = contract.Column{
			Name:         fd.Name,
			PostgresType: pgTypeName(oid),
			TypeOID:      int(oid),
			Format:       classifyFormat(oid),
			Sensitive:    sensitiveColumnName(fd.Name),
		}
	}

	var out contract.QueryResult
	out.Columns = cols
	out.Rows = make([][]any, 0)
	truncated, reason := false, ""
	rowsPayloadBytes := 0
	rowSizes := []int{}
	for rows.Next() {
		if len(out.Rows) >= maxRows {
			truncated, reason = true, "rows"
			break
		}
		raw := rows.RawValues()
		row := make([]any, len(raw))
		for i, rv := range raw {
			if cols[i].Sensitive {
				row[i] = "[REDACTED]"
				continue
			}
			if valueWouldExceedLimit(fds[i].DataTypeOID, rv, a.limit.ValueBytes) {
				return contract.QueryResult{}, &contract.PublicError{Code: contract.CodeResultValueTooLarge, Message: "result value exceeds byte limit"}
			}
			val, eerr := encodeValue(fds[i].DataTypeOID, rv)
			if eerr != nil {
				return contract.QueryResult{}, eerr
			}
			encoded, eerr := json.Marshal(val)
			if eerr != nil {
				return contract.QueryResult{}, &contract.PublicError{Code: contract.CodeInternalError, Message: "result encoding failed"}
			}
			if len(encoded) > a.limit.ValueBytes {
				return contract.QueryResult{}, &contract.PublicError{Code: contract.CodeResultValueTooLarge, Message: "result value exceeds byte limit"}
			}
			row[i] = val
		}
		encodedRow, err := json.Marshal(row)
		if err != nil {
			return contract.QueryResult{}, &contract.PublicError{Code: contract.CodeInternalError, Message: "result encoding failed"}
		}
		out.Rows = append(out.Rows, row)
		out.RowCount = len(out.Rows)
		candidateRowsBytes := rowsPayloadBytes + len(encodedRow)
		if out.RowCount > 1 {
			candidateRowsBytes++
		}
		if resultEnvelopeBytes(out, candidateRowsBytes) > maxPayload {
			out.Rows = out.Rows[:len(out.Rows)-1]
			out.RowCount = len(out.Rows)
			truncated, reason = true, "payload"
			break
		}
		rowsPayloadBytes = candidateRowsBytes
		rowSizes = append(rowSizes, len(encodedRow))
	}
	if err := rows.Err(); err != nil {
		return contract.QueryResult{}, wrapDB(err)
	}
	out.RowCount = len(out.Rows)
	if truncated {
		out.Truncated = true
		out.TruncationReason = &reason
		for resultEnvelopeBytes(out, rowsPayloadBytes) > maxPayload && len(out.Rows) > 0 {
			last := len(out.Rows) - 1
			rowsPayloadBytes -= rowSizes[last]
			if last > 0 {
				rowsPayloadBytes--
			}
			out.Rows = out.Rows[:last]
			rowSizes = rowSizes[:last]
			out.RowCount = len(out.Rows)
		}
	}
	if resultEnvelopeBytes(out, rowsPayloadBytes) > maxPayload {
		return contract.QueryResult{}, &contract.PublicError{Code: contract.CodeResultValueTooLarge, Message: "response metadata exceeds byte limit"}
	}
	return out, nil
}

func (a *adapter) Query(ctx context.Context, req engine.QueryRequest) (contract.QueryResult, error) {
	p, err := a.prepare(ctx, req.SQL, req.Parameters)
	if err != nil {
		return contract.QueryResult{}, err
	}
	maxRows, maxPayload, err := a.effectiveLimits(req.Limits)
	if err != nil {
		return contract.QueryResult{}, err
	}
	return a.executeSelect(ctx, p, maxRows, maxPayload)
}

func (a *adapter) SampleRows(ctx context.Context, req engine.SampleRequest) (engine.SampleResult, error) {
	ref := req.TableRef
	if err := a.authorizeRelation(ctx, guard.Relation{Schema: ref.Schema, Name: ref.Table}); err != nil {
		return engine.SampleResult{}, err
	}
	maxRows := req.MaxRows
	if maxRows <= 0 {
		maxRows = a.limit.SampleRows
	} else if maxRows > a.limit.SampleRows {
		return engine.SampleResult{}, invalidLimit("max_rows")
	}
	pk := a.primaryKeyColumns(ctx, ref.Schema, ref.Table)
	sql := buildSampleSQL(ref.Schema, ref.Table, pk, maxRows+1)
	p, err := a.prepare(ctx, sql, nil)
	if err != nil {
		return engine.SampleResult{}, err
	}
	maxPayload := samplePayloadBudget(a.limit.ResponseBytes, len(pk) > 0)
	if maxPayload <= 0 {
		return engine.SampleResult{}, &contract.PublicError{Code: contract.CodeResultValueTooLarge, Message: "response metadata exceeds byte limit"}
	}
	res, err := a.executeSelect(ctx, p, maxRows, maxPayload)
	if err != nil {
		return engine.SampleResult{}, err
	}
	return engine.SampleResult{Result: res, Deterministic: len(pk) > 0}, nil
}

func (a *adapter) CountRows(ctx context.Context, ref engine.TableRef, estimate bool) (engine.CountResult, error) {
	if err := a.authorizeRelation(ctx, guard.Relation{Schema: ref.Schema, Name: ref.Table}); err != nil {
		return engine.CountResult{}, err
	}
	var sql string
	if estimate {
		// Use pg_class.reltuples for fast estimation without table scan.
		sql = fmt.Sprintf(`SELECT reltuples::text FROM pg_class WHERE oid = '%s.%s'::regclass::oid`, quoteIdent(ref.Schema), quoteIdent(ref.Table))
		res, err := a.executeSelect(ctx, &preparedQuery{sql: sql}, 1, a.limit.ResponseBytes)
		if err != nil {
			return engine.CountResult{}, err
		}
		if len(res.Rows) == 0 || len(res.Rows[0]) == 0 {
			return engine.CountResult{}, &contract.PublicError{Code: contract.CodeInternalError, Message: "count returned no rows"}
		}
		countText, ok := res.Rows[0][0].(string)
		if !ok {
			return engine.CountResult{}, &contract.PublicError{Code: contract.CodeInternalError, Message: "count not a string"}
		}
		// reltuples is -1 if unknown or table is empty
		if countText == "-1" {
			countText = "0"
		}
		return engine.CountResult{Count: countText, Estimated: true}, nil
	}
	sql = fmt.Sprintf("SELECT count(*)::text FROM %s.%s", quoteIdent(ref.Schema), quoteIdent(ref.Table))
	p, err := a.prepare(ctx, sql, nil)
	if err != nil {
		return engine.CountResult{}, err
	}
	res, err := a.executeSelect(ctx, p, 1, a.limit.ResponseBytes)
	if err != nil {
		return engine.CountResult{}, err
	}
	if len(res.Rows) == 0 || len(res.Rows[0]) == 0 {
		return engine.CountResult{}, &contract.PublicError{Code: contract.CodeInternalError, Message: "count returned no rows"}
	}
	countText, ok := res.Rows[0][0].(string)
	if !ok {
		return engine.CountResult{}, &contract.PublicError{Code: contract.CodeInternalError, Message: "count not a string"}
	}
	return engine.CountResult{Count: countText, Estimated: false}, nil
}

func (a *adapter) effectiveLimits(over *engine.ExecutionLimits) (maxRows, maxPayload int, err error) {
	maxRows, maxPayload = a.limit.QueryRows, a.limit.ResponseBytes
	if over != nil {
		if over.MaxRows != nil {
			if *over.MaxRows <= 0 || *over.MaxRows > maxRows {
				return 0, 0, invalidLimit("max_rows")
			}
			maxRows = *over.MaxRows
		}
		if over.MaxResponseBytes != nil {
			if *over.MaxResponseBytes <= 0 || *over.MaxResponseBytes > maxPayload {
				return 0, 0, invalidLimit("max_response_bytes")
			}
			maxPayload = *over.MaxResponseBytes
		}
	}
	return maxRows, maxPayload, nil
}

func invalidLimit(name string) error {
	return &contract.PublicError{Code: contract.CodeInvalidRequest, Message: name + " exceeds configured limit"}
}

func resultEnvelopeBytes(result contract.QueryResult, rowsPayloadBytes int) int {
	result.Rows = [][]any{}
	raw, err := json.Marshal(contract.SuccessEnvelope{
		SchemaVersion: contract.SchemaVersion,
		Data:          result,
		Meta: contract.SuccessMeta{
			DurationMS:       int64(^uint64(0) >> 1),
			Truncated:        result.Truncated,
			TruncationReason: result.TruncationReason,
		},
	})
	if err != nil {
		return int(^uint(0) >> 1)
	}
	return len(raw) + rowsPayloadBytes
}

func samplePayloadBudget(maxPayload int, deterministic bool) int {
	empty := contract.QueryResult{}
	direct := resultEnvelopeBytes(empty, 0)
	raw, err := json.Marshal(contract.SuccessEnvelope{
		SchemaVersion: contract.SchemaVersion,
		Data: map[string]any{
			"result":        empty,
			"deterministic": deterministic,
		},
		Meta: contract.SuccessMeta{DurationMS: int64(^uint64(0) >> 1)},
	})
	if err != nil {
		return 0
	}
	return maxPayload - (len(raw) - direct)
}

func valueWouldExceedLimit(oid uint32, data []byte, limit int) bool {
	if data == nil {
		return false
	}
	if oid == 17 {
		hexLen := len(data)
		if hexLen >= 2 && data[0] == '\\' && data[1] == 'x' {
			hexLen -= 2
		}
		decodedLen := (hexLen + 1) / 2
		base64Len := ((decodedLen + 2) / 3) * 4
		return base64Len+2 > limit
	}
	switch oid {
	case 16, 21, 23, 700, 701:
		return len(data) > limit
	default:
		return jsonStringBytes(data) > limit
	}
}

func jsonStringBytes(data []byte) int {
	size := 2
	for len(data) > 0 {
		r, width := utf8.DecodeRune(data)
		if r == utf8.RuneError && width == 1 {
			size += 6
			data = data[1:]
			continue
		}
		data = data[width:]
		switch r {
		case '\\', '"', '\b', '\f', '\n', '\r', '\t':
			size += 2
		case '<', '>', '&', '\u2028', '\u2029':
			size += 6
		default:
			if r < 0x20 {
				size += 6
			} else {
				size += width
			}
		}
	}
	return size
}

// primaryKeyColumns returns the ordered primary-key column names of a relation.
// The result is cached because it only changes on DDL; a copy is returned so the
// caller never aliases the cached slice.
func (a *adapter) primaryKeyColumns(ctx context.Context, schema, table string) []string {
	val, err := a.catalogGet(ctx, a.catalogKey("pk", schema, table), func(ctx context.Context) (any, error) {
		rows, err := a.pool.Query(ctx, `
SELECT a.attname
FROM pg_index idx
JOIN pg_attribute a ON a.attrelid = idx.indrelid AND a.attnum = ANY(idx.indkey::int2[])
JOIN pg_class c ON c.oid = idx.indrelid
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = $1 AND c.relname = $2 AND idx.indisprimary
ORDER BY array_position(idx.indkey::int2[], a.attnum)`, schema, table)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var cols []string
		for rows.Next() {
			var c string
			if err := rows.Scan(&c); err == nil {
				cols = append(cols, c)
			}
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return cols, nil
	})
	if err != nil {
		return nil
	}
	cols, _ := val.([]string)
	return append([]string(nil), cols...)
}

func buildSampleSQL(schema, table string, pk []string, limit int) string {
	var b strings.Builder
	b.WriteString("SELECT * FROM ")
	b.WriteString(quoteIdent(schema))
	b.WriteString(".")
	b.WriteString(quoteIdent(table))
	if len(pk) > 0 {
		b.WriteString(" ORDER BY ")
		for i, c := range pk {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(quoteIdent(c))
			b.WriteString(" NULLS LAST")
		}
	}
	fmt.Fprintf(&b, " LIMIT %d", limit)
	return b.String()
}

func pgTypeName(oid uint32) string {
	if n, ok := pgTypeNames[oid]; ok {
		return n
	}
	return "unknown"
}

var pgTypeNames = map[uint32]string{
	16: "bool", 17: "bytea", 19: "name",
	20: "int8", 21: "int2", 23: "int4", 25: "text",
	26: "oid", 114: "json", 700: "float4", 701: "float8",
	705: "unknown", 790: "money", 1042: "bpchar", 1043: "varchar",
	1082: "date", 1083: "time", 1114: "timestamp", 1184: "timestamptz",
	1186: "interval", 1700: "numeric", 2950: "uuid", 3802: "jsonb",
	3807: "jsonpath",
}

func wrapDB(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return &contract.PublicError{Code: contract.CodeDatabaseError, Message: pgErr.Message}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &contract.PublicError{Code: contract.CodeTimeout, Message: "query timed out"}
	}
	if errors.Is(err, context.Canceled) {
		return &contract.PublicError{Code: contract.CodeCancelled, Message: "query cancelled"}
	}
	return &contract.PublicError{Code: contract.CodeDatabaseError, Message: err.Error()}
}

// enrichColumnError enriches a PostgreSQL "column does not exist" error
// (SQLSTATE 42703) with the columns actually available on the referenced
// relations, so an agent can correct its query without a separate metadata
// round-trip (agent fluency improvement). Other errors pass through unchanged.
func (a *adapter) enrichColumnError(ctx context.Context, err error, rels []guard.Relation) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "42703" {
		return wrapDB(err)
	}
	var parts []string
	for _, r := range rels {
		if r.Schema == "" || r.Name == "" || isSystemSchema(r.Schema) {
			continue
		}
		cols := a.relationColumns(ctx, r.Schema, r.Name)
		if len(cols) == 0 {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s.%s: %s", r.Schema, r.Name, strings.Join(cols, ", ")))
	}
	msg := pgErr.Message
	if len(parts) > 0 {
		msg += " (available columns: " + strings.Join(parts, "; ") + ")"
	}
	return &contract.PublicError{Code: contract.CodeDatabaseError, Message: msg}
}

// relationColumns returns the column names of a relation, capped so the hint
// never balloons the error message. An empty result is returned on any error.
func (a *adapter) relationColumns(ctx context.Context, schema, table string) []string {
	rows, err := a.pool.Query(ctx, `
SELECT a.attname
FROM pg_attribute a
WHERE a.attrelid = $1::regclass::oid
  AND a.attnum > 0
  AND NOT a.attisdropped
ORDER BY a.attnum
LIMIT 50`, schema+"."+table)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil
		}
		cols = append(cols, c)
	}
	if rows.Err() != nil {
		return nil
	}
	return cols
}
