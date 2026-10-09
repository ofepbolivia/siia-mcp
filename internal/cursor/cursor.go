// Package cursor encodes and validates opaque metadata pagination cursors.
package cursor

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
)

const (
	version      = 1
	signatureLen = sha256.Size

	// MaxTokenSize is the maximum encoded cursor size in bytes.
	MaxTokenSize = 1024
)

var (
	// ErrInvalidCursor identifies every malformed, unauthenticated, or
	// context-mismatched cursor. Callers may map it to INVALID_CURSOR.
	ErrInvalidCursor = errors.New("invalid cursor")
	ErrInvalidKey    = errors.New("cursor key must contain at least 32 bytes")
)

// Context binds a cursor to the metadata request that created it. Filters
// contain only the request's base filters, such as schema and table names.
type Context struct {
	Operation    string
	Connection   string
	Filters      map[string]string
	PolicyDigest []byte
}

// SortKey identifies the last item in a keyset page. Values hold the public
// order components; OID is the internal final tie-breaker.
type SortKey struct {
	Values []string `json:"values"`
	OID    uint32   `json:"oid"`
}

// Codec authenticates cursors with a process-local key. New copies key so
// callers may safely reuse or clear their input buffer.
type Codec struct {
	key []byte
}

type payload struct {
	Version      int               `json:"version"`
	Operation    string            `json:"operation"`
	Connection   string            `json:"connection"`
	Filters      map[string]string `json:"filters"`
	PolicyDigest []byte            `json:"policy_digest"`
	Last         SortKey           `json:"last"`
}

// New constructs a codec from a cryptographically random process key supplied
// by the caller. A fresh key makes existing cursors invalid after restart.
func New(key []byte) (*Codec, error) {
	if len(key) < sha256.Size {
		return nil, ErrInvalidKey
	}
	return &Codec{key: append([]byte(nil), key...)}, nil
}

// Encode returns a deterministic, authenticated base64url cursor.
func (c *Codec) Encode(ctx Context, last SortKey) (string, error) {
	if c == nil || len(c.key) < sha256.Size || !validContext(ctx) || len(last.Values) == 0 {
		return "", fmt.Errorf("encode cursor: %w", ErrInvalidCursor)
	}

	p := payload{
		Version:      version,
		Operation:    ctx.Operation,
		Connection:   ctx.Connection,
		Filters:      cloneFilters(ctx.Filters),
		PolicyDigest: append([]byte(nil), ctx.PolicyDigest...),
		Last: SortKey{
			Values: append([]string(nil), last.Values...),
			OID:    last.OID,
		},
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return "", fmt.Errorf("encode cursor: %w", ErrInvalidCursor)
	}

	mac := hmac.New(sha256.New, c.key)
	_, _ = mac.Write(raw)
	signed := append(raw, mac.Sum(nil)...)
	token := base64.RawURLEncoding.EncodeToString(signed)
	if len(token) > MaxTokenSize {
		return "", fmt.Errorf("encode cursor: token exceeds %d bytes: %w", MaxTokenSize, ErrInvalidCursor)
	}
	return token, nil
}

// Decode authenticates token, validates its complete request context, and
// returns its last sort key. Every failure wraps ErrInvalidCursor.
func (c *Codec) Decode(token string, expected Context) (SortKey, error) {
	if c == nil || len(c.key) < sha256.Size || len(token) == 0 || len(token) > MaxTokenSize || !validContext(expected) {
		return SortKey{}, ErrInvalidCursor
	}

	signed, err := base64.RawURLEncoding.Strict().DecodeString(token)
	if err != nil || len(signed) <= signatureLen {
		return SortKey{}, ErrInvalidCursor
	}
	raw, signature := signed[:len(signed)-signatureLen], signed[len(signed)-signatureLen:]
	mac := hmac.New(sha256.New, c.key)
	_, _ = mac.Write(raw)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return SortKey{}, ErrInvalidCursor
	}

	var p payload
	if err := json.Unmarshal(raw, &p); err != nil || !validPayload(p) {
		return SortKey{}, ErrInvalidCursor
	}
	if p.Version != version || p.Operation != expected.Operation || p.Connection != expected.Connection ||
		!maps.Equal(p.Filters, normalizeFilters(expected.Filters)) ||
		!hmac.Equal(p.PolicyDigest, expected.PolicyDigest) {
		return SortKey{}, ErrInvalidCursor
	}

	return SortKey{Values: append([]string(nil), p.Last.Values...), OID: p.Last.OID}, nil
}

func validContext(ctx Context) bool {
	return ctx.Operation != "" && ctx.Connection != "" && len(ctx.PolicyDigest) > 0
}

func validPayload(p payload) bool {
	return p.Version > 0 && p.Operation != "" && p.Connection != "" && p.Filters != nil &&
		len(p.PolicyDigest) > 0 && len(p.Last.Values) > 0
}

func normalizeFilters(filters map[string]string) map[string]string {
	if filters == nil {
		return map[string]string{}
	}
	return filters
}

func cloneFilters(filters map[string]string) map[string]string {
	return maps.Clone(normalizeFilters(filters))
}
