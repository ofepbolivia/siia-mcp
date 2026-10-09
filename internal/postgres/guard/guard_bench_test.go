//go:build bench

package guard

import "testing"

// BenchGuardSQL microbenchmarks parse+classify+authz (SDD §21, benchmark §3.1).
// The allowlist callback mirrors the adapter's public-schema policy.
func benchSQL(b *testing.B, sql string) {
	for i := 0; i < b.N; i++ {
		if _, err := Check(sql); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkGuardSingleSelect(b *testing.B) {
	benchSQL(b, "SELECT * FROM authors WHERE name = $1 ORDER BY id LIMIT 10")
}

// BenchmarkGuardSingleSelectCached measures the memoized verdict path used by
// the adapter for repeated parameterized statements.
func BenchmarkGuardSingleSelectCached(b *testing.B) {
	checker := NewChecker(128)
	sql := "SELECT * FROM authors WHERE name = $1 ORDER BY id LIMIT 10"
	for i := 0; i < b.N; i++ {
		if _, err := checker.Check(sql); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkGuardJoinWhere(b *testing.B) {
	benchSQL(b, "SELECT a.title, b.name FROM books a JOIN authors b ON a.author_id = b.id WHERE b.name ILIKE $1 AND a.year > $2")
}

func BenchmarkGuardRejectWrite(b *testing.B) {
	// write rejection: exercises fail-closed path
	for i := 0; i < b.N; i++ {
		if _, err := Check("DELETE FROM authors"); err == nil {
			b.Fatal("expected rejection")
		}
	}
}

func BenchmarkGuardRejectMultiStmt(b *testing.B) {
	for i := 0; i < b.N; i++ {
		if _, err := Check("SELECT 1; SELECT 2"); err == nil {
			b.Fatal("expected rejection")
		}
	}
}
