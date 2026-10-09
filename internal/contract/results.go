package contract

// SchemaVersion is the version reported in every result envelope.
const SchemaVersion = "1"

// SuccessMeta carries timing and truncation details for every successful tool
// result.
type SuccessMeta struct {
	DurationMS       int64   `json:"duration_ms"`
	Truncated        bool    `json:"truncated"`
	TruncationReason *string `json:"truncation_reason"`
}

// SuccessEnvelope wraps the data of a successful tool result with the common
// versioned envelope described in SDD §11.3.
type SuccessEnvelope struct {
	SchemaVersion string      `json:"schema_version"`
	Data          any         `json:"data"`
	Meta          SuccessMeta `json:"meta"`
}

// ErrorEnvelope is defined in errors.go; reference kept here for symmetry with
// the success envelope.

// QueryResult is the lossless result of a data query. Rows are positional
// values whose interpretation is driven by the column metadata.
type QueryResult struct {
	Columns  []Column `json:"columns"`
	Rows     [][]any  `json:"rows"`
	RowCount int      `json:"row_count"`
	// Truncated and TruncationReason are populated only when a bounded result
	// was cut short by row or payload limits.
	Truncated        bool    `json:"truncated,omitempty"`
	TruncationReason *string `json:"truncation_reason,omitempty"`
}

// Column describes a result column.
type Column struct {
	Name         string `json:"name"`
	PostgresType string `json:"postgres_type"`
	TypeOID      int    `json:"type_oid"`
	Format       string `json:"format"`
	Sensitive    bool   `json:"sensitive"`
}

// Page is the paginated envelope for metadata lists.
type Page[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

// ConnectionItem describes one configured logical connection without exposing
// any network or credential details.
type ConnectionItem struct {
	Name     string `json:"name"`
	State    string `json:"state"`
	Required bool   `json:"required"`
}

// PingResult is the result of db_ping.
type PingResult struct {
	OK          bool `json:"ok"`
	ServerMajor int  `json:"server_major"`
}

// SchemaItem is a single authorized schema.
type SchemaItem struct {
	Name string `json:"name"`
}

// TableItem is a single authorized table or view.
type TableItem struct {
	Schema          string `json:"schema"`
	Name            string `json:"name"`
	Kind            string `json:"kind"`
	SecurityInvoker *bool  `json:"security_invoker"`
	SecurityBarrier *bool  `json:"security_barrier"`
}

// ColumnInfo describes one column of a table description.
type ColumnInfo struct {
	Name         string `json:"name"`
	Ordinal      int    `json:"ordinal"`
	PostgresType string `json:"postgres_type"`
	Nullable     bool   `json:"nullable"`
	HasDefault   bool   `json:"has_default"`
	Generated    bool   `json:"generated"`
	Identity     bool   `json:"identity"`
	Sensitive    bool   `json:"sensitive"`
}

// ColumnSearchItem is one authorized column matched by db_search_columns.
type ColumnSearchItem struct {
	Schema       string `json:"schema"`
	Table        string `json:"table"`
	Kind         string `json:"kind"`
	Column       string `json:"column"`
	Ordinal      int    `json:"ordinal"`
	PostgresType string `json:"postgres_type"`
	Nullable     bool   `json:"nullable"`
	Sensitive    bool   `json:"sensitive"`
}

// ReferenceMatch is one authorized column where db_find_references found a
// non-zero number of matches for the requested value.
type ReferenceMatch struct {
	Schema  string `json:"schema"`
	Table   string `json:"table"`
	Column  string `json:"column"`
	Matches string `json:"matches"`
}

// KeyConstraint is a primary key or unique constraint.
type KeyConstraint struct {
	Name              string   `json:"name"`
	Columns           []string `json:"columns"`
	Deferrable        bool     `json:"deferrable"`
	InitiallyDeferred bool     `json:"initially_deferred"`
}

// CheckConstraint is a named check or exclusion constraint.
type CheckConstraint struct {
	Name      string `json:"name"`
	Validated bool   `json:"validated"`
}

// TableDescription is the full description of one table or view.
type TableDescription struct {
	Schema                string             `json:"schema"`
	Name                  string             `json:"name"`
	Kind                  string             `json:"kind"`
	Columns               []ColumnInfo       `json:"columns"`
	PrimaryKey            *KeyConstraint     `json:"primary_key"`
	UniqueConstraints     []KeyConstraint    `json:"unique_constraints"`
	CheckConstraints      []CheckConstraint  `json:"check_constraints"`
	ExclusionConstraints  []CheckConstraint  `json:"exclusion_constraints"`
	ForeignKeyConstraints []RelationshipItem `json:"foreign_keys"`
}

// IndexItem is a single index.
type IndexItem struct {
	Name           string     `json:"name"`
	Unique         bool       `json:"unique"`
	Primary        bool       `json:"primary"`
	Valid          bool       `json:"valid"`
	Method         string     `json:"method"`
	Keys           []IndexKey `json:"keys"`
	IncludeColumns []string   `json:"include_columns"`
	Partial        bool       `json:"partial"`
}

// IndexKey is one key column or expression of an index.
type IndexKey struct {
	Column     *string `json:"column"`
	Expression bool    `json:"expression"`
}

// ObjectRef is a schema-qualified relation reference.
type ObjectRef struct {
	Schema string `json:"schema"`
	Name   string `json:"name"`
}

// SuggestedRelationship is one inferred logical join candidate: two authorized
// relations that share a column of the same name and type but have no declared
// foreign key between them (agent fluency improvement for schema discovery).
type SuggestedRelationship struct {
	Column       string    `json:"column"`
	Source       ObjectRef `json:"source"`
	Target       ObjectRef `json:"target"`
	SourceColumn string    `json:"source_column"`
	TargetColumn string    `json:"target_column"`
	PostgresType string    `json:"postgres_type"`
}

// RelationshipItem is a single foreign key relationship.
type RelationshipItem struct {
	Name              string    `json:"name"`
	From              ObjectRef `json:"from"`
	FromColumns       []string  `json:"from_columns"`
	To                ObjectRef `json:"to"`
	ToColumns         []string  `json:"to_columns"`
	OnUpdate          string    `json:"on_update"`
	OnDelete          string    `json:"on_delete"`
	Deferrable        bool      `json:"deferrable"`
	InitiallyDeferred bool      `json:"initially_deferred"`
}
