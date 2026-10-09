// Package engine defines the connection-bound contract between the MCP
// layer and database engines. It is kept small: public responsibilities only,
// no repository/service layers (SDD §10, §3.3).
package engine

import (
	"context"

	"github.com/siia/siia-mcp/internal/contract"
)

// PageRequest is a pagination request.
type PageRequest struct {
	Limit  int
	Cursor *string
}

// TableRef identifies one schema-qualified table.
type TableRef struct {
	Schema string
	Table  string
}

// ListTablesRequest filters by optional schema.
type ListTablesRequest struct {
	Schema string
	Page   PageRequest
}

// RelationshipRequest optionally filters by schema and/or table.
type RelationshipRequest struct {
	Schema   *string
	Table    *string
	ToSchema *string
	ToTable  *string
	Column   *string
	Page     PageRequest
}

// SearchColumnsRequest filters authorized columns by name pattern.
type SearchColumnsRequest struct {
	Pattern string
	Schema  *string
	Table   *string
	Page    PageRequest
}

// SuggestRelationshipsRequest optionally filters inferred logical join
// candidates by source schema/table.
type SuggestRelationshipsRequest struct {
	Schema *string
	Table  *string
	Page   PageRequest
}

// FindReferencesRequest asks where a value appears across authorized columns
// whose names match the requested patterns.
type FindReferencesRequest struct {
	Value    string
	Patterns []string
	Schema   *string
}

// SampleRequest asks for a bounded sample of a table.
type SampleRequest struct {
	TableRef
	MaxRows int
}

// ExecutionLimits lets a request reduce (never raise) effective limits.
type ExecutionLimits struct {
	TimeoutMS        *int
	MaxRows          *int
	MaxResponseBytes *int
}

// QueryRequest is one read-only parametrized query.
type QueryRequest struct {
	TableRef
	SQL        string
	Parameters []contract.ParameterInput
	Limits     *ExecutionLimits
}

// ExplainRequest is one read-only EXPLAIN, optionally with ANALYZE.
type ExplainRequest struct {
	TableRef
	SQL        string
	Parameters []contract.ParameterInput
	Limits     *ExecutionLimits
	Analyze    bool
}

// PingResult reports reachability and server major version.
type PingResult struct {
	OK          bool
	ServerMajor int
}

// CountResult is an exact or estimated decimal string count.
type CountResult struct {
	Count     string
	Estimated bool
}

// ExplainResult is the allowlisted execution plan.
type ExplainResult struct {
	Format string
	Plan   any
}

// Connection is the engine-neutral contract implemented by adapters.
type Connection interface {
	Ping(context.Context) (PingResult, error)
	ListSchemas(context.Context, PageRequest) (contract.Page[contract.SchemaItem], error)
	ListTables(context.Context, ListTablesRequest) (contract.Page[contract.TableItem], error)
	DescribeTable(context.Context, TableRef) (contract.TableDescription, error)
	ListIndexes(context.Context, TableRef, PageRequest) (contract.Page[contract.IndexItem], error)
	ListRelationships(context.Context, RelationshipRequest) (contract.Page[contract.RelationshipItem], error)
	SuggestRelationships(context.Context, SuggestRelationshipsRequest) (contract.Page[contract.SuggestedRelationship], error)
	SearchColumns(context.Context, SearchColumnsRequest) (contract.Page[contract.ColumnSearchItem], error)
	FindReferences(context.Context, FindReferencesRequest) ([]contract.ReferenceMatch, error)
	SampleRows(context.Context, SampleRequest) (SampleResult, error)
	CountRows(context.Context, TableRef, bool) (CountResult, error)
	Query(context.Context, QueryRequest) (contract.QueryResult, error)
	Explain(context.Context, ExplainRequest) (ExplainResult, error)
	Close(context.Context) error
}

// SampleResult bundles a sample with its determinism flag.
type SampleResult struct {
	Result        contract.QueryResult
	Deterministic bool
}
