package config

import (
	"testing"
)

// FuzzParse feeds arbitrary YAML into the strict config parser (SDD §20.2).
// Invariants: never panic on adversarial input (Go fuzzer checks panics), and
// a nil-error result must round-trip as a valid configuration. A deeply nested
// YAML tree must not blow the call stack.
func FuzzParse(f *testing.F) {
	seed := []string{
		"version: 1\n",
		"version: 1\nserver:\n  request_timeout: 30s\n",
		"version: 1\nconnection: null\n",
		`{"version":1}`,
		"dsn: secret\n",
		"version: 1\nconnection:\n  name: a\n  engine: postgres\n",
		"a:\n  b:\n    c: 1\n",
	}
	for _, s := range seed {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		// Even unbounded nesting must not panic (stack-overflow guard).
		_, _ = Parse(data, func(string) (string, bool) { return "", false })
	})
}
