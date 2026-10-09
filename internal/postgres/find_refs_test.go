package postgres

import "testing"

func TestColumnPatternLike(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "user", want: "%user%"},
		{in: "%user%", want: "%user%"},
		{in: "%%user%%", want: "%user%"},
		{in: "  user  ", want: "%user%"},
		{in: "user_id", want: `%user\_id%`},
		{in: "100%_done", want: `%100\%\_done%`},
	}
	for _, tt := range tests {
		if got := columnPatternLike(tt.in); got != tt.want {
			t.Errorf("columnPatternLike(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestBuildReferenceCountSQL(t *testing.T) {
	tests := []struct {
		name    string
		schema  string
		table   string
		column  string
		colType string
		value   string
		want    string
	}{
		{
			name:   "integer column with integer value uses bigint cast",
			schema: "public", table: "books", column: "author_id", colType: "integer", value: "1",
			want: `SELECT count(*)::text FROM "public"."books" WHERE "author_id" = $1::bigint`,
		},
		{
			name:   "integer column with non-integer value falls back to text",
			schema: "public", table: "books", column: "author_id", colType: "integer", value: "abc",
			want: `SELECT count(*)::text FROM "public"."books" WHERE "author_id"::text = $1::text`,
		},
		{
			name:   "text column uses text equality",
			schema: "public", table: "authors", column: "name", colType: "text", value: "Ada",
			want: `SELECT count(*)::text FROM "public"."authors" WHERE "name"::text = $1::text`,
		},
		{
			name:   "identifier with quotes is escaped",
			schema: "pub lic", table: "bo\"oks", column: "na\"me", colType: "text", value: "x",
			want: `SELECT count(*)::text FROM "pub lic"."bo""oks" WHERE "na""me"::text = $1::text`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := buildReferenceCountSQL(tt.schema, tt.table, tt.column, tt.colType, tt.value); got != tt.want {
				t.Errorf("buildReferenceCountSQL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIsIntegerType(t *testing.T) {
	for _, v := range []string{"smallint", "integer", "bigint", "int2", "int4", "int8"} {
		if !isIntegerType(v) {
			t.Errorf("isIntegerType(%q) = false, want true", v)
		}
	}
	for _, v := range []string{"text", "varchar", "numeric", "uuid", "timestamp without time zone", "character varying"} {
		if isIntegerType(v) {
			t.Errorf("isIntegerType(%q) = true, want false", v)
		}
	}
}
