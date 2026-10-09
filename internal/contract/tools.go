package contract

// This file declares the frozen input schemas for the MCP tools of
// v1. Field layout and tags are normative; they are serialized to golden
// files so the wire contract cannot drift from the Go types.

// ConnRef is a subset repeated as the identifier of a configured connection.
type ConnRef struct {
	Connection string `json:"connection" schemajson:"required,maxlength=64,minlength=1"`
}

// PageRequest carries optional pagination.
type PageRequest struct {
	Limit  *int    `json:"limit" schemajson:"minimum=1"`
	Cursor *string `json:"cursor" schemajson:"maxlength=1024,minlength=1"`
}

// TableRef identifies one schema-qualified table.
type TableRef struct {
	Connection string `json:"connection" schemajson:"required,maxlength=64,minlength=1"`
	Schema     string `json:"schema" schemajson:"required,maxlength=63,minlength=1"`
	Table      string `json:"table" schemajson:"required,maxlength=63,minlength=1"`
}

// Tool input argument structs, one per tool.

type ListConnectionsInput struct{}

type PingInput struct {
	Connection string `json:"connection" schemajson:"required,maxlength=64,minlength=1"`
	TimeoutMS  *int   `json:"timeout_ms" schemajson:"minimum=1"`
}

type ListSchemasInput struct {
	Connection string  `json:"connection" schemajson:"required,maxlength=64,minlength=1"`
	Limit      *int    `json:"limit" schemajson:"minimum=1"`
	Cursor     *string `json:"cursor" schemajson:"maxlength=1024,minlength=1"`
}

type ListTablesInput struct {
	Connection string  `json:"connection" schemajson:"required,maxlength=64,minlength=1"`
	Schema     *string `json:"schema" schemajson:"maxlength=63,minlength=1"`
	Limit      *int    `json:"limit" schemajson:"minimum=1"`
	Cursor     *string `json:"cursor" schemajson:"maxlength=1024,minlength=1"`
}

type DescribeTableInput struct {
	Connection string `json:"connection" schemajson:"required,maxlength=64,minlength=1"`
	Schema     string `json:"schema" schemajson:"required,maxlength=63,minlength=1"`
	Table      string `json:"table" schemajson:"required,maxlength=63,minlength=1"`
}

type ListIndexesInput struct {
	Connection string  `json:"connection" schemajson:"required,maxlength=64,minlength=1"`
	Schema     string  `json:"schema" schemajson:"required,maxlength=63,minlength=1"`
	Table      string  `json:"table" schemajson:"required,maxlength=63,minlength=1"`
	Limit      *int    `json:"limit" schemajson:"minimum=1"`
	Cursor     *string `json:"cursor" schemajson:"maxlength=1024,minlength=1"`
}

type ListRelationshipsInput struct {
	Connection string  `json:"connection" schemajson:"required,maxlength=64,minlength=1"`
	Schema     *string `json:"schema" schemajson:"maxlength=63,minlength=1"`
	Table      *string `json:"table" schemajson:"maxlength=63,minlength=1"`
	ToSchema   *string `json:"to_schema" schemajson:"maxlength=63,minlength=1"`
	ToTable    *string `json:"to_table" schemajson:"maxlength=63,minlength=1"`
	Column     *string `json:"column" schemajson:"maxlength=63,minlength=1"`
	Limit      *int    `json:"limit" schemajson:"minimum=1"`
	Cursor     *string `json:"cursor" schemajson:"maxlength=1024,minlength=1"`
}

type SearchColumnsInput struct {
	Connection string  `json:"connection" schemajson:"required,maxlength=64,minlength=1"`
	Pattern    string  `json:"pattern" schemajson:"required,maxlength=128,minlength=1"`
	Schema     *string `json:"schema" schemajson:"maxlength=63,minlength=1"`
	Table      *string `json:"table" schemajson:"maxlength=63,minlength=1"`
	Limit      *int    `json:"limit" schemajson:"minimum=1"`
	Cursor     *string `json:"cursor" schemajson:"maxlength=1024,minlength=1"`
}

type SuggestRelationshipsInput struct {
	Connection string  `json:"connection" schemajson:"required,maxlength=64,minlength=1"`
	Schema     *string `json:"schema" schemajson:"maxlength=63,minlength=1"`
	Table      *string `json:"table" schemajson:"maxlength=63,minlength=1"`
	Limit      *int    `json:"limit" schemajson:"minimum=1"`
	Cursor     *string `json:"cursor" schemajson:"maxlength=1024,minlength=1"`
}

type FindReferencesInput struct {
	Connection string   `json:"connection" schemajson:"required,maxlength=64,minlength=1"`
	Value      string   `json:"value" schemajson:"required,maxlength=128,minlength=1"`
	Patterns   []string `json:"patterns"`
	Schema     *string  `json:"schema" schemajson:"maxlength=63,minlength=1"`
}

type SampleRowsInput struct {
	Connection string `json:"connection" schemajson:"required,maxlength=64,minlength=1"`
	Schema     string `json:"schema" schemajson:"required,maxlength=63,minlength=1"`
	Table      string `json:"table" schemajson:"required,maxlength=63,minlength=1"`
	MaxRows    *int   `json:"max_rows" schemajson:"minimum=1"`
	TimeoutMS  *int   `json:"timeout_ms" schemajson:"minimum=1"`
}

type CountRowsInput struct {
	Connection string `json:"connection" schemajson:"required,maxlength=64,minlength=1"`
	Schema     string `json:"schema" schemajson:"required,maxlength=63,minlength=1"`
	Table      string `json:"table" schemajson:"required,maxlength=63,minlength=1"`
	TimeoutMS  *int   `json:"timeout_ms" schemajson:"minimum=1"`
	Estimate   *bool  `json:"estimate"`
}

// QueryLimitsInput lets a request only reduce effective limits.
type QueryLimitsInput struct {
	TimeoutMS        *int `json:"timeout_ms" schemajson:"minimum=1"`
	MaxRows          *int `json:"max_rows" schemajson:"minimum=1"`
	MaxResponseBytes *int `json:"max_response_bytes" schemajson:"minimum=1"`
}

// ParameterInput is one typed query parameter.
type ParameterInput struct {
	Type  string `json:"type" schemajson:"required"`
	Value any    `json:"value" schemajson:"required"`
}

type QueryInput struct {
	Connection string            `json:"connection" schemajson:"required,maxlength=64,minlength=1"`
	SQL        string            `json:"sql" schemajson:"required"`
	Parameters []ParameterInput  `json:"parameters"`
	Limits     *QueryLimitsInput `json:"limits"`
}

type ExplainInput struct {
	Connection string              `json:"connection" schemajson:"required,maxlength=64,minlength=1"`
	SQL        string              `json:"sql" schemajson:"required"`
	Parameters []ParameterInput    `json:"parameters"`
	Limits     *ExplainLimitsInput `json:"limits"`
	Analyze    *bool               `json:"analyze"`
}

type ExplainLimitsInput struct {
	TimeoutMS        *int `json:"timeout_ms" schemajson:"minimum=1"`
	MaxResponseBytes *int `json:"max_response_bytes" schemajson:"minimum=1"`
}

// Tool registers one MCP tool definition.
type Tool struct {
	Name        string
	Description string
	InputSchema *Schema
}
