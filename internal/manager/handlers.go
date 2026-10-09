package manager

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/siia/siia-mcp/internal/contract"
	"github.com/siia/siia-mcp/internal/engine"
)

// lookup returns a ready engine connection for a logical connection name,
// triggering lazy initialization when the handle is not yet ready.
func (m *Manager) lookup(ctx context.Context, name string) (engine.Connection, error) {
	return m.registry.Lookup(ctx, name, func(ctx context.Context) (engine.Connection, error) {
		return m.initConnection(ctx, name, nil)
	})
}

func (m *Manager) decode(args json.RawMessage, into any) error {
	if len(args) == 0 {
		args = []byte(`{}`)
	}
	dec := json.NewDecoder(bytes.NewReader(args))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}

// metaKey builds the cache key for one authorized metadata result: immutable
// handle identity, operation, base filters, decoded start key and limit,
// allowlist digest, and contract schema version (SDD §16.2).
func (m *Manager) metaKey(connName, op string, parts ...string) string {
	out := connName + "|" + op + "|" + m.policyDigests[connName] + "|" + contract.SchemaVersion
	for _, p := range parts {
		out += "|" + p
	}
	return out
}

// metaGet reads/refreshes one metadata entry. Public policy/input errors retain
// their contract code; unexpected refresh failures map to METADATA_UNAVAILABLE.
func (m *Manager) metaGet(ctx context.Context, key string, refresh func(context.Context) (any, error)) (any, error) {
	val, _, err := m.meta.Get(ctx, key, refresh)
	if err != nil {
		if pe, ok := err.(*contract.PublicError); ok {
			return nil, pe
		}
		if err == context.Canceled {
			return nil, &contract.PublicError{Code: contract.CodeCancelled, Message: "request cancelled"}
		}
		if err == context.DeadlineExceeded {
			return nil, &contract.PublicError{Code: contract.CodeTimeout, Message: "metadata refresh timed out"}
		}
		return nil, &contract.PublicError{Code: contract.CodeMetadataUnavailable, Message: "metadata refresh failed"}
	}
	return val, nil
}

func (m *Manager) metadataRefresh(connectionName string, refresh func(context.Context) (any, error)) func(context.Context) (any, error) {
	return func(ctx context.Context) (any, error) {
		cfg, ok := m.byName[connectionName]
		if !ok || cfg.Limits == nil || cfg.Limits.RequestTimeout <= 0 {
			return refresh(ctx)
		}
		bounded, cancel := context.WithTimeout(ctx, cfg.Limits.RequestTimeout)
		defer cancel()
		return refresh(bounded)
	}
}

func (m *Manager) dbListConnections(ctx context.Context) (any, error) {
	items := make([]contract.ConnectionItem, 0, len(m.registry.List()))
	for _, h := range m.registry.List() {
		items = append(items, contract.ConnectionItem{
			Name:     h.Name(),
			State:    string(h.State()),
			Required: h.Required(),
		})
	}
	return items, nil
}

func (m *Manager) dbPing(ctx context.Context, args json.RawMessage) (any, error) {
	var in contract.PingInput
	if err := m.decode(args, &in); err != nil {
		return nil, &contract.PublicError{Code: contract.CodeInvalidRequest, Message: "invalid db_ping arguments"}
	}
	conn, err := m.lookup(ctx, in.Connection)
	if err != nil {
		return nil, err
	}
	res, err := conn.Ping(ctx)
	if err != nil {
		return nil, wrapDatabaseError(err)
	}
	return contract.PingResult{OK: res.OK, ServerMajor: res.ServerMajor}, nil
}

func (m *Manager) dbListSchemas(ctx context.Context, args json.RawMessage) (any, error) {
	var in contract.ListSchemasInput
	if err := m.decode(args, &in); err != nil {
		return nil, &contract.PublicError{Code: contract.CodeInvalidRequest, Message: "invalid db_list_schemas arguments"}
	}
	conn, err := m.lookup(ctx, in.Connection)
	if err != nil {
		return nil, err
	}
	key := m.metaKey(in.Connection, "list_schemas", derefStr(in.Cursor), fmt.Sprint(derefInt(in.Limit)))
	return m.metaGet(ctx, key, m.metadataRefresh(in.Connection, func(ctx context.Context) (any, error) {
		return conn.ListSchemas(ctx, engine.PageRequest{Limit: derefInt(in.Limit), Cursor: in.Cursor})
	}))
}

func (m *Manager) dbListTables(ctx context.Context, args json.RawMessage) (any, error) {
	var in contract.ListTablesInput
	if err := m.decode(args, &in); err != nil {
		return nil, &contract.PublicError{Code: contract.CodeInvalidRequest, Message: "invalid db_list_tables arguments"}
	}
	conn, err := m.lookup(ctx, in.Connection)
	if err != nil {
		return nil, err
	}
	schema := derefStr(in.Schema)
	key := m.metaKey(in.Connection, "list_tables", schema, derefStr(in.Cursor), fmt.Sprint(derefInt(in.Limit)))
	return m.metaGet(ctx, key, m.metadataRefresh(in.Connection, func(ctx context.Context) (any, error) {
		return conn.ListTables(ctx, engine.ListTablesRequest{
			Schema: schema,
			Page:   engine.PageRequest{Limit: derefInt(in.Limit), Cursor: in.Cursor},
		})
	}))
}

