package guard

import (
	"strings"
	"testing"

	"github.com/siia/siia-mcp/internal/contract"
)

func mustReject(t *testing.T, sql string) {
	t.Helper()
	_, err := Check(sql)
	if err == nil {
		t.Fatalf("expected rejection for %q", sql)
	}
	pe := contract.AsPublicError(err)
	if pe.Code != contract.CodeQueryRejected {
		t.Fatalf("code = %q, want QUERY_REJECTED for %q", pe.Code, sql)
	}
}

func mustAccept(t *testing.T, sql string) *Result {
	t.Helper()
	res, err := Check(sql)
	if err != nil {
		t.Fatalf("unexpected rejection for %q: %v", sql, err)
	}
	return res
}

func TestSimpleSelect(t *testing.T) {
	res := mustAccept(t, "SELECT id, name FROM public.users WHERE active = $1")
	if len(res.Relations) != 1 || res.Relations[0].Schema != "public" || res.Relations[0].Name != "users" {
		t.Fatalf("relations = %+v", res.Relations)
	}
	if res.Params != 1 {
		t.Fatalf("params = %d, want 1", res.Params)
	}
}

func TestRejectMutators(t *testing.T) {
	for _, sql := range []string{
		"INSERT INTO public.t VALUES (1)",
		"UPDATE public.t SET a=1",
		"DELETE FROM public.t",
		"MERGE INTO public.t USING public.s ON true WHEN MATCHED THEN DELETE",
		"CREATE TABLE public.t (a int)",
		"DROP TABLE public.t",
		"SET search_path = public",
	} {
		mustReject(t, sql)
	}
}

func TestRejectMultiStatement(t *testing.T) {
	mustReject(t, "SELECT 1; SELECT 2")
}

func TestRejectNonSelect(t *testing.T) {
	mustReject(t, "WITH x AS (DELETE FROM public.t RETURNING 1) SELECT * FROM x")
}

func TestRejectSelectInto(t *testing.T) {
	mustReject(t, "SELECT * INTO public.newtab FROM public.t")
}

func TestRejectForUpdate(t *testing.T) {
	for _, sql := range []string{
		"SELECT * FROM public.t FOR UPDATE",
		"SELECT * FROM public.t FOR SHARE",
		"SELECT * FROM public.t FOR NO KEY UPDATE",
	} {
		mustReject(t, sql)
	}
}

func TestRejectExplainInsideQuery(t *testing.T) {
	mustReject(t, "EXPLAIN SELECT * FROM public.t")
}

func TestRejectCTEWithMutation(t *testing.T) {
	mustReject(t, "WITH x AS (UPDATE public.t SET a=1 RETURNING *) SELECT * FROM x")
}

func TestCollectFunctionsAndCTEs(t *testing.T) {
	res := mustAccept(t, "WITH base AS (SELECT id FROM public.t) SELECT lower(name) FROM base WHERE now() > $1")
	if len(res.CTEs) != 1 || res.CTEs[0] != "base" {
		t.Fatalf("ctes = %+v", res.CTEs)
	}
	found := false
	for _, f := range res.Functions {
		if strings.HasSuffix(f, "lower") || f == "lower" {
			found = true
		}
	}
	if !found {
		t.Fatalf("functions = %+v, want lower", res.Functions)
	}
	if res.Params != 1 {
		t.Fatalf("params = %d, want 1", res.Params)
	}
}

func TestRejectSelectIntoCheck(t *testing.T) {
	res := mustAccept(t, "SELECT 1")
	if res.HasIntoTarget {
		t.Fatal("select has into target")
	}
}

func TestCTEReferenceNotRequiresSchema(t *testing.T) {
	// The CTE reference in the final SELECT must not be collected as a
	// persistent relation; only the real base tables inside the CTE body are.
	res := mustAccept(t, "WITH counts AS (SELECT id FROM public.t) SELECT * FROM counts")
	if len(res.CTEs) != 1 || res.CTEs[0] != "counts" {
		t.Fatalf("ctes = %+v, want [counts]", res.CTEs)
	}
	if len(res.Relations) != 1 || res.Relations[0].Schema != "public" || res.Relations[0].Name != "t" {
		t.Fatalf("relations = %+v, want only public.t", res.Relations)
	}
}

func TestUnqualifiedBaseTableStillCollected(t *testing.T) {
	// A non-CTE unqualified table reference remains a relation with an empty
	// schema, so authorization can reject it as not schema-qualified.
	res := mustAccept(t, "SELECT * FROM users")
	if len(res.Relations) != 1 || res.Relations[0].Schema != "" || res.Relations[0].Name != "users" {
		t.Fatalf("relations = %+v, want unqualified users", res.Relations)
	}
}

func TestCTEShadowingSchemaQualifiedTable(t *testing.T) {
	// A CTE named like a real table must not mask an explicit schema-qualified
	// reference to that table.
	res := mustAccept(t, "WITH users AS (SELECT 1) SELECT * FROM public.users")
	if len(res.Relations) != 1 || res.Relations[0].Schema != "public" || res.Relations[0].Name != "users" {
		t.Fatalf("relations = %+v, want public.users", res.Relations)
	}
}
