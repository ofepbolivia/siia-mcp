// Package guard parses and walks the PostgreSQL AST to fail-closed against
// anything outside a single read-only SELECT (SDD §9).
package guard

import (
	"encoding/json"
	"fmt"
	"strings"

	pgquery "github.com/pganalyze/pg_query_go/v6"
	"github.com/siia/siia-mcp/internal/contract"
)

// Relation is a persistent relation reference collected from the AST.
type Relation struct {
	Schema string
	Name   string
}

// Result captures what the visitor found. Params is the highest placeholder
// number seen (0 when none); the caller validates contiguity.
type Result struct {
	Relations     []Relation
	Functions     []string
	CTEs          []string
	HasIntoTarget bool
	Params        int
}

// fullStmtKeys are statement-level node keys that must never appear anywhere
// inside a read-only SELECT.
var fullStmtKeys = map[string]bool{
	"RenameStmt": true, "AlterTableStmt": true, "AlterDomainStmt": true,
	"SetStmt": true, "ResetStmt": true, "CallStmt": true, "DoStmt": true,
	"CreateStmt": true, "CreateTableAsStmt": true, "CreateIndexStmt": true,
	"CreateSeqStmt": true, "CreateExtensionStmt": true, "AlterExtensionStmt": true,
	"CreateFunctionStmt": true, "AlterFunctionStmt": true, "CreateSchemaStmt": true,
	"CreateViewStmt": true, "CreateEnumStmt": true, "CreateRangeStmt": true,
	"CreateDomainStmt": true, "CreateCastStmt": true, "CreateOpClassStmt": true,
	"CreateOpFamilyStmt": true, "CommentStmt": true, "SecLabelStmt": true,
	"DropStmt": true, "TruncateStmt": true, "DropdbStmt": true, "CreatedbStmt": true,
	"AlterSystemStmt": true, "ClusterStmt": true, "VacuumStmt": true,
	"ExplainStmt": true, "CopyStmt": true, "PrepareStmt": true,
	"ExecuteStmt": true, "DeallocateStmt": true, "DiscardStmt": true,
	"TransactionStmt": true, "LockStmt": true, "GrantStmt": true,
	"RevokeStmt": true, "ReindexStmt": true, "CheckPointStmt": true,
	"ListenStmt": true, "NotifyStmt": true, "UnlistenStmt": true,
	"RefreshMatViewStmt": true, "ReassignOwnedStmt": true, "RoleStmt": true,
	"GrantRoleStmt": true, "RevokeRoleStmt": true, "CreateRoleStmt": true,
	"AlterRoleStmt": true, "DropRoleStmt": true, "CreatePolicyStmt": true,
	"AlterPolicyStmt": true, "CreatePublicationStmt": true,
	"AlterPublicationStmt": true, "CreateSubscriptionStmt": true,
	"AlterSubscriptionStmt": true, "PrepareTransactionStmt": true,
	"DeclareCursorStmt": true, "FetchStmt": true, "ClosePortalStmt": true,
	"CreateTransformStmt": true, "CreatePLangStmt": true, "AlterTblSpcStmt": true,
}

// mutatingStmtKeys appear as the value of a statement node (subquery/CTE body)
// and reject the whole query.
var mutatingStmtKeys = map[string]bool{
	"InsertStmt": true, "UpdateStmt": true, "DeleteStmt": true, "MergeStmt": true,
}

var lockingClauseKeys = map[string]bool{
	"PARAM_ForUpdate":      true,
	"PARAM_ForNoKeyUpdate": true,
	"PARAM_ForShare":       true,
	"PARAM_ForKeyShare":    true,
}

// Check parses and validates a single read-only SELECT. It returns the
// collected relations on success and a QUERY_REJECTED PublicError otherwise.
//
// The parse tree is obtained ONCE as JSON (pg_query.ParseToJSON). The former
// implementation additionally ran pg_query.Parse (protobuf) only to assert
// "exactly one statement" and "top-level SelectStmt"; both assertions are
// rederived from the JSON tree here (requireSingleSelect), so the redundant
// protobuf parse is eliminated. The denylist walk remains the same generic
// reflection walk, unchanged, as the detection oracle.
func Check(sql string) (*Result, error) {
	parseAny, err := pgquery.ParseToJSON(sql)
	if err != nil {
		return nil, reject("parse error")
	}

	var root map[string]any
	if err := json.Unmarshal([]byte(parseAny), &root); err != nil {
		return nil, reject("parse error")
	}
	if err := requireSingleSelect(root); err != nil {
		return nil, err
	}

	res := &Result{}
	if err := walkObject(root, res); err != nil {
		return nil, err
	}
	if res.HasIntoTarget {
		return nil, reject("SELECT INTO is not allowed")
	}
	filterCTERelations(res)
	return res, nil
}

