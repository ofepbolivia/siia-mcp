package postgres

import (
	"testing"

	"github.com/siia/siia-mcp/internal/contract"
)

func TestEncodeParamAcceptsPostgresAliasesAndJSONNumbers(t *testing.T) {
	tests := []struct {
		name string
		in   contract.ParameterInput
		want any
	}{
		{name: "bigint alias string", in: contract.ParameterInput{Type: "bigint", Value: "1126"}, want: int64(1126)},
		{name: "int8 JSON number", in: contract.ParameterInput{Type: "int8", Value: float64(1126)}, want: int64(1126)},
		{name: "integer alias", in: contract.ParameterInput{Type: "integer", Value: float64(7)}, want: int64(7)},
		{name: "varchar alias", in: contract.ParameterInput{Type: "varchar", Value: "jpardo"}, want: "jpardo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _, err := encodeParam(tt.in)
			if err != nil {
				t.Fatalf("encodeParam: %v", err)
			}
			if got != tt.want {
				t.Fatalf("arg = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestSensitiveColumnName(t *testing.T) {
	for _, name := range []string{"password", "remember_token", "payload", "api_secret", "database_dsn"} {
		if !sensitiveColumnName(name) {
			t.Fatalf("%q should be sensitive", name)
		}
	}
	if sensitiveColumnName("user_id") {
		t.Fatal("user_id should not be sensitive")
	}
}
