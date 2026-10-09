package contract

// PublicErrorCode is the stable public error taxonomy returned by every MCP
// tool. Codes must not change; they are part of the frozen contract.
type PublicErrorCode string

const (
	CodeInvalidRequest        PublicErrorCode = "INVALID_REQUEST"
	CodeInvalidCursor         PublicErrorCode = "INVALID_CURSOR"
	CodeConnectionNotFound    PublicErrorCode = "CONNECTION_NOT_FOUND"
	CodeConnectionUnavailable PublicErrorCode = "CONNECTION_UNAVAILABLE"
	CodeObjectNotAllowed      PublicErrorCode = "OBJECT_NOT_ALLOWED"
	CodeObjectNotFound        PublicErrorCode = "OBJECT_NOT_FOUND"
	CodeQueryRejected         PublicErrorCode = "QUERY_REJECTED"
	CodeParameterMismatch     PublicErrorCode = "PARAMETER_MISMATCH"
	CodeInputLimitExceeded    PublicErrorCode = "INPUT_LIMIT_EXCEEDED"
	CodeResultValueTooLarge   PublicErrorCode = "RESULT_VALUE_TOO_LARGE"
	CodeTimeout               PublicErrorCode = "TIMEOUT"
	CodeCancelled             PublicErrorCode = "CANCELLED"
	CodeServerBusy            PublicErrorCode = "SERVER_BUSY"
	CodeMetadataUnavailable   PublicErrorCode = "METADATA_UNAVAILABLE"
	CodeAuditUnavailable      PublicErrorCode = "AUDIT_UNAVAILABLE"
	CodeDatabaseError         PublicErrorCode = "DATABASE_ERROR"
	CodeInternalError         PublicErrorCode = "INTERNAL_ERROR"
)

// Retryable reports whether an error with this code may be retried.
func (c PublicErrorCode) Retryable() bool {
	switch c {
	case CodeConnectionUnavailable, CodeTimeout, CodeCancelled, CodeServerBusy,
		CodeMetadataUnavailable, CodeAuditUnavailable:
		return true
	case CodeDatabaseError:
		return false
	default:
		return false
	}
}

// ErrorEnvelope is the structured error body attached to every failed tool
// result.
type ErrorEnvelope struct {
	Code      PublicErrorCode `json:"code"`
	Message   string          `json:"message"`
	Retryable bool            `json:"retryable"`
	RequestID string          `json:"request_id"`
}

// ErrorEnvelopeRoot is the versioned error envelope returned in a failed tool
// result's structured content (SDD §11.4).
type ErrorEnvelopeRoot struct {
	SchemaVersion string        `json:"schema_version"`
	Error         ErrorEnvelope `json:"error"`
}

// PublicError is an error carrying a stable public code. It implements error
// and is safe to surface to clients.
type PublicError struct {
	Code      PublicErrorCode
	Message   string
	RequestID string
}

func (e *PublicError) Error() string { return e.Message }

// PublicErrorEnvelope converts the error into its wire envelope.
func (e *PublicError) PublicErrorEnvelope() ErrorEnvelope {
	return ErrorEnvelope{
		Code:      e.Code,
		Message:   e.Message,
		Retryable: e.Code.Retryable(),
		RequestID: e.RequestID,
	}
}

// AsPublicError maps any error into a PublicError, defaulting to
// INTERNAL_ERROR for unexpected values.
func AsPublicError(err error) *PublicError {
	if pe, ok := err.(*PublicError); ok {
		return pe
	}
	return &PublicError{Code: CodeInternalError, Message: "internal error"}
}

// CodeFromError extracts the stable code of any error, defaulting to
// INTERNAL_ERROR for unexpected values.
func CodeFromError(err error) PublicErrorCode {
	if pe, ok := err.(*PublicError); ok {
		return pe.Code
	}
	return CodeInternalError
}
