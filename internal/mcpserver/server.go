// Package mcpserver wires the MCP Go SDK over stdio and registers the frozen
// v1 tool set. It keeps stdout exclusively for MCP protocol frames.
package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/siia/siia-mcp/internal/contract"
)

// ToolHandler executes one tool given its raw JSON arguments and returns the
// opaque data payload for the success envelope, or a public error.
type ToolHandler func(ctx context.Context, name string, args json.RawMessage) (any, error)

// Server is a stdio MCP server exposing the frozen tool set.
type Server struct {
	sdk      *mcp.Server
	handlers map[string]ToolHandler
	logger   *slog.Logger
}

// New builds an MCP server over stdio with the eleven v1 tools registered.
// handle should route by tool name; it may be nil until adapters exist.
func New(impl *mcp.Implementation, handle ToolHandler, logger *slog.Logger) *Server {
	if handle == nil {
		handle = notImplemented
	}
	s := &Server{
		sdk:      mcp.NewServer(impl, nil),
		handlers: map[string]ToolHandler{},
		logger:   logger,
	}
	for _, t := range contract.Tools() {
		name := t.Name
		tool := &mcp.Tool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
			Annotations: &mcp.ToolAnnotations{
				ReadOnlyHint:    true,
				DestructiveHint: boolPtr(false),
			},
		}
		s.sdk.AddTool(tool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var args json.RawMessage
			if req != nil && req.Params != nil {
				args = req.Params.Arguments
			}
			return handleRequest(ctx, name, args, handle)
		})
	}
	return s
}

// Run serves the MCP protocol until the client disconnects.
func (s *Server) Run(ctx context.Context, maxFrameBytes, maxNestingDepth int) error {
	transport, err := NewBoundedStdioTransport(maxFrameBytes, maxNestingDepth)
	if err != nil {
		return err
	}
	return s.sdk.Run(ctx, transport)
}

// notImplemented is the safe default handler before adapters are wired.
func notImplemented(ctx context.Context, name string, args json.RawMessage) (any, error) {
	return nil, &contract.PublicError{Code: contract.CodeInternalError, Message: fmt.Sprintf("tool %q is not implemented", name)}
}

func boolPtr(b bool) *bool { return &b }
