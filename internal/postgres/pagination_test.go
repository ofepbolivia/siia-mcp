package postgres

import (
	"crypto/sha256"
	"errors"
	"testing"

	"github.com/siia/siia-mcp/internal/config"
	"github.com/siia/siia-mcp/internal/contract"
	"github.com/siia/siia-mcp/internal/cursor"
	"github.com/siia/siia-mcp/internal/engine"
)

func TestListSchemasPaginatesWithBoundCursor(t *testing.T) {
	codec, err := cursor.New(make([]byte, sha256.Size))
	if err != nil {
		t.Fatal(err)
	}
	a := &adapter{
		name:         "app",
		cfg:          config.Connection{Allow: &config.Allowlist{Schemas: []string{"zeta", "public", "audit"}}},
		limit:        limits{MetadataPageSize: 2},
		cursors:      codec,
		policyDigest: []byte("policy"),
	}

	first, err := a.ListSchemas(t.Context(), engine.PageRequest{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if got := schemaNames(first.Items); got != "audit,public" || first.NextCursor == nil {
		t.Fatalf("first page = %q cursor=%v", got, first.NextCursor)
	}
	second, err := a.ListSchemas(t.Context(), engine.PageRequest{Limit: 2, Cursor: first.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if got := schemaNames(second.Items); got != "zeta" || second.NextCursor != nil {
		t.Fatalf("second page = %q cursor=%v", got, second.NextCursor)
	}

	tampered := *first.NextCursor
	tampered = tampered[:len(tampered)-1] + "A"
	_, err = a.ListSchemas(t.Context(), engine.PageRequest{Limit: 2, Cursor: &tampered})
	var pe *contract.PublicError
	if !errors.As(err, &pe) || pe.Code != contract.CodeInvalidCursor {
		t.Fatalf("tampered cursor error = %#v, want INVALID_CURSOR", err)
	}
}

func TestMetadataPageLimitCannotBeRaised(t *testing.T) {
	_, err := pageLimit(engine.PageRequest{Limit: 11}, 10)
	var pe *contract.PublicError
	if !errors.As(err, &pe) || pe.Code != contract.CodeInvalidRequest {
		t.Fatalf("pageLimit error = %#v, want INVALID_REQUEST", err)
	}
}

func TestResolveLimitsUsesGlobalValuesAndConnectionReductions(t *testing.T) {
	global := config.LimitsConfig{
		QueryBytes:           100,
		QueryRows:            90,
		SampleRows:           80,
		ValueBytes:           70,
		ResponseBytes:        60,
		MetadataPageSize:     50,
		ParameterCount:       40,
		ParameterBytes:       30,
		ParametersTotalBytes: 20,
	}
	cfg := config.Connection{Limits: &config.ConnectionLimits{
		QueryRows: 9, SampleRows: 8, ValueBytes: 7, ResponseBytes: 6, MetadataPageSize: 5,
	}}
	got := resolveLimits(cfg, global)
	if got.QueryBytes != 100 || got.QueryRows != 9 || got.SampleRows != 8 || got.ValueBytes != 7 ||
		got.ResponseBytes != 6 || got.MetadataPageSize != 5 || got.ParameterCount != 40 ||
		got.ParameterBytes != 30 || got.ParametersTotalBytes != 20 {
		t.Fatalf("resolveLimits() = %+v", got)
	}
}

func TestEffectiveQueryLimitsRejectRaises(t *testing.T) {
	a := &adapter{limit: limits{QueryRows: 10, ResponseBytes: 100}}
	raised := 11
	_, _, err := a.effectiveLimits(&engine.ExecutionLimits{MaxRows: &raised})
	var pe *contract.PublicError
	if !errors.As(err, &pe) || pe.Code != contract.CodeInvalidRequest {
		t.Fatalf("effectiveLimits error = %#v, want INVALID_REQUEST", err)
	}
}

func TestValueLimitAccountsForWireEncoding(t *testing.T) {
	tests := []struct {
		name  string
		oid   uint32
		data  []byte
		limit int
		want  bool
	}{
		{name: "text exact", oid: 25, data: []byte("four"), limit: 6},
		{name: "text over", oid: 25, data: []byte("sixsix"), limit: 5, want: true},
		{name: "escaped text over", oid: 25, data: []byte("<&"), limit: 13, want: true},
		{name: "bytea base64 exact", oid: 17, data: []byte(`\x010203`), limit: 6},
		{name: "bytea base64 over", oid: 17, data: []byte(`\x010203`), limit: 5, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := valueWouldExceedLimit(tt.oid, tt.data, tt.limit); got != tt.want {
				t.Fatalf("valueWouldExceedLimit() = %v, want %v", got, tt.want)
			}
		})
	}
}

func schemaNames(items []contract.SchemaItem) string {
	result := ""
	for _, item := range items {
		if result != "" {
			result += ","
		}
		result += item.Name
	}
	return result
}
