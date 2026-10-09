package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/siia/siia-mcp/internal/contract"
	"github.com/siia/siia-mcp/internal/engine"
	"github.com/siia/siia-mcp/internal/postgres/guard"
)

// Explain builds EXPLAIN and emits a fully allowlisted plan model
// (SDD §12.12). Relations named by any plan node must be authorized or the
// whole result fails; a partially redacted plan is never returned.
// When req.Analyze is true, EXPLAIN ANALYZE is used to capture actual
// execution times (agent fluency improvement for performance testing).
func (a *adapter) Explain(ctx context.Context, req engine.ExplainRequest) (engine.ExplainResult, error) {
	p, err := a.prepare(ctx, req.SQL, req.Parameters)
	if err != nil {
		return engine.ExplainResult{}, err
	}
	// v1 rejects EXPLAIN when any AST relation is a view or materialized view.
	for _, r := range p.gres.Relations {
		if k := a.resolveKind(ctx, r.Schema, r.Name); k == "v" || k == "m" {
			return engine.ExplainResult{}, &contract.PublicError{
				Code:    contract.CodeQueryRejected,
				Message: "EXPLAIN over views is not allowed",
			}
		}
	}

	// The EXPLAIN wrapper is server-generated: the user SQL was already
	// validated by prepare() above (guard + policy + placeholders). It must
	// not be re-guarded, since EXPLAIN is a utility statement (SDD §9.2/§12.12).
	explainSQL := "EXPLAIN ("
	if req.Analyze {
		explainSQL += "ANALYZE TRUE, FORMAT JSON, COSTS TRUE, SETTINGS FALSE, BUFFERS TRUE, WAL FALSE, TIMING TRUE, SUMMARY TRUE"
	} else {
		explainSQL += "ANALYZE FALSE, FORMAT JSON, COSTS TRUE, SETTINGS FALSE, BUFFERS FALSE, WAL FALSE, TIMING FALSE, SUMMARY FALSE"
	}
	explainSQL += ") " + req.SQL
	_, _, err = a.effectiveLimits(req.Limits)
	if err != nil {
		return engine.ExplainResult{}, err
	}
	res, err := a.executeSelect(ctx, &preparedQuery{sql: explainSQL, args: p.args, gres: p.gres}, 1, a.limit.ResponseBytes)
	if err != nil {
		return engine.ExplainResult{}, err
	}
	if len(res.Rows) == 0 || len(res.Rows[0]) == 0 {
		return engine.ExplainResult{}, &contract.PublicError{Code: contract.CodeInternalError, Message: "EXPLAIN returned no plan"}
	}
	text, ok := res.Rows[0][0].(string)
	if !ok {
		return engine.ExplainResult{}, &contract.PublicError{Code: contract.CodeInternalError, Message: "EXPLAIN plan not textual"}
	}
	plan, err := a.allowlistPlan(ctx, text, req.Analyze)
	if err != nil {
		return engine.ExplainResult{}, err
	}
	return engine.ExplainResult{Format: "json", Plan: plan}, nil
}

func (a *adapter) allowlistPlan(ctx context.Context, raw string, analyze bool) (any, error) {
	var arr []map[string]any
	if err := json.Unmarshal([]byte(raw), &arr); err != nil {
		return nil, &contract.PublicError{Code: contract.CodeInternalError, Message: "invalid EXPLAIN output"}
	}
	if len(arr) == 0 {
		return nil, &contract.PublicError{Code: contract.CodeInternalError, Message: "empty EXPLAIN output"}
	}
	root, ok := arr[0]["Plan"].(map[string]any)
	if !ok {
		return nil, &contract.PublicError{Code: contract.CodeInternalError, Message: "EXPLAIN output missing Plan"}
	}
	return a.parsePlanNode(ctx, root, analyze)
}

