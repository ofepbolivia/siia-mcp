package contract

import (
	"testing"
)

// TestToolsGoldenFreeze ensures the frozen input schemas match the golden
// files in testdata/schemas. Run with -update to (re)generate them.
func TestToolsGoldenFreeze(t *testing.T) {
	if *update {
		if err := GenerateToolsGolden("testdata/schemas"); err != nil {
			t.Fatalf("GenerateToolsGolden: %v", err)
		}
	}
	if err := MustEqualToolGolden("testdata/schemas"); err != nil {
		t.Fatal(err)
	}
}

// TestToolsOrderAndNamesFreeze protects the registry order and required names.
func TestToolsOrderAndNamesFreeze(t *testing.T) {
	want := []string{
		"db_list_connections",
		"db_ping",
		"db_list_schemas",
		"db_list_tables",
		"db_describe_table",
		"db_list_indexes",
		"db_list_relationships",
		"db_suggest_relationships",
		"db_search_columns",
		"db_find_references",
		"db_sample_rows",
		"db_count_rows",
		"db_query",
		"db_explain",
	}
	got := Tools()
	if len(got) != len(want) {
		t.Fatalf("got %d tools, want %d", len(got), len(want))
	}
	for i, name := range want {
		if got[i].Name != name {
			t.Fatalf("tools[%d] = %q, want %q", i, got[i].Name, name)
		}
	}
}

// TestSchemasClosed verifies every tool input object is closed.
func TestSchemasClosed(t *testing.T) {
	for _, tool := range Tools() {
		s := tool.InputSchema
		if s.Type != "object" {
			t.Fatalf("%s: input root type = %v, want object", tool.Name, s.Type)
		}
		if s.AdditionalProperties == nil || *s.AdditionalProperties {
			t.Fatalf("%s: root must set additionalProperties=false", tool.Name)
		}
		if s.Properties == nil {
			t.Fatalf("%s: root must define properties", tool.Name)
		}
		checkClosed(t, tool.Name, s)
	}
}

func checkClosed(t *testing.T, path string, s *Schema) {
	for name, prop := range s.Properties {
		p := path + "." + name
		if prop.Type == "object" {
			if prop.AdditionalProperties == nil || *prop.AdditionalProperties {
				t.Fatalf("%s: object must set additionalProperties=false", p)
			}
			checkClosed(t, p, prop)
		}
		if items := prop.Items; items != nil && items.Type == "object" {
			checkClosed(t, p+"[]", items)
		}
	}
}
