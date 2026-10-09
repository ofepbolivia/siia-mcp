//go:build bench

package postgres

import (
	"testing"

	"github.com/siia/siia-mcp/internal/contract"
)

var benchLimits = limits{
	ParameterCount:       100,
	ParameterBytes:       1 << 20,
	ParametersTotalBytes: 1 << 20,
	QueryBytes:           1 << 14,
}

// BenchmarkEncodeParam / Result encoding cover RNF-06 types (SDD §21, benchmark §3.2).

func BenchmarkEncodeNumericString(b *testing.B) {
	for i := 0; i < b.N; i++ {
		if _, err := encodeValue(1700, []byte("1234567890.12345678901234567890")); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEncodeInt8(b *testing.B) {
	for i := 0; i < b.N; i++ {
		if _, err := encodeValue(20, []byte("9223372036854775807")); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEncodeTimestamp(b *testing.B) {
	for i := 0; i < b.N; i++ {
		if _, err := encodeValue(1184, []byte("2026-01-02 15:04:05.123456+00")); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEncodeJSON(b *testing.B) {
	for i := 0; i < b.N; i++ {
		if _, err := encodeValue(3802, []byte(`{"a":1,"b":[true,null,"x"]}`)); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEncodeBytea(b *testing.B) {
	hex := make([]byte, 0, 512)
	for j := 0; j < 256; j++ {
		hex = append(hex, "0123456789abcdef"[j%16])
	}
	s := string(hex)
	for i := 0; i < b.N; i++ {
		if _, err := encodeValue(17, []byte(s)); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkEncodeParam1 and N exercise typed-parameter serialization (§13).
func BenchmarkEncodeParam1(b *testing.B) {
	p := contract.ParameterInput{Type: "text", Value: "hello world"}
	for i := 0; i < b.N; i++ {
		if _, _, err := encodeParam(p); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkBuildArgs8(b *testing.B) {
	params := []contract.ParameterInput{
		{Type: "text", Value: "author-name"},
		{Type: "int8", Value: "1234567890"},
		{Type: "float8", Value: 3.1415927},
		{Type: "bool", Value: true},
		{Type: "numeric", Value: "9.99"},
		{Type: "uuid", Value: "550e8400-e29b-41d4-a716-446655440000"},
		{Type: "date", Value: "2026-08-30"},
		{Type: "jsonb", Value: `{"k":"v"}`},
	}
	for i := 0; i < b.N; i++ {
		if _, err := buildArgs(params, benchLimits); err != nil {
			b.Fatal(err)
		}
	}
}
