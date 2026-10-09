package mcpserver

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var (
	// ErrMCPFrameTooLarge indicates that an incoming NDJSON frame exceeded its
	// configured byte limit before JSON decoding.
	ErrMCPFrameTooLarge = errors.New("MCP frame exceeds byte limit")
	// ErrJSONNestingTooDeep indicates that an incoming frame exceeded its
	// configured object/array nesting limit before JSON decoding.
	ErrJSONNestingTooDeep = errors.New("JSON nesting exceeds depth limit")
)

// BoundedStdioTransport is an MCP stdio transport that bounds each incoming
// newline-delimited JSON frame before the SDK decodes it.
type BoundedStdioTransport struct {
	reader          io.ReadCloser
	writer          io.Writer
	maxFrameBytes   int
	maxNestingDepth int

	mu        sync.Mutex
	connected bool
}

// NewBoundedStdioTransport constructs a single-use stdio transport suitable
// for passing directly to mcp.Server.Run.
func NewBoundedStdioTransport(maxFrameBytes, maxNestingDepth int) (*BoundedStdioTransport, error) {
	return newBoundedTransport(os.Stdin, os.Stdout, maxFrameBytes, maxNestingDepth)
}

func newBoundedTransport(reader io.ReadCloser, writer io.Writer, maxFrameBytes, maxNestingDepth int) (*BoundedStdioTransport, error) {
	if reader == nil {
		return nil, errors.New("bounded stdio transport requires a reader")
	}
	if writer == nil {
		return nil, errors.New("bounded stdio transport requires a writer")
	}
	if maxFrameBytes <= 0 {
		return nil, fmt.Errorf("MCP frame byte limit must be positive: %d", maxFrameBytes)
	}
	if maxNestingDepth <= 0 {
		return nil, fmt.Errorf("JSON nesting depth limit must be positive: %d", maxNestingDepth)
	}
	return &BoundedStdioTransport{
		reader:          reader,
		writer:          writer,
		maxFrameBytes:   maxFrameBytes,
		maxNestingDepth: maxNestingDepth,
	}, nil
}

// Connect implements mcp.Transport. A transport instance may be connected
// only once, matching the SDK transport contract.
func (t *BoundedStdioTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.connected {
		return nil, errors.New("bounded stdio transport is already connected")
	}
	t.connected = true
	return newBoundedConnection(t.reader, t.writer, t.maxFrameBytes, t.maxNestingDepth), nil
}

type boundedConnection struct {
	reader io.ReadCloser
	writer io.Writer

	incoming chan messageOrError
	closed   chan struct{}

	writeMu   sync.Mutex
	closeOnce sync.Once
	closeErr  error
}

type messageOrError struct {
	message jsonrpc.Message
	err     error
}

func newBoundedConnection(reader io.ReadCloser, writer io.Writer, maxFrameBytes, maxNestingDepth int) *boundedConnection {
	c := &boundedConnection{
		reader:   reader,
		writer:   writer,
		incoming: make(chan messageOrError),
		closed:   make(chan struct{}),
	}
	go c.readLoop(maxFrameBytes, maxNestingDepth)
	return c
}

func (c *boundedConnection) readLoop(maxFrameBytes, maxNestingDepth int) {
	reader := bufio.NewReaderSize(c.reader, frameBufferSize(maxFrameBytes))
	for {
		frame, err := readFrame(reader, maxFrameBytes)
		if err == nil && len(bytes.TrimSpace(frame)) == 0 {
			continue
		}
		if err == nil {
			err = checkJSONNesting(frame, maxNestingDepth)
		}
		var message jsonrpc.Message
		if err == nil {
			message, err = jsonrpc.DecodeMessage(frame)
		}
		select {
		case c.incoming <- messageOrError{message: message, err: err}:
		case <-c.closed:
			return
		}
		if err != nil {
			return
		}
	}
}

func frameBufferSize(maxFrameBytes int) int {
	const preferred = 32 * 1024
	if maxFrameBytes < preferred {
		return maxFrameBytes + 1
	}
	return preferred
}

// readFrame reads at most maxFrameBytes of payload plus its delimiter. The
// delimiter is not counted as part of the frame limit.
func readFrame(reader *bufio.Reader, maxFrameBytes int) ([]byte, error) {
	frame := make([]byte, 0, min(maxFrameBytes, 32*1024))
	for {
		fragment, err := reader.ReadSlice('\n')
		terminated := len(fragment) > 0 && fragment[len(fragment)-1] == '\n'
		if terminated {
			fragment = fragment[:len(fragment)-1]
		}
		combinedLen := len(frame) + len(fragment)
		endsWithCR := (len(fragment) > 0 && fragment[len(fragment)-1] == '\r') || (len(fragment) == 0 && len(frame) > 0 && frame[len(frame)-1] == '\r')
		crDelimiterCandidate := endsWithCR && combinedLen == maxFrameBytes+1 && (terminated || errors.Is(err, bufio.ErrBufferFull))
		if combinedLen > maxFrameBytes && !crDelimiterCandidate {
			return nil, fmt.Errorf("%w: maximum is %d bytes", ErrMCPFrameTooLarge, maxFrameBytes)
		}
		frame = append(frame, fragment...)
		if terminated && len(frame) > 0 && frame[len(frame)-1] == '\r' {
			frame = frame[:len(frame)-1]
		}

		switch {
		case terminated:
			return frame, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF) && len(frame) > 0:
			return frame, nil
		case err != nil:
			return nil, err
		}
	}
}

func checkJSONNesting(frame []byte, maxDepth int) error {
	depth := 0
	inString := false
	escaped := false
	for _, b := range frame {
		if inString {
			switch {
			case escaped:
				escaped = false
			case b == '\\':
				escaped = true
			case b == '"':
				inString = false
			}
			continue
		}
		switch b {
		case '"':
			inString = true
		case '{', '[':
			depth++
			if depth > maxDepth {
				return fmt.Errorf("%w: maximum is %d", ErrJSONNestingTooDeep, maxDepth)
			}
		case '}', ']':
			if depth > 0 {
				depth--
			}
		}
	}
	return nil
}

// Read implements mcp.Connection.
func (c *boundedConnection) Read(ctx context.Context) (jsonrpc.Message, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.closed:
		return nil, io.EOF
	default:
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.closed:
		return nil, io.EOF
	case result := <-c.incoming:
		return result.message, result.err
	}
}

// Write implements mcp.Connection and serializes concurrent writes so frames
// cannot interleave.
func (c *boundedConnection) Write(ctx context.Context, message jsonrpc.Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	select {
	case <-c.closed:
		return mcp.ErrConnectionClosed
	default:
	}
	data, err := jsonrpc.EncodeMessage(message)
	if err != nil {
		return fmt.Errorf("encoding JSON-RPC message: %w", err)
	}
	data = append(data, '\n')
	if _, err := io.Copy(c.writer, bytes.NewReader(data)); err != nil {
		return err
	}
	return nil
}

// Close implements mcp.Connection. Signaling closed before closing the reader
// guarantees that a blocked Read call returns promptly.
func (c *boundedConnection) Close() error {
	c.closeOnce.Do(func() {
		close(c.closed)
		c.closeErr = c.reader.Close()
	})
	return c.closeErr
}

// SessionID implements mcp.Connection. Stdio connections have no session ID.
func (*boundedConnection) SessionID() string { return "" }

var _ mcp.Transport = (*BoundedStdioTransport)(nil)
var _ mcp.Connection = (*boundedConnection)(nil)