func (m *Manager) dbDescribeTable(ctx context.Context, args json.RawMessage) (any, error) {
	var in contract.DescribeTableInput
	if err := m.decode(args, &in); err != nil {
		return nil, &contract.PublicError{Code: contract.CodeInvalidRequest, Message: "invalid db_describe_table arguments"}
	}
	conn, err := m.lookup(ctx, in.Connection)
	if err != nil {
		return nil, err
	}
	key := m.metaKey(in.Connection, "describe_table", in.Schema, in.Table)
	return m.metaGet(ctx, key, m.metadataRefresh(in.Connection, func(ctx context.Context) (any, error) {
		return conn.DescribeTable(ctx, engine.TableRef{Schema: in.Schema, Table: in.Table})
	}))
}

func (m *Manager) dbListIndexes(ctx context.Context, args json.RawMessage) (any, error) {
	var in contract.ListIndexesInput
	if err := m.decode(args, &in); err != nil {
		return nil, &contract.PublicError{Code: contract.CodeInvalidRequest, Message: "invalid db_list_indexes arguments"}
	}
	conn, err := m.lookup(ctx, in.Connection)
	if err != nil {
		return nil, err
	}
	key := m.metaKey(in.Connection, "list_indexes", in.Schema, in.Table, derefStr(in.Cursor), fmt.Sprint(derefInt(in.Limit)))
	return m.metaGet(ctx, key, m.metadataRefresh(in.Connection, func(ctx context.Context) (any, error) {
		return conn.ListIndexes(ctx,
			engine.TableRef{Schema: in.Schema, Table: in.Table},
			engine.PageRequest{Limit: derefInt(in.Limit), Cursor: in.Cursor})
	}))
}

func (m *Manager) dbListRelationships(ctx context.Context, args json.RawMessage) (any, error) {
	var in contract.ListRelationshipsInput
	if err := m.decode(args, &in); err != nil {
		return nil, &contract.PublicError{Code: contract.CodeInvalidRequest, Message: "invalid db_list_relationships arguments"}
	}
	conn, err := m.lookup(ctx, in.Connection)
	if err != nil {
		return nil, err
	}
	key := m.metaKey(in.Connection, "list_relationships",
		derefStr(in.Schema), derefStr(in.Table), derefStr(in.ToSchema), derefStr(in.ToTable), derefStr(in.Column), derefStr(in.Cursor), fmt.Sprint(derefInt(in.Limit)))
	return m.metaGet(ctx, key, m.metadataRefresh(in.Connection, func(ctx context.Context) (any, error) {
		return conn.ListRelationships(ctx, engine.RelationshipRequest{
			Schema:   in.Schema,
			Table:    in.Table,
			ToSchema: in.ToSchema,
			ToTable:  in.ToTable,
			Column:   in.Column,
			Page:     engine.PageRequest{Limit: derefInt(in.Limit), Cursor: in.Cursor},
		})
	}))
}

func (m *Manager) dbSuggestRelationships(ctx context.Context, args json.RawMessage) (any, error) {
	var in contract.SuggestRelationshipsInput
	if err := m.decode(args, &in); err != nil {
		return nil, &contract.PublicError{Code: contract.CodeInvalidRequest, Message: "invalid db_suggest_relationships arguments"}
	}
	conn, err := m.lookup(ctx, in.Connection)
	if err != nil {
		return nil, err
	}
	key := m.metaKey(in.Connection, "suggest_relationships", derefStr(in.Schema), derefStr(in.Table), derefStr(in.Cursor), fmt.Sprint(derefInt(in.Limit)))
	return m.metaGet(ctx, key, m.metadataRefresh(in.Connection, func(ctx context.Context) (any, error) {
		return conn.SuggestRelationships(ctx, engine.SuggestRelationshipsRequest{
			Schema: in.Schema,
			Table:  in.Table,
			Page:   engine.PageRequest{Limit: derefInt(in.Limit), Cursor: in.Cursor},
		})
	}))
}

func (m *Manager) dbSearchColumns(ctx context.Context, args json.RawMessage) (any, error) {
	var in contract.SearchColumnsInput
	if err := m.decode(args, &in); err != nil {
		return nil, &contract.PublicError{Code: contract.CodeInvalidRequest, Message: "invalid db_search_columns arguments"}
	}
	conn, err := m.lookup(ctx, in.Connection)
	if err != nil {
		return nil, err
	}
	key := m.metaKey(in.Connection, "search_columns", in.Pattern, derefStr(in.Schema), derefStr(in.Table), derefStr(in.Cursor), fmt.Sprint(derefInt(in.Limit)))
	return m.metaGet(ctx, key, m.metadataRefresh(in.Connection, func(ctx context.Context) (any, error) {
		return conn.SearchColumns(ctx, engine.SearchColumnsRequest{
			Pattern: in.Pattern,
			Schema:  in.Schema,
			Table:   in.Table,
			Page:    engine.PageRequest{Limit: derefInt(in.Limit), Cursor: in.Cursor},
		})
	}))
}

