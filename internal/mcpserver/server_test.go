package mcpserver

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/siia/siia-mcp/internal/contract"
)

func TestHandleRequestSuccess(t *testing.T) {
	h := func(ctx context.Context, name string, args json.RawMessage) (any, error) {
		time.Sleep(5 * time.Millisecond)
		reason := "rows"
		return &contract.QueryResult{RowCount: 3, Truncated: true, TruncationReason: &reason}, nil
	}
	result, err := handleRequest(context.Background(), "db_query", nil, h)
	if err != nil {
		t.Fatalf("handleRequest error = %v", err)
	}
	if result.IsError {
		t.Fatal("expected success result")
	}
	env, ok := result.StructuredContent.(contract.SuccessEnvelope)
	if !ok {
		t.Fatalf("StructuredContent type = %T, want SuccessEnvelope", result.StructuredContent)
	}
	if env.SchemaVersion != contract.SchemaVersion {
		t.Fatalf("schema_version = %q, want %q", env.SchemaVersion, contract.SchemaVersion)
	}
	if env.Meta.DurationMS < 1 {
		t.Fatalf("duration_ms = %d, want measured duration", env.Meta.DurationMS)
	}
	if !env.Meta.Truncated || env.Meta.TruncationReason == nil || *env.Meta.TruncationReason != "rows" {
		t.Fatalf("meta truncation = %+v, want rows", env.Meta)
	}
	if len(result.Content) != 0 {
		t.Fatalf("Content len = %d, want 0 for structured-only success", len(result.Content))
	}
}

func TestSuccessMetaPropagatesSampleTruncation(t *testing.T) {
	reason := "payload"
	meta := successMeta(map[string]any{
		"result": contract.QueryResult{Truncated: true, TruncationReason: &reason},
	}, 7)
	if meta.DurationMS != 7 || !meta.Truncated || meta.TruncationReason == nil || *meta.TruncationReason != reason {
		t.Fatalf("successMeta() = %+v", meta)
	}
}

func TestHandleRequestError(t *testing.T) {
	h := func(ctx context.Context, name string, args json.RawMessage) (any, error) {
		return nil, &contract.PublicError{Code: contract.CodeQueryRejected, Message: "not allowed"}
	}
	result, err := handleRequest(context.Background(), "db_query", nil, h)
	if err != nil {
		t.Fatalf("handleRequest error = %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result")
	}
	root, ok := result.StructuredContent.(contract.ErrorEnvelopeRoot)
	if !ok {
		t.Fatalf("StructuredContent type = %T, want ErrorEnvelopeRoot", result.StructuredContent)
	}
	if root.Error.Code != contract.CodeQueryRejected {
		t.Fatalf("code = %q, want QUERY_REJECTED", root.Error.Code)
	}
	if root.Error.Retryable {
		t.Fatal("QUERY_REJECTED must not be retryable")
	}
}

func TestNotImplementedReturnsInternalError(t *testing.T) {
	_, err := notImplemented(context.Background(), "db_query", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	pe := contract.AsPublicError(err)
	if pe.Code != contract.CodeInternalError {
		t.Fatalf("code = %q, want INTERNAL_ERROR", pe.Code)
	}
}

func TestServerRegistersAllTools(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	// AddTool panics if a schema is invalid; if no tool fails, all 14 registered.
	s := New(&mcp.Implementation{Name: "test", Version: "0"}, notImplemented, logger)
	if s == nil {
		t.Fatal("New returned nil server")
	}
	if len(contract.Tools()) != 14 {
		t.Fatalf("registered %d tools, want 14", len(contract.Tools()))
	}
}
