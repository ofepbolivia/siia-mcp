package cursor

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func TestCodecRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		ctx  Context
		last SortKey
	}{
		{
			name: "schema cursor without filters",
			ctx: Context{
				Operation:    "db_list_schemas",
				Connection:   "analytics",
				PolicyDigest: digest("policy-a"),
			},
			last: SortKey{Values: []string{"public"}, OID: 2200},
		},
		{
			name: "relationship cursor with exact filters",
			ctx: Context{
				Operation:    "db_list_relationships",
				Connection:   "warehouse",
				Filters:      map[string]string{"table": "orders", "schema": "sales"},
				PolicyDigest: digest("policy-b"),
			},
			last: SortKey{Values: []string{"sales", "orders", "orders_customer_fk", "crm", "customers"}, OID: 9876},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			codec, err := New(bytesOf(0x42, sha256.Size))
			if err != nil {
				t.Fatal(err)
			}
			first, err := codec.Encode(tt.ctx, tt.last)
			if err != nil {
				t.Fatal(err)
			}
			second, err := codec.Encode(tt.ctx, tt.last)
			if err != nil {
				t.Fatal(err)
			}
			if first != second {
				t.Fatalf("Encode is not deterministic:\n%s\n%s", first, second)
			}
			if strings.Contains(first, "=") {
				t.Fatalf("token %q is padded", first)
			}
			got, err := codec.Decode(first, tt.ctx)
			if err != nil {
				t.Fatal(err)
			}
			if got.OID != tt.last.OID || strings.Join(got.Values, "\x00") != strings.Join(tt.last.Values, "\x00") {
				t.Fatalf("Decode = %#v, want %#v", got, tt.last)
			}
		})
	}
}

func TestDecodeRejectsInvalidCursor(t *testing.T) {
	key := bytesOf(0x11, sha256.Size)
	codec, err := New(key)
	if err != nil {
		t.Fatal(err)
	}
	ctx := Context{
		Operation:    "db_list_tables",
		Connection:   "primary",
		Filters:      map[string]string{"schema": "public"},
		PolicyDigest: digest("policy"),
	}
	token, err := codec.Encode(ctx, SortKey{Values: []string{"public", "users", "table"}, OID: 42})
	if err != nil {
		t.Fatal(err)
	}
	tampered := token[:len(token)-1] + string(alternateBase64Byte(token[len(token)-1]))
	otherCodec, err := New(bytesOf(0x22, sha256.Size))
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		codec    *Codec
		token    string
		expected Context
	}{
		{name: "empty", codec: codec, expected: ctx},
		{name: "malformed base64", codec: codec, token: "not+base64", expected: ctx},
		{name: "truncated", codec: codec, token: base64.RawURLEncoding.EncodeToString([]byte("short")), expected: ctx},
		{name: "tampered", codec: codec, token: tampered, expected: ctx},
		{name: "different process key", codec: otherCodec, token: token, expected: ctx},
		{name: "operation mismatch", codec: codec, token: token, expected: withOperation(ctx, "db_list_indexes")},
		{name: "connection mismatch", codec: codec, token: token, expected: withConnection(ctx, "replica")},
		{name: "filter value mismatch", codec: codec, token: token, expected: withFilters(ctx, map[string]string{"schema": "private"})},
		{name: "filter set mismatch", codec: codec, token: token, expected: withFilters(ctx, map[string]string{"schema": "public", "table": "users"})},
		{name: "policy mismatch", codec: codec, token: token, expected: withPolicy(ctx, digest("changed-policy"))},
		{name: "oversized", codec: codec, token: strings.Repeat("a", MaxTokenSize+1), expected: ctx},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := tt.codec.Decode(tt.token, tt.expected); !errors.Is(err, ErrInvalidCursor) {
				t.Fatalf("Decode error = %v, want ErrInvalidCursor", err)
			}
		})
	}
}

func TestEncodeRejectsInvalidInput(t *testing.T) {
	codec, err := New(bytesOf(0x33, sha256.Size))
	if err != nil {
		t.Fatal(err)
	}
	valid := Context{Operation: "db_list_schemas", Connection: "primary", PolicyDigest: digest("policy")}

	tests := []struct {
		name string
		ctx  Context
		last SortKey
	}{
		{name: "empty operation", ctx: withOperation(valid, ""), last: SortKey{Values: []string{"public"}}},
		{name: "empty connection", ctx: withConnection(valid, ""), last: SortKey{Values: []string{"public"}}},
		{name: "empty policy digest", ctx: withPolicy(valid, nil), last: SortKey{Values: []string{"public"}}},
		{name: "empty sort key", ctx: valid},
		{name: "token exceeds limit", ctx: valid, last: SortKey{Values: []string{strings.Repeat("x", MaxTokenSize)}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := codec.Encode(tt.ctx, tt.last); !errors.Is(err, ErrInvalidCursor) {
				t.Fatalf("Encode error = %v, want ErrInvalidCursor", err)
			}
		})
	}
}

func TestNewCopiesAndValidatesKey(t *testing.T) {
	tests := []struct {
		name    string
		key     []byte
		wantErr bool
	}{
		{name: "nil key", wantErr: true},
		{name: "short key", key: bytesOf(1, sha256.Size-1), wantErr: true},
		{name: "sha256 sized key", key: bytesOf(2, sha256.Size)},
		{name: "longer key", key: bytesOf(3, sha256.Size+1)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			codec, err := New(tt.key)
			if tt.wantErr {
				if !errors.Is(err, ErrInvalidKey) {
					t.Fatalf("New error = %v, want ErrInvalidKey", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}

			ctx := Context{Operation: "db_list_schemas", Connection: "primary", PolicyDigest: digest("policy")}
			token, err := codec.Encode(ctx, SortKey{Values: []string{"public"}, OID: 1})
			if err != nil {
				t.Fatal(err)
			}
			tt.key[0] ^= 0xff
			if _, err := codec.Decode(token, ctx); err != nil {
				t.Fatalf("Decode after caller key mutation: %v", err)
			}
		})
	}
}

func digest(value string) []byte {
	sum := sha256.Sum256([]byte(value))
	return sum[:]
}

func bytesOf(value byte, n int) []byte {
	return []byte(strings.Repeat(string([]byte{value}), n))
}

func alternateBase64Byte(current byte) byte {
	if current == 'A' {
		return 'B'
	}
	return 'A'
}

func withOperation(ctx Context, operation string) Context {
	ctx.Operation = operation
	return ctx
}

func withConnection(ctx Context, connection string) Context {
	ctx.Connection = connection
	return ctx
}

func withFilters(ctx Context, filters map[string]string) Context {
	ctx.Filters = filters
	return ctx
}

func withPolicy(ctx Context, policy []byte) Context {
	ctx.PolicyDigest = policy
	return ctx
}
