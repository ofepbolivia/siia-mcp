package mcpserver

import (
	"context"
	"encoding/json"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/siia/siia-mcp/internal/contract"
)

// handleRequest routes a tool call to the registered handler and builds the
// MCP CallToolResult manually. Successful structured-only results leave
// Content empty so clients do not hide the structured payload behind a
// non-informative text summary (SDD §11.3/§11.4).
func handleRequest(ctx context.Context, name string, args json.RawMessage, handle ToolHandler) (*mcp.CallToolResult, error) {
	start := time.Now()
	data, err := handle(ctx, name, args)
	if err != nil {
		pe := contract.AsPublicError(err)
		envelope := contract.ErrorEnvelopeRoot{
			SchemaVersion: contract.SchemaVersion,
			Error:         pe.PublicErrorEnvelope(),
		}
		content := []mcp.Content{&mcp.TextContent{Text: pe.Message}}
		return &mcp.CallToolResult{
			Content:           content,
			IsError:           true,
			StructuredContent: envelope,
		}, nil
	}

	envelope := contract.SuccessEnvelope{
		SchemaVersion: contract.SchemaVersion,
		Data:          data,
		Meta:          successMeta(data, time.Since(start).Milliseconds()),
	}
	return &mcp.CallToolResult{
		Content:           []mcp.Content{},
		StructuredContent: envelope,
	}, nil
}

func successMeta(data any, durationMS int64) contract.SuccessMeta {
	meta := contract.SuccessMeta{DurationMS: durationMS}
	switch result := data.(type) {
	case contract.QueryResult:
		meta.Truncated = result.Truncated
		meta.TruncationReason = result.TruncationReason
	case *contract.QueryResult:
		if result != nil {
			meta.Truncated = result.Truncated
			meta.TruncationReason = result.TruncationReason
		}
	case map[string]any:
		if sample, ok := result["result"].(contract.QueryResult); ok {
			meta.Truncated = sample.Truncated
			meta.TruncationReason = sample.TruncationReason
		}
	}
	return meta
}
