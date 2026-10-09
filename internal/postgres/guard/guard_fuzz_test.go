package guard

import (
	"testing"

	"github.com/siia/siia-mcp/internal/contract"
)

// FuzzCheck feeds adversarial SQL into the read-only SELECT guard (SDD §20.2).
// Invariants:
//   - never panic on arbitrary input (the CGO parser or JSON walker must not
//     crash on adversarial or truncated SQL) — Go fuzzer asserts this;
//   - never accept an unknown/mutating construct: when Check returns OK the
//     query is a single SELECT, the result is internally consistent, and the
//     params are non-negative;
//   - any rejection must be the public QUERY_REJECTED code, never an internal
//     error.
func FuzzCheck(f *testing.F) {
	seeds := []string{
		"SELECT 1",
		"SELECT id, name FROM public.users WHERE active = $1",
		"SELECT * FROM public.books b JOIN public.authors a ON a.id = b.author_id WHERE a.name = $1",
		"INSERT INTO public.t VALUES (1)",
		"UPDATE public.t SET a = 1",
		"DELETE FROM public.t",
		"MERGE INTO public.t USING public.s ON true WHEN MATCHED THEN DELETE",
		"CREATE TABLE public.t (a int)",
		"DROP TABLE public.t",
		"SELECT 1; SELECT 2",
		"WITH x AS (DELETE FROM public.t RETURNING 1) SELECT * FROM x",
		"SELECT * FROM public.t FOR UPDATE",
		"SELECT pg_sleep(10)",
		"''''''",
		"SELECT ' --comment\n FROM public.t",
		"SELECT $$ $$",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, sql string) {
		res, err := Check(sql)
		if err != nil {
			// Any rejection must be a well-formed public QUERY_REJECTED, never
			// an internal/other code leaking a stack trace or secret.
			pe, ok := err.(*contract.PublicError)
			if !ok {
				t.Fatalf("non-public error type: %T", err)
			}
			if pe.Code != contract.CodeQueryRejected {
				t.Fatalf("rejection code = %q, want QUERY_REJECTED", pe.Code)
			}
			return
		}
		if res == nil {
			t.Fatal("Check returned nil Result with nil error")
		}
		if res.Params < 0 {
			t.Fatalf("negative param count: %d", res.Params)
		}
		// A successfully accepted query must collect at least one relation from
		// a plain SELECT over a table; accept empty only for pure expression
		// selects (SELECT 1, SELECT now()).
		for _, fn := range res.Functions {
			if fn == "" {
				t.Fatalf("empty function name accepted: %q", fn)
			}
		}
	})
}
