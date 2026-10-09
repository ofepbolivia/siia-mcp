package postgres

import "testing"

func TestParseServerMajor(t *testing.T) {
	cases := map[string]int{
		"PostgreSQL 16.3 (Debian 16.3-1.pgdg120+1) on x86_64-pc-linux-gnu, compiled by gcc": 16,
		"PostgreSQL 9.6.24 on x86_64-pc-linux-gnu":                                          9,
		"weird output": 0,
	}
	for in, want := range cases {
		if got := parseServerMajor(in); got != want {
			t.Errorf("parseServerMajor(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestReferentialAction(t *testing.T) {
	cases := map[byte]string{
		'a': "NO ACTION",
		'r': "RESTRICT",
		'c': "CASCADE",
		'n': "SET NULL",
		'd': "SET DEFAULT",
		'x': "NO ACTION",
	}
	for in, want := range cases {
		if got := referentialAction(in); got != want {
			t.Errorf("referentialAction(%q) = %q, want %q", in, got, want)
		}
	}
}