// requireSingleSelect enforces the top-level whitelist directly on the JSON
// parse tree: exactly one statement, and that statement is a SELECT. This is
// the positive check that complements the denylist walk and replaces the
// protobuf-parsed assertions. A non-SELECT statement whose node key is not in
// the denylist maps (an unknown future construct) is rejected here.
func requireSingleSelect(root map[string]any) error {
	stmts, _ := root["stmts"].([]any)
	if len(stmts) != 1 {
		return reject("exactly one statement is required")
	}
	entry, _ := stmts[0].(map[string]any)
	if entry == nil {
		return reject("only SELECT statements are allowed")
	}
	stmt, _ := entry["stmt"].(map[string]any)
	if stmt == nil || len(stmt) != 1 || stmt["SelectStmt"] == nil {
		return reject("only SELECT statements are allowed")
	}
	return nil
}

// filterCTERelations removes relations that are actually CTE references. A CTE
// is referenced by name without a schema (e.g. `SELECT * FROM cte`); such a
// reference is not a persistent relation and must not require schema
// qualification. Only schema-less references that match a collected CTE name
// are dropped, so schema-qualified relations and genuinely unqualified base
// tables keep their current behavior. The CTE body's own relations remain in
// the set and are still authorized.
func filterCTERelations(res *Result) {
	if len(res.CTEs) == 0 {
		return
	}
	cteSet := make(map[string]struct{}, len(res.CTEs))
	for _, c := range res.CTEs {
		cteSet[c] = struct{}{}
	}
	filtered := res.Relations[:0]
	for _, r := range res.Relations {
		if r.Schema == "" {
			if _, isCTE := cteSet[r.Name]; isCTE {
				continue
			}
		}
		filtered = append(filtered, r)
	}
	res.Relations = filtered
}

func walkObject(obj map[string]any, res *Result) error {
	for key, value := range obj {
		switch {
		case fullStmtKeys[key] || mutatingStmtKeys[key]:
			return reject(fmt.Sprintf("construct %q is not allowed", key))
		case lockingClauseKeys[key]:
			return reject("locking clauses are not allowed")
		}
		switch key {
		case "intoClause":
			if value != nil {
				res.HasIntoTarget = true
			}
			continue
		case "RangeVar":
			if rv, ok := value.(map[string]any); ok {
				if relname := str(rv["relname"]); relname != "" {
					if persistence := str(rv["relpersistence"]); persistence == "" || persistence[0] != 't' {
						res.Relations = append(res.Relations, Relation{
							Schema: str(rv["schemaname"]),
							Name:   relname,
						})
					}
				}
			}
		case "FuncCall":
			if fc, ok := value.(map[string]any); ok {
				res.Functions = append(res.Functions, functionName(fc))
			}
		case "ParameterRef", "ParamRef":
			if pr, ok := value.(map[string]any); ok {
				if n, ok := pr["number"].(float64); ok {
					v := int(n)
					if v > res.Params {
						res.Params = v
					}
				}
			}
		case "LockingClause":
			if lc, ok := value.(map[string]any); ok {
				if s := str(lc["strength"]); s != "" {
					return reject("locking clauses are not allowed")
				}
			}
		case "CommonTableExpr":
			if cte, ok := value.(map[string]any); ok {
				if n := str(cte["ctename"]); n != "" {
					res.CTEs = append(res.CTEs, n)
				}
			}
		}
		if err := walkValue(value, res); err != nil {
			return err
		}
	}
	return nil
}

func walkValue(value any, res *Result) error {
	switch v := value.(type) {
	case map[string]any:
		return walkObject(v, res)
	case []any:
		for _, item := range v {
			if err := walkValue(item, res); err != nil {
				return err
			}
		}
	}
	return nil
}

func functionName(fc map[string]any) string {
	var parts []string
	if fn, ok := fc["funcname"].([]any); ok {
		for _, p := range fn {
			if m, ok := p.(map[string]any); ok {
				if strNode, ok := m["String"].(map[string]any); ok {
					if s := str(strNode["sval"]); s != "" {
						parts = append(parts, s)
					}
				}
			}
		}
	}
	return strings.Join(parts, ".")
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func reject(msg string) error {
	return &contract.PublicError{Code: contract.CodeQueryRejected, Message: msg}
}
