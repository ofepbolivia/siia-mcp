package connection

import "github.com/siia/siia-mcp/internal/contract"

// errUnavailable maps an unavailable/not-ready handle to the public error
// contract.
func errUnavailable(name string) error {
	return &contract.PublicError{
		Code:    contract.CodeConnectionUnavailable,
		Message: "connection is not available",
	}
}