func (m *Manager) dbFindReferences(ctx context.Context, args json.RawMessage) (any, error) {
	var in contract.FindReferencesInput
	if err := m.decode(args, &in); err != nil {
		return nil, &contract.PublicError{Code: contract.CodeInvalidRequest, Message: "invalid db_find_references arguments"}
	}
	conn, err := m.lookup(ctx, in.Connection)
	if err != nil {
		return nil, err
	}
	return conn.FindReferences(ctx, engine.FindReferencesRequest{
		Value:    in.Value,
		Patterns: in.Patterns,
		Schema:   in.Schema,
	})
}

func (m *Manager) dbSampleRows(ctx context.Context, args json.RawMessage) (any, error) {
	var in contract.SampleRowsInput
	if err := m.decode(args, &in); err != nil {
		return nil, &contract.PublicError{Code: contract.CodeInvalidRequest, Message: "invalid db_sample_rows arguments"}
	}
	if in.MaxRows != nil && *in.MaxRows <= 0 {
		return nil, &contract.PublicError{Code: contract.CodeInvalidRequest, Message: "max_rows must be positive"}
	}
	conn, err := m.lookup(ctx, in.Connection)
	if err != nil {
		return nil, err
	}
	res, err := conn.SampleRows(ctx, engine.SampleRequest{
		TableRef: engine.TableRef{Schema: in.Schema, Table: in.Table},
		MaxRows:  derefInt(in.MaxRows),
	})
	if err != nil {
		return nil, wrapDatabaseError(err)
	}
	return map[string]any{
		"result":        res.Result,
		"deterministic": res.Deterministic,
	}, nil
}

func (m *Manager) dbCountRows(ctx context.Context, args json.RawMessage) (any, error) {
	var in contract.CountRowsInput
	if err := m.decode(args, &in); err != nil {
		return nil, &contract.PublicError{Code: contract.CodeInvalidRequest, Message: "invalid db_count_rows arguments"}
	}
	conn, err := m.lookup(ctx, in.Connection)
	if err != nil {
		return nil, err
	}
	estimate := in.Estimate != nil && *in.Estimate
	res, err := conn.CountRows(ctx, engine.TableRef{Schema: in.Schema, Table: in.Table}, estimate)
	if err != nil {
		return nil, wrapDatabaseError(err)
	}
	return map[string]any{"count": res.Count, "estimated": res.Estimated}, nil
}

func (m *Manager) dbQuery(ctx context.Context, args json.RawMessage) (any, error) {
	var in contract.QueryInput
	if err := m.decode(args, &in); err != nil {
		return nil, &contract.PublicError{Code: contract.CodeInvalidRequest, Message: "invalid db_query arguments"}
	}
	conn, err := m.lookup(ctx, in.Connection)
	if err != nil {
		return nil, err
	}
	req := engine.QueryRequest{
		SQL:        in.SQL,
		Parameters: in.Parameters,
	}
	if in.Limits != nil {
		req.Limits = &engine.ExecutionLimits{
			TimeoutMS:        in.Limits.TimeoutMS,
			MaxRows:          in.Limits.MaxRows,
			MaxResponseBytes: in.Limits.MaxResponseBytes,
		}
	}
	res, err := conn.Query(ctx, req)
	if err != nil {
		return nil, wrapDatabaseError(err)
	}
	return res, nil
}

func (m *Manager) dbExplain(ctx context.Context, args json.RawMessage) (any, error) {
	var in contract.ExplainInput
	if err := m.decode(args, &in); err != nil {
		return nil, &contract.PublicError{Code: contract.CodeInvalidRequest, Message: "invalid db_explain arguments"}
	}
	conn, err := m.lookup(ctx, in.Connection)
	if err != nil {
		return nil, err
	}
	req := engine.ExplainRequest{
		SQL:        in.SQL,
		Parameters: in.Parameters,
	}
	if in.Limits != nil {
		req.Limits = &engine.ExecutionLimits{
			TimeoutMS:        in.Limits.TimeoutMS,
			MaxResponseBytes: in.Limits.MaxResponseBytes,
		}
	}
	if in.Analyze != nil && *in.Analyze {
		req.Analyze = true
	}
	res, err := conn.Explain(ctx, req)
	if err != nil {
		return nil, wrapDatabaseError(err)
	}
	return map[string]any{"format": res.Format, "plan": res.Plan}, nil
}

func wrapDatabaseError(err error) error {
	if pe, ok := err.(*contract.PublicError); ok {
		return pe
	}
	return &contract.PublicError{Code: contract.CodeDatabaseError, Message: err.Error()}
}

func derefInt(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}

func derefStr(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
