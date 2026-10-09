package postgres

import (
	"encoding/base64"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/siia/siia-mcp/internal/contract"
)

// Format classification constants mirrored into result columns.
const (
	formatBool     = "bool"
	formatNumber   = "number"
	formatDecimal  = "decimal"
	formatString   = "string"
	formatDatetime = "datetime"
	formatJSON     = "json"
	formatBinary   = "binary"
	formatText     = "text"
)

// classifyFormat maps a PostgreSQL type OID to a column format label.
func classifyFormat(oid uint32) string {
	switch oid {
	case 16: // bool
		return formatBool
	case 20, 21, 23: // int8, int2, int4
		return formatNumber
	case 700, 701: // float4, float8
		return formatNumber
	case 1700, 790: // numeric, money
		return formatDecimal
	case 114, 3802, 3807: // json, jsonb, jsonpath
		return formatJSON
	case 17: // bytea
		return formatBinary
	case 1082, 1114, 1184, 1186, 1083: // date, timestamp, timestamptz, interval, time
		return formatDatetime
	case 2950, 25, 1042, 1043, 19: // uuid, text, bpchar, varchar, name
		return formatString
	default:
		return formatText
	}
}

// encodeValue converts raw PostgreSQL text (already in session formatting)
// into the canonical JSON value for a column OID (SDD §14.2). A nil slice is
// SQL NULL.
func encodeValue(oid uint32, data []byte) (any, error) {
	if data == nil {
		return nil, nil
	}
	s := string(data)
	switch oid {
	case 16: // bool
		return s == "t", nil
	case 21, 23: // int2, int4
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return nil, &contract.PublicError{Code: contract.CodeResultValueTooLarge, Message: "invalid integer value"}
		}
		return n, nil
	case 700, 701: // float4, float8
		switch s {
		case "NaN":
			return "NaN", nil
		case "Infinity":
			return "Infinity", nil
		case "-Infinity":
			return "-Infinity", nil
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil || math.IsInf(f, 0) {
			return nil, &contract.PublicError{Code: contract.CodeResultValueTooLarge, Message: "invalid float value"}
		}
		return f, nil
	case 20, 1700, 790: // int8, numeric, money -> exact string
		return s, nil
	case 17: // bytea becomes base64 (hex input from session invariants)
		b, err := hexToBytes(s)
		if err != nil {
			return nil, &contract.PublicError{Code: contract.CodeResultValueTooLarge, Message: "invalid bytea value"}
		}
		return base64.StdEncoding.EncodeToString(b), nil
	case 114, 3802: // json, jsonb -> textual JSON string
		return s, nil
	default:
		return s, nil
	}
}

// hexToBytes decodes the hex representation produced by bytea_output = hex.
func hexToBytes(s string) ([]byte, error) {
	if strings.HasPrefix(s, `\x`) {
		s = s[2:]
	}
	if len(s)%2 != 0 {
		return nil, fmt.Errorf("odd length hex")
	}
	out := make([]byte, len(s)/2)
	for i := 0; i < len(out); i++ {
		hi := fromHex(s[2*i])
		lo := fromHex(s[2*i+1])
		if hi < 0 || lo < 0 {
			return nil, fmt.Errorf("invalid hex")
		}
		out[i] = byte(hi<<4 | lo)
	}
	return out, nil
}

func fromHex(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}
