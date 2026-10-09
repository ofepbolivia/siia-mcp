//go:build integration

package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/siia/siia-mcp/internal/config"
	"github.com/siia/siia-mcp/internal/contract"
	"github.com/siia/siia-mcp/internal/engine"
)

func testITConfig() config.Connection {
	return config.Connection{
		Name:       "it",
		Engine:     "postgres",
		Mode:       "readonly",
		Initialize: "eager",
		Host:       "127.0.0.1",
		Port:       55432,
		Database:   "itdb",
		User:       "ituser",
		Password:   "itpass",
		TLS:        config.TLSConfig{Mode: "disable"},
		Pool: config.PoolConfig{
			MaxConnections:        4,
			MaxConnectionLifetime: 30 * time.Minute,
			MaxConnectionIdleTime: 5 * time.Minute,
			HealthCheckPeriod:     30 * time.Second,
		},
		Allow: &config.Allowlist{Schemas: []string{"public"}},
	}
}

func newTestConn(t *testing.T) engine.Connection {
	t.Helper()
	return newTestConnAllow(t, &config.Allowlist{Schemas: []string{"public"}})
}

// newTestConnAllow builds a test connection with the given allowlist. A nil
// allowlist yields an unrestricted connection (SDD §8.3).
func newTestConnAllow(t *testing.T, allow *config.Allowlist) engine.Connection {
	t.Helper()
	cfg := testITConfig()
	cfg.Allow = allow
	if h := os.Getenv("SIIASQL_IT_HOST"); h != "" {
		cfg.Host = h
	}
	if p := os.Getenv("SIIASQL_IT_PORT"); p != "" {
		var port int
		if _, err := fmt.Sscanf(p, "%d", &port); err != nil {
			t.Fatal(err)
		}
		cfg.Port = port
	}
	if pw := os.Getenv("SIIASQL_IT_PASSWORD"); pw != "" {
		cfg.Password = pw
	}
	if u := os.Getenv("SIIASQL_IT_USER"); u != "" {
		cfg.User = u
	}
	if d := os.Getenv("SIIASQL_IT_DB"); d != "" {
		cfg.Database = d
	}
	global := config.LimitsConfig{
		QueryBytes:           65536,
		ParameterCount:       64,
		ParameterBytes:       262144,
		ParametersTotalBytes: 1048576,
	}
	conn, err := New(context.Background(), cfg, global, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn
}

func TestITPing(t *testing.T) {
	conn := newTestConn(t)
	res, err := conn.Ping(context.Background())
	if err != nil {
		t.Fatalf("ping: %v", err)
	}
	if !res.OK {
		t.Fatal("ping not ok")
	}
	if res.ServerMajor < 10 {
		t.Fatalf("server major = %d, want >= 10", res.ServerMajor)
	}
}

func TestITListSchemasAllowlist(t *testing.T) {
	conn := newTestConn(t)
	page, err := conn.ListSchemas(context.Background(), engine.PageRequest{})
	if err != nil {
		t.Fatalf("ListSchemas: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].Name != "public" {
		t.Fatalf("schemas = %+v, want [public]", page.Items)
	}
}

func TestITListTables(t *testing.T) {
	conn := newTestConn(t)
	page, err := conn.ListTables(context.Background(), engine.ListTablesRequest{Page: engine.PageRequest{}})
	if err != nil {
		t.Fatalf("ListTables: %v", err)
	}
	names := map[string]bool{}
	for _, it := range page.Items {
		names[it.Name] = true
	}
	for _, want := range []string{"authors", "books", "hidden_links"} {
		if !names[want] {
			t.Fatalf("missing %q in %v", want, names)
		}
	}
	if names["author_books"] {
		t.Fatalf("unauthorized view author_books exposed in %v", names)
	}
}

func TestITMetadataPagination(t *testing.T) {
	conn := newTestConn(t)
	var cursor *string
	seen := map[string]bool{}
	for {
		page, err := conn.ListTables(context.Background(), engine.ListTablesRequest{
			Page: engine.PageRequest{Limit: 1, Cursor: cursor},
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) != 1 {
			t.Fatalf("page items = %d, want 1", len(page.Items))
		}
		key := page.Items[0].Schema + "." + page.Items[0].Name
		if seen[key] {
			t.Fatalf("duplicate paginated item %q", key)
		}
		seen[key] = true
		cursor = page.NextCursor
		if cursor == nil {
			break
		}
	}
	for _, want := range []string{"public.authors", "public.books", "public.hidden_links"} {
		if !seen[want] {
			t.Fatalf("pagination missing %q: %v", want, seen)
		}
	}
}

func TestITExplicitViewDoesNotExposeSchemaTables(t *testing.T) {
	cfg := testITConfig()
	cfg.Allow = &config.Allowlist{Views: []config.ObjectRef{{Schema: "hidden", Name: "safe_authors"}}}
	conn, err := New(context.Background(), cfg, config.LimitsConfig{MetadataPageSize: 100}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	page, err := conn.ListTables(context.Background(), engine.ListTablesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Schema != "hidden" || page.Items[0].Name != "safe_authors" || page.Items[0].Kind != "view" {
		t.Fatalf("visible relations = %+v, want only hidden.safe_authors", page.Items)
	}
}

func TestITDescribeTable(t *testing.T) {
	conn := newTestConn(t)
	desc, err := conn.DescribeTable(context.Background(), engine.TableRef{Schema: "public", Table: "books"})
	if err != nil {
		t.Fatalf("DescribeTable: %v", err)
	}
	if desc.Kind != "table" {
		t.Fatalf("kind = %q", desc.Kind)
	}
	if desc.PrimaryKey == nil || desc.PrimaryKey.Name != "books_pkey" {
		t.Fatalf("primary key = %+v", desc.PrimaryKey)
	}
	if len(desc.Columns) != 3 {
		t.Fatalf("columns = %d, want 3", len(desc.Columns))
	}
	if len(desc.ForeignKeyConstraints) != 1 {
		t.Fatalf("fk count = %d, want 1", len(desc.ForeignKeyConstraints))
	}
	if desc.ForeignKeyConstraints[0].OnDelete != "CASCADE" {
		t.Fatalf("on delete = %q", desc.ForeignKeyConstraints[0].OnDelete)
	}
}

func TestITMetadataRejectsUnauthorizedObjects(t *testing.T) {
	conn := newTestConn(t)
	tests := []struct {
		name string
		call func() error
	}{
		{name: "describe", call: func() error {
			_, err := conn.DescribeTable(context.Background(), engine.TableRef{Schema: "hidden", Table: "secrets"})
			return err
		}},
		{name: "indexes", call: func() error {
			_, err := conn.ListIndexes(context.Background(), engine.TableRef{Schema: "hidden", Table: "secrets"}, engine.PageRequest{})
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var pe *contract.PublicError
			if err := tt.call(); !errors.As(err, &pe) || pe.Code != contract.CodeObjectNotAllowed {
				t.Fatalf("error = %#v, want OBJECT_NOT_ALLOWED", err)
			}
		})
	}
}

func TestITMetadataDoesNotRevealUnauthorizedRelationships(t *testing.T) {
	conn := newTestConn(t)
	page, err := conn.ListRelationships(context.Background(), engine.RelationshipRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for _, relationship := range page.Items {
		if relationship.From.Schema == "hidden" || relationship.To.Schema == "hidden" {
			t.Fatalf("unauthorized relationship leaked: %+v", relationship)
		}
	}

	desc, err := conn.DescribeTable(context.Background(), engine.TableRef{Schema: "public", Table: "hidden_links"})
	if err != nil {
		t.Fatal(err)
	}
	if len(desc.ForeignKeyConstraints) != 0 {
		t.Fatalf("unauthorized foreign keys leaked: %+v", desc.ForeignKeyConstraints)
	}
}

func TestITSearchColumnsFindsOnlyAuthorizedColumns(t *testing.T) {
	conn := newTestConn(t)
	page, err := conn.SearchColumns(context.Background(), engine.SearchColumnsRequest{Pattern: "author", Page: engine.PageRequest{Limit: 10}})
	if err != nil {
		t.Fatalf("SearchColumns: %v", err)
	}
	foundBookAuthor := false
	for _, item := range page.Items {
		if item.Schema == "hidden" {
			t.Fatalf("unauthorized column leaked: %+v", item)
		}
		if item.Schema == "public" && item.Table == "books" && item.Column == "author_id" {
			foundBookAuthor = true
		}
	}
	if !foundBookAuthor {
		t.Fatalf("public.books.author_id not found in %+v", page.Items)
	}
}

func TestITSearchColumnsByTable(t *testing.T) {
	conn := newTestConn(t)
	schema := "public"
	table := "books"
	page, err := conn.SearchColumns(context.Background(), engine.SearchColumnsRequest{
		Pattern: "id",
		Schema:  &schema,
		Table:   &table,
		Page:    engine.PageRequest{Limit: 20},
	})
	if err != nil {
		t.Fatalf("SearchColumns by table: %v", err)
	}
	for _, item := range page.Items {
		if item.Table != "books" {
			t.Fatalf("column from other table leaked: %+v", item)
		}
	}
	foundAuthorID := false
	for _, item := range page.Items {
		if item.Column == "author_id" {
			foundAuthorID = true
		}
	}
	if !foundAuthorID {
		t.Fatalf("author_id not found when filtering by table: %+v", page.Items)
	}
}

func TestITSearchColumnsRequiresSchemaWhenTable(t *testing.T) {
	conn := newTestConn(t)
	table := "books"
	_, err := conn.SearchColumns(context.Background(), engine.SearchColumnsRequest{
		Pattern: "id",
		Table:   &table,
	})
	if err == nil {
		t.Fatal("expected error when table provided without schema")
	}
}

func TestITFindReferences(t *testing.T) {
	conn := newTestConn(t)
	refs, err := conn.FindReferences(context.Background(), engine.FindReferencesRequest{
		Value:    "1",
		Patterns: []string{"%author_id%"},
	})
	if err != nil {
		t.Fatalf("FindReferences: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("references = %+v, want exactly public.books.author_id", refs)
	}
	got := refs[0]
	if got.Schema != "public" || got.Table != "books" || got.Column != "author_id" || got.Matches != "1" {
		t.Fatalf("reference = %+v, want public.books.author_id matches 1", got)
	}
}

func TestITFindReferencesNoMatches(t *testing.T) {
	conn := newTestConn(t)
	refs, err := conn.FindReferences(context.Background(), engine.FindReferencesRequest{
		Value:    "99999",
		Patterns: []string{"%author_id%"},
	})
	if err != nil {
		t.Fatalf("FindReferences: %v", err)
	}
	if len(refs) != 0 {
		t.Fatalf("references = %+v, want none", refs)
	}
}

func TestITFindReferencesRequiresValue(t *testing.T) {
	conn := newTestConn(t)
	if _, err := conn.FindReferences(context.Background(), engine.FindReferencesRequest{Value: "   "}); err == nil {
		t.Fatal("expected error for empty value")
	}
}

func TestITListRelationshipsFiltersByTargetAndColumn(t *testing.T) {
	conn := newTestConn(t)
	toSchema, toTable, column := "public", "authors", "author_id"
	page, err := conn.ListRelationships(context.Background(), engine.RelationshipRequest{
		ToSchema: &toSchema,
		ToTable:  &toTable,
		Column:   &column,
		Page:     engine.PageRequest{Limit: 10},
	})
	if err != nil {
		t.Fatalf("ListRelationships: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("relationships = %+v, want one public.books -> public.authors", page.Items)
	}
	got := page.Items[0]
	if got.From.Schema != "public" || got.From.Name != "books" || got.To.Schema != "public" || got.To.Name != "authors" {
		t.Fatalf("relationship = %+v, want public.books -> public.authors", got)
	}
	if len(got.FromColumns) != 1 || got.FromColumns[0] != "author_id" || len(got.ToColumns) != 1 || got.ToColumns[0] != "id" {
		t.Fatalf("relationship columns = from %v to %v", got.FromColumns, got.ToColumns)
	}
}

func TestITQueryRejectsOversizedValue(t *testing.T) {
	cfg := testITConfig()
	global := config.LimitsConfig{
		QueryBytes:           65536,
		ParameterCount:       64,
		ParameterBytes:       262144,
		ParametersTotalBytes: 1048576,
		ValueBytes:           4,
	}
	conn, err := New(context.Background(), cfg, global, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	_, err = conn.Query(context.Background(), engine.QueryRequest{SQL: "SELECT 'abcdef'::text"})
	var pe *contract.PublicError
	if !errors.As(err, &pe) || pe.Code != contract.CodeResultValueTooLarge {
		t.Fatalf("error = %#v, want RESULT_VALUE_TOO_LARGE", err)
	}
}

func TestITListIndexes(t *testing.T) {
	conn := newTestConn(t)
	page, err := conn.ListIndexes(context.Background(), engine.TableRef{Schema: "public", Table: "books"}, engine.PageRequest{})
	if err != nil {
		t.Fatalf("ListIndexes: %v", err)
	}
	found := false
	for _, it := range page.Items {
		if it.Name == "idx_books_author" {
			found = true
		}
	}
	if !found {
		t.Fatalf("idx_books_author not found: %+v", page.Items)
	}
}

func TestITQuerySelect(t *testing.T) {
	conn := newTestConn(t)
	res, err := conn.Query(context.Background(), engine.QueryRequest{
		SQL:        "SELECT id, title FROM public.books WHERE id = $1",
		Parameters: []contract.ParameterInput{{Type: "int4", Value: "1"}},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if res.RowCount != 1 {
		t.Fatalf("row_count = %d, want 1", res.RowCount)
	}
	if len(res.Columns) != 2 {
		t.Fatalf("columns = %d, want 2", len(res.Columns))
	}
	title, ok := res.Rows[0][1].(string)
	if !ok || title != "Notes G" {
		t.Fatalf("title = %v", res.Rows[0][1])
	}
}

func TestITQueryRedactsSensitiveColumns(t *testing.T) {
	conn := newTestConn(t)
	res, err := conn.Query(context.Background(), engine.QueryRequest{SQL: "SELECT 'secret'::text AS password"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(res.Columns) != 1 || !res.Columns[0].Sensitive {
		t.Fatalf("columns = %+v, want sensitive password column", res.Columns)
	}
	if got := res.Rows[0][0]; got != "[REDACTED]" {
		t.Fatalf("value = %#v, want redacted", got)
	}
}

func TestITQueryNumericLossless(t *testing.T) {
	conn := newTestConn(t)
	res, err := conn.Query(context.Background(), engine.QueryRequest{
		SQL: "SELECT 12345678901234567890::numeric AS big",
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	v, ok := res.Rows[0][0].(string)
	if !ok {
		t.Fatalf("numeric type = %T, want string", res.Rows[0][0])
	}
	if v != "12345678901234567890" {
		t.Fatalf("numeric value = %q", v)
	}
	if res.Columns[0].Format != "decimal" {
		t.Fatalf("format = %q", res.Columns[0].Format)
	}
}

func TestITQueryRejectsWrite(t *testing.T) {
	conn := newTestConn(t)
	_, err := conn.Query(context.Background(), engine.QueryRequest{
		SQL: "UPDATE public.books SET title = 'x' WHERE id = 1",
	})
	if err == nil {
		t.Fatal("expected QUERY_REJECTED for UPDATE")
	}
}

func TestITQueryRejectsManyStatements(t *testing.T) {
	conn := newTestConn(t)
	_, err := conn.Query(context.Background(), engine.QueryRequest{
		SQL: "SELECT 1; SELECT 2",
	})
	if err == nil {
		t.Fatal("expected rejection for multi-statement")
	}
}

func TestITQueryAllowsSystemCatalog(t *testing.T) {
	conn := newTestConn(t)
	res, err := conn.Query(context.Background(), engine.QueryRequest{
		SQL:    "SELECT table_name FROM information_schema.tables LIMIT 1",
		Limits: &engine.ExecutionLimits{MaxRows: intPtr(1)},
	})
	if err != nil {
		t.Fatalf("Query: expected catalog access to be allowed, got %v", err)
	}
	if res.RowCount != 1 {
		t.Fatalf("row_count = %d, want 1", res.RowCount)
	}
}

func TestITQueryAllowsPgCatalogRelation(t *testing.T) {
	conn := newTestConn(t)
	res, err := conn.Query(context.Background(), engine.QueryRequest{
		SQL:    "SELECT relname FROM pg_catalog.pg_class LIMIT 1",
		Limits: &engine.ExecutionLimits{MaxRows: intPtr(1)},
	})
	if err != nil {
		t.Fatalf("Query: expected pg_catalog access to be allowed, got %v", err)
	}
	if res.RowCount != 1 {
		t.Fatalf("row_count = %d, want 1", res.RowCount)
	}
}

func TestITUnrestrictedListsAllSchemas(t *testing.T) {
	conn := newTestConnAllow(t, nil)
	page, err := conn.ListSchemas(context.Background(), engine.PageRequest{})
	if err != nil {
		t.Fatalf("ListSchemas: %v", err)
	}
	names := map[string]bool{}
	for _, it := range page.Items {
		names[it.Name] = true
	}
	if !names["public"] || !names["hidden"] {
		t.Fatalf("unrestricted schemas = %v, want public and hidden", names)
	}
}

func TestITUnrestrictedListsAllTables(t *testing.T) {
	conn := newTestConnAllow(t, nil)
	page, err := conn.ListTables(context.Background(), engine.ListTablesRequest{})
	if err != nil {
		t.Fatalf("ListTables: %v", err)
	}
	names := map[string]bool{}
	for _, it := range page.Items {
		names[it.Schema+"."+it.Name] = true
	}
	for _, want := range []string{
		"public.authors",
		"public.books",
		"public.hidden_links",
		"public.author_books",
		"hidden.secrets",
		"hidden.private_links",
		"hidden.safe_authors",
	} {
		if !names[want] {
			t.Fatalf("unrestricted tables missing %q in %v", want, names)
		}
	}
}

func TestITUnrestrictedAllowsHiddenMetadata(t *testing.T) {
	conn := newTestConnAllow(t, nil)
	desc, err := conn.DescribeTable(context.Background(), engine.TableRef{Schema: "hidden", Table: "secrets"})
	if err != nil {
		t.Fatalf("DescribeTable(hidden.secrets): %v", err)
	}
	if desc.Kind != "table" {
		t.Fatalf("kind = %q", desc.Kind)
	}
	idx, err := conn.ListIndexes(context.Background(), engine.TableRef{Schema: "hidden", Table: "secrets"}, engine.PageRequest{})
	if err != nil {
		t.Fatalf("ListIndexes(hidden.secrets): %v", err)
	}
	_ = idx
}

func TestITUnrestrictedQueryAllowsHidden(t *testing.T) {
	conn := newTestConnAllow(t, nil)
	res, err := conn.Query(context.Background(), engine.QueryRequest{
		SQL:    "SELECT id FROM hidden.secrets",
		Limits: &engine.ExecutionLimits{MaxRows: intPtr(10)},
	})
	if err != nil {
		t.Fatalf("Query hidden.secrets: %v", err)
	}
	if res.Truncated {
		t.Fatal("unexpected truncation for empty hidden.secrets")
	}
}

func TestITUnrestrictedExplainAllowsHidden(t *testing.T) {
	conn := newTestConnAllow(t, nil)
	res, err := conn.Explain(context.Background(), engine.ExplainRequest{
		SQL: "SELECT id FROM hidden.secrets",
	})
	if err != nil {
		t.Fatalf("Explain hidden.secrets: %v", err)
	}
	if res.Format != "json" {
		t.Fatalf("format = %q", res.Format)
	}
}

func TestITQueryTruncation(t *testing.T) {
	conn := newTestConn(t)
	res, err := conn.Query(context.Background(), engine.QueryRequest{
		SQL:    "SELECT generate_series(1, 100) AS n",
		Limits: &engine.ExecutionLimits{MaxRows: intPtr(10)},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if res.RowCount != 10 {
		t.Fatalf("row_count = %d, want 10 (truncated)", res.RowCount)
	}
	if !res.Truncated {
		t.Fatal("expected truncated=true")
	}
}

func TestITSampleRows(t *testing.T) {
	conn := newTestConn(t)
	res, err := conn.SampleRows(context.Background(), engine.SampleRequest{
		TableRef: engine.TableRef{Schema: "public", Table: "books"},
		MaxRows:  10,
	})
	if err != nil {
		t.Fatalf("SampleRows: %v", err)
	}
	if res.Result.RowCount == 0 {
		t.Fatal("sample returned no rows")
	}
	if !res.Deterministic {
		t.Fatal("books has a PK, sample should be deterministic")
	}
}

func TestITSampleRowsReportsTruncation(t *testing.T) {
	conn := newTestConn(t)
	res, err := conn.SampleRows(context.Background(), engine.SampleRequest{
		TableRef: engine.TableRef{Schema: "public", Table: "books"},
		MaxRows:  1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Result.RowCount != 1 || !res.Result.Truncated || res.Result.TruncationReason == nil || *res.Result.TruncationReason != "rows" {
		t.Fatalf("sample result = %+v, want row truncation", res.Result)
	}
}

func TestITCountRows(t *testing.T) {
	conn := newTestConn(t)
	res, err := conn.CountRows(context.Background(), engine.TableRef{Schema: "public", Table: "authors"}, false)
	if err != nil {
		t.Fatalf("CountRows: %v", err)
	}
	if res.Count != "2" {
		t.Fatalf("count = %q, want 2", res.Count)
	}
	if res.Estimated {
		t.Fatalf("estimated = true, want false for exact count")
	}
}

func TestITExplain(t *testing.T) {
	conn := newTestConn(t)
	res, err := conn.Explain(context.Background(), engine.ExplainRequest{
		SQL: "SELECT id FROM public.books WHERE author_id = 1",
	})
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if res.Format != "json" {
		t.Fatalf("format = %q", res.Format)
	}
	plan, ok := res.Plan.(map[string]any)
	if !ok {
		t.Fatalf("plan type = %T", res.Plan)
	}
	if plan["node_type"] == "" {
		t.Fatal("missing node_type")
	}
}

func TestITExplainRejectsView(t *testing.T) {
	conn := newTestConn(t)
	_, err := conn.Explain(context.Background(), engine.ExplainRequest{
		SQL: "SELECT * FROM public.author_books",
	})
	if err == nil {
		t.Fatal("expected QUERY_REJECTED for EXPLAIN over view")
	}
}

func TestITExplainAnalyze(t *testing.T) {
	conn := newTestConn(t)
	res, err := conn.Explain(context.Background(), engine.ExplainRequest{
		SQL:     "SELECT id FROM public.books WHERE author_id = 1",
		Analyze: true,
	})
	if err != nil {
		t.Fatalf("Explain(analyze): %v", err)
	}
	plan, ok := res.Plan.(map[string]any)
	if !ok {
		t.Fatalf("plan type = %T", res.Plan)
	}
	if _, ok := plan["actual_rows"]; !ok {
		t.Fatal("analyze plan missing actual_rows")
	}
	if _, ok := plan["actual_total_time"]; !ok {
		t.Fatal("analyze plan missing actual_total_time")
	}
}

func TestITCountRowsEstimate(t *testing.T) {
	conn := newTestConn(t)
	res, err := conn.CountRows(context.Background(), engine.TableRef{Schema: "public", Table: "authors"}, true)
	if err != nil {
		t.Fatalf("CountRows(estimate): %v", err)
	}
	if !res.Estimated {
		t.Fatal("estimated = false, want true")
	}
}

func TestITSuggestRelationships(t *testing.T) {
	conn := newTestConn(t)
	page, err := conn.SuggestRelationships(context.Background(), engine.SuggestRelationshipsRequest{
		Page: engine.PageRequest{Limit: 50},
	})
	if err != nil {
		t.Fatalf("SuggestRelationships: %v", err)
	}
	foundNameJoin := false
	for _, item := range page.Items {
		if item.Column != "name" {
			continue
		}
		refs := map[string]bool{item.Source.Schema + "." + item.Source.Name: true, item.Target.Schema + "." + item.Target.Name: true}
		if refs["public.authors"] && refs["public.tags"] {
			foundNameJoin = true
		}
		if item.Source.Schema == "hidden" || item.Target.Schema == "hidden" {
			t.Fatalf("unauthorized relation leaked in suggestion: %+v", item)
		}
	}
	if !foundNameJoin {
		t.Fatalf("expected logical join authors.name <-> tags.name in %+v", page.Items)
	}
}

func TestITSuggestRelationshipsByTable(t *testing.T) {
	conn := newTestConn(t)
	schema := "public"
	table := "authors"
	page, err := conn.SuggestRelationships(context.Background(), engine.SuggestRelationshipsRequest{
		Schema: &schema,
		Table:  &table,
		Page:   engine.PageRequest{Limit: 50},
	})
	if err != nil {
		t.Fatalf("SuggestRelationships by table: %v", err)
	}
	for _, item := range page.Items {
		involves := item.Source.Schema+"."+item.Source.Name == "public.authors" || item.Target.Schema+"."+item.Target.Name == "public.authors"
		if !involves {
			t.Fatalf("suggestion does not involve filtered table: %+v", item)
		}
	}
}

func intPtr(v int) *int { return &v }
