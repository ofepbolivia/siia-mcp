package postgres

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"

	"github.com/siia/siia-mcp/internal/contract"
	"github.com/siia/siia-mcp/internal/postgres/guard"
)

// buildArgs validates typed parameters and converts them to pgx arguments.
// It enforces the count/byte limits and the serialization forms of §13.
func buildArgs(params []contract.ParameterInput, limit limits) ([]any, error) {
	if len(params) > limit.ParameterCount {
		return nil, &contract.PublicError{Code: contract.CodeInputLimitExceeded, Message: "too many parameters"}
	}
	args := make([]any, 0, len(params))
	total := 0
	for _, p := range params {
		arg, size, err := encodeParam(p)
		if err != nil {
			return nil, err
		}
		if size > limit.ParameterBytes {
			return nil, &contract.PublicError{Code: contract.CodeInputLimitExceeded, Message: "parameter value too large"}
		}
		total += size
		if total > limit.ParametersTotalBytes {
			return nil, &contract.PublicError{Code: contract.CodeInputLimitExceeded, Message: "parameters exceed total byte limit"}
		}
		args = append(args, arg)
	}
	return args, nil
}

func encodeParam(p contract.ParameterInput) (arg any, size int, err error) {
	if p.Type == "" {
		return nil, 0, &contract.PublicError{Code: contract.CodeInvalidRequest, Message: "parameter type is required"}
	}
	typ := normalizeParamType(p.Type)
	size = len(p.Type)
	if p.Value == nil {
		return nil, size, nil
	}
	switch typ {
	case "bool":
		b, ok := p.Value.(bool)
		if !ok {
			return nil, 0, paramTypeError(p.Type)
		}
		return b, size, nil
	case "int2", "int4", "int8":
		s, ok := scalarParamString(p.Value)
		if !ok {
			return nil, 0, paramTypeError(p.Type)
		}
		n, perr := strconv.ParseInt(s, 10, 64)
		if perr != nil {
			return nil, 0, &contract.PublicError{Code: contract.CodeInvalidRequest, Message: "invalid integer parameter"}
		}
		return n, size + len(s), nil
	case "float4", "float8":
		switch v := p.Value.(type) {
		case float64:
			return v, size, nil
		case string:
			if v == "NaN" || v == "Infinity" || v == "-Infinity" {
				f, _ := strconv.ParseFloat(v, 64)
				return f, size + len(v), nil
			}
		}
		return nil, 0, paramTypeError(p.Type)
	case "numeric", "text", "uuid", "date", "timestamp", "timestamptz", "json", "jsonb", "array":
		s, ok := p.Value.(string)
		if !ok {
			return nil, 0, paramTypeError(p.Type)
		}
		return s, size + len(s), nil
	case "bytea":
		s, ok := p.Value.(string)
		if !ok {
			return nil, 0, paramTypeError(p.Type)
		}
		b, derr := base64.StdEncoding.DecodeString(s)
		if derr != nil {
			return nil, 0, &contract.PublicError{Code: contract.CodeInvalidRequest, Message: "invalid bytea parameter"}
		}
		return b, size + len(b), nil
	default:
		return nil, 0, &contract.PublicError{Code: contract.CodeInvalidRequest, Message: fmt.Sprintf("unsupported parameter type %q", p.Type)}
	}
}

func normalizeParamType(t string) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "boolean":
		return "bool"
	case "smallint", "int2":
		return "int2"
	case "integer", "int", "int4":
		return "int4"
	case "bigint", "int8":
		return "int8"
	case "real":
		return "float4"
	case "double precision":
		return "float8"
	case "decimal":
		return "numeric"
	case "varchar", "character varying", "char", "character":
		return "text"
	default:
		return strings.ToLower(strings.TrimSpace(t))
	}
}

func scalarParamString(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		return x, true
	case float64:
		if x == float64(int64(x)) {
			return strconv.FormatInt(int64(x), 10), true
		}
		return "", false
	default:
		return "", false
	}
}

func paramTypeError(t string) error {
	return &contract.PublicError{Code: contract.CodeInvalidRequest, Message: "invalid value for parameter type " + t}
}

// validatePlaceholders checks that placeholders are contiguous 1..N. count is
// the number of provided parameters.
func validatePlaceholders(res *guard.Result, count int) error {
	if res.Params == 0 && count == 0 {
		return nil
	}
	if res.Params != count {
		return &contract.PublicError{
			Code:    contract.CodeParameterMismatch,
			Message: fmt.Sprintf("query uses %d placeholders but %d parameters were provided", res.Params, count),
		}
	}
	return nil
}