func (a *adapter) parsePlanNode(ctx context.Context, node map[string]any, analyze bool) (map[string]any, error) {
	out := map[string]any{
		"node_type":    str(node["Node Type"]),
		"startup_cost": planCost(node["Startup Cost"]),
		"total_cost":   planCost(node["Total Cost"]),
		"plan_rows":    planRows(node["Plan Rows"]),
		"plan_width":   planWidth(node["Plan Width"]),
		"children":     []any{},
	}

	// When ANALYZE is enabled, include actual execution metrics.
	if analyze {
		if v, ok := node["Actual Rows"]; ok {
			out["actual_rows"] = planRows(v)
		}
		if v, ok := node["Actual Loops"]; ok {
			out["actual_loops"] = planWidth(v)
		}
		if v, ok := node["Actual Startup Time"]; ok {
			out["actual_startup_time"] = planCost(v)
		}
		if v, ok := node["Actual Total Time"]; ok {
			out["actual_total_time"] = planCost(v)
		}
		if v, ok := node["Shared Hit Blocks"]; ok {
			out["shared_hit_blocks"] = planWidth(v)
		}
		if v, ok := node["Shared Read Blocks"]; ok {
			out["shared_read_blocks"] = planWidth(v)
		}
		if v, ok := node["Shared Dirtied Blocks"]; ok {
			out["shared_dirtied_blocks"] = planWidth(v)
		}
		if v, ok := node["Shared Written Blocks"]; ok {
			out["shared_written_blocks"] = planWidth(v)
		}
	}

	// Relation mentioned by this node must be authorized (SDD §12.12).
	// PG plan JSON omits "Schema"; resolve it within allowlisted schemas only.
	name := str(node["Relation Name"])
	if name != "" {
		schema := str(node["Schema"])
		if schema == "" {
			var err error
			schema, err = a.resolveRelationSchema(ctx, name)
			if err != nil {
				return nil, err
			}
		}
		if err := a.authorizeRelation(ctx, guard.Relation{Schema: schema, Name: name}); err != nil {
			return nil, err
		}
		out["relation"] = map[string]any{"schema": schema, "name": name}
	}

	if children, ok := node["Plans"].([]any); ok {
		ch := make([]any, 0, len(children))
		for _, c := range children {
			cmap, ok := c.(map[string]any)
			if !ok {
				continue
			}
			sub, err := a.parsePlanNode(ctx, cmap, analyze)
			if err != nil {
				return nil, err
			}
			ch = append(ch, sub)
		}
		out["children"] = ch
	}
	return out, nil
}

// resolveRelationSchema resolves the schema of a relation by name so plan
// nodes without "Schema" can be re-validated without trusting search_path
// (SDD §9.3). In restricted mode, only allowlisted schemas are scanned; in
// unrestricted mode all non-system schemas are considered.
func (a *adapter) resolveRelationSchema(ctx context.Context, relname string) (string, error) {
	var schema string
	var err error
	if !a.unrestricted() {
		err = a.pool.QueryRow(ctx, `
SELECT n.nspname
FROM pg_catalog.pg_class c
JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
WHERE c.relname = $1 AND n.nspname = ANY($2::text[])
LIMIT 1`, relname, a.cfg.Allow.Schemas).Scan(&schema)
	} else {
		err = a.pool.QueryRow(ctx, `
SELECT n.nspname
FROM pg_catalog.pg_class c
JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
WHERE c.relname = $1
  AND n.nspname NOT LIKE 'pg\_%'
  AND n.nspname <> 'information_schema'
LIMIT 1`, relname).Scan(&schema)
	}
	if err != nil {
		return "", notAllowed(fmt.Sprintf("relation %q not found in authorized schemas", relname))
	}
	return schema, nil
}

func planCost(v any) string {
	if f, ok := v.(float64); ok {
		return strconv.FormatFloat(f, 'f', 2, 64)
	}
	return "0.00"
}

func planRows(v any) string {
	if f, ok := v.(float64); ok {
		return strconv.FormatFloat(f, 'f', 0, 64)
	}
	return "0"
}

func planWidth(v any) int {
	if f, ok := v.(float64); ok {
		return int(f)
	}
	return 0
}

func str(v any) string {
	s, _ := v.(string)
	return s
}
