package contract

// Tools returns the frozen list of v1 MCP tool definitions in registry order.
// The order and schemas are normative and covered by golden files.
func Tools() []Tool {
	return []Tool{
		{Name: "db_list_connections", Description: "List configured connections without exposing credentials.", InputSchema: toolInputSchema(ListConnectionsInput{})},
		{Name: "db_ping", Description: "Check a configured connection is reachable.", InputSchema: toolInputSchema(PingInput{})},
		{Name: "db_list_schemas", Description: "List authorized schemas.", InputSchema: toolInputSchema(ListSchemasInput{})},
		{Name: "db_list_tables", Description: "List authorized tables and views in a schema.", InputSchema: toolInputSchema(ListTablesInput{})},
		{Name: "db_describe_table", Description: "Describe columns, types, keys and constraints of a table.", InputSchema: toolInputSchema(DescribeTableInput{})},
		{Name: "db_list_indexes", Description: "List indexes of an authorized table.", InputSchema: toolInputSchema(ListIndexesInput{})},
		{Name: "db_list_relationships", Description: "List foreign key relationships visible by policy, with optional source, target, and column filters.", InputSchema: toolInputSchema(ListRelationshipsInput{})},
		{Name: "db_suggest_relationships", Description: "Infer logical join candidates between authorized tables that share a same-named, same-typed column but have no declared foreign key.", InputSchema: toolInputSchema(SuggestRelationshipsInput{})},
		{Name: "db_search_columns", Description: "Search authorized relation columns by name pattern.", InputSchema: toolInputSchema(SearchColumnsInput{})},
		{Name: "db_find_references", Description: "Find authorized columns whose values match a given value, returning a match count per column.", InputSchema: toolInputSchema(FindReferencesInput{})},
		{Name: "db_sample_rows", Description: "Return a small bounded sample of rows.", InputSchema: toolInputSchema(SampleRowsInput{})},
		{Name: "db_count_rows", Description: "Return an exact row count or timeout.", InputSchema: toolInputSchema(CountRowsInput{})},
		{Name: "db_query", Description: "Execute a single read-only parametrized query.", InputSchema: toolInputSchema(QueryInput{})},
		{Name: "db_explain", Description: "Return the allowed execution plan without ANALYZE.", InputSchema: toolInputSchema(ExplainInput{})},
	}
}
