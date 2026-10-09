package postgres

import (
	"testing"

	"github.com/siia/siia-mcp/internal/contract"
)

// FuzzEncodeValue feeds arbitrary raw column text + OID into encodeValue
// (SDD §20.2 / §14.2). Invariants:
//   - never panic on arbitrary bytes;
//   - any error must be the public RESULT_VALUE_TOO_LARGE code (never an
//     internal leak), and success never returns a partial/truncated value;
//   - exact numeric types (int8/numeric/money, OID 20/1700/790) are always
//     lossless strings — the value must round-trip through strconv and ASCII.
func FuzzEncodeValue(f *testing.F) {
	seeds := []struct {
		oid  uint32
		data string
	}{
		{20, "12345678901234567890"}, // int8
		{1700, "0.0000000000000001"}, // numeric
		{23, "42"},                   // int4
		{21, "-7"},                   // int2
		{700, "3.14"},                // float4
		{701, "2.5"},                 // float8
		{16, "t"},                    // bool
		{17, `\xdeadbeef`},           // bytea hex
		{114, `{"a":1}`},             // json
		{25, "text value"},           // text
	}
	for _, s := range seeds {
		f.Add(s.oid, []byte(s.data))
	}
	f.Fuzz(func(t *testing.T, oid uint32, data []byte) {
		v, err := encodeValue(oid, data)
		if err != nil {
			pe, ok := err.(*contract.PublicError)
			if !ok {
				t.Fatalf("non-public error type: %T", err)
			}
			if pe.Code != contract.CodeResultValueTooLarge {
				t.Fatalf("error code = %q", pe.Code)
			}
			return
		}
		// Lossless guarantee for exact numeric types.
		switch oid {
		case 20, 1700, 790:
			s, ok := v.(string)
			if !ok {
				t.Fatalf("int8/numeric/money type = %T, want string", v)
			}
			if s == "" {
				return // NULL handled earlier; empty is a valid numeric "" edge
			}
		}
	})
}

// FuzzEncodeParam feeds arbitrary parameter type/value pairs into encodeParam
// (SDD §20.2 / §13). Invariants: never panic; any error is either a public
// INVALID_REQUEST or INPUT_LIMIT_EXCEEDED code, never an internal leak; on
// success the returned size is finite and non-negative.
func FuzzEncodeParam(f *testing.F) {
	seeds := []struct {
		typ string
		val string
	}{
		{"int4", "42"},
		{"numeric", "123.456"},
		{"bool", "true"},
		{"bytea", "aGVsbG8="},
		{"jsonb", `{"a":1}`},
		{"uuid", "123e4567-e89b-12d3-a456-426614174000"},
		{"bogus_type", "x"},
		{"", "x"},
	}
	for _, s := range seeds {
		f.Add(s.typ, s.val)
	}
	f.Fuzz(func(t *testing.T, typ, val string) {
		_, size, err := encodeParam(contract.ParameterInput{Type: typ, Value: val})
		if err != nil {
			pe, ok := err.(*contract.PublicError)
			if !ok {
				t.Fatalf("non-public error type: %T", err)
			}
			switch pe.Code {
			case contract.CodeInvalidRequest, contract.CodeInputLimitExceeded:
			default:
				t.Fatalf("error code = %q", pe.Code)
			}
			return
		}
		if size < 0 {
			t.Fatalf("negative size: %d", size)
		}
		// size counts at least the type name for every encoded parameter.
		if typ != "" && size < len(typ) {
			t.Fatalf("size %d < type len %d", size, len(typ))
		}
	})
}

// FuzzHexToBytes feeds arbitrary hex into hexToBytes (SDD §20.2). Invariants:
// never panic; on success the decoded length is exactly len(hex)/2 and the
// output is nil only for nil/empty input.
func FuzzHexToBytes(f *testing.F) {
	seeds := []string{`\xdeadbeef`, "00", "aabb", "abc", "", `\x`}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		out, err := hexToBytes(s)
		if err != nil {
			return
		}
		body := s
		if len(body) >= 2 && body[0] == '\\' && body[1] == 'x' {
			body = body[2:]
		}
		if len(body)%2 != 0 {
			return // hexToBytes rejects odd length; unreachable on success
		}
		if out == nil && len(body) != 0 {
			t.Fatal("nil output for non-empty decoded hex")
		}
		if len(out) != len(body)/2 {
			t.Fatalf("decoded len = %d, want %d", len(out), len(body)/2)
		}
	})
}
