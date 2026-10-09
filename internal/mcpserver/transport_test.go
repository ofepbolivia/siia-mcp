package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestBoundedTransportRejectsInvalidLimits(t *testing.T) {
	tests := []struct {
		name       string
		frameBytes int
		depth      int
	}{
		{name: "zero frame bytes", frameBytes: 0, depth: 1},
		{name: "negative frame bytes", frameBytes: -1, depth: 1},
		{name: "zero nesting depth", frameBytes: 1, depth: 0},
		{name: "negative nesting depth", frameBytes: 1, depth: -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := newBoundedTransport(io.NopCloser(strings.NewReader("")), io.Discard, tt.frameBytes, tt.depth); err == nil {
				t.Fatal("newBoundedTransport() error = nil, want validation error")
			}
		})
	}
}

func TestBoundedConnectionReadsNDJSON(t *testing.T) {
	frames := []string{
		`{"jsonrpc":"2.0","method":"first","params":{"value":"escaped quote: \" and brackets: [{]} and slash: \\"}}`,
		`{"jsonrpc":"2.0","method":"second"}`,
	}
	input := strings.Join(frames, "\r\n") + "\r\n"
	conn := connectTestTransport(t, input, len(input), 2)

	for _, wantMethod := range []string{"first", "second"} {
		message, err := conn.Read(context.Background())
		if err != nil {
			t.Fatalf("Read() error = %v", err)
		}
		request, ok := message.(*jsonrpc.Request)
		if !ok {
			t.Fatalf("Read() message type = %T, want *jsonrpc.Request", message)
		}
		if request.Method != wantMethod {
			t.Fatalf("Read() method = %q, want %q", request.Method, wantMethod)
		}
	}
}

func TestBoundedConnectionEnforcesFrameBytes(t *testing.T) {
	frame := `{"jsonrpc":"2.0","method":"test"}`
	tests := []struct {
		name      string
		limit     int
		wantError bool
	}{
		{name: "exact limit accepted", limit: len(frame)},
		{name: "one byte over limit rejected", limit: len(frame) - 1, wantError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn := connectTestTransport(t, frame+"\n", tt.limit, 2)
			_, err := conn.Read(context.Background())
			if tt.wantError && !errors.Is(err, ErrMCPFrameTooLarge) {
				t.Fatalf("Read() error = %v, want ErrMCPFrameTooLarge", err)
			}
			if !tt.wantError && err != nil {
				t.Fatalf("Read() error = %v, want nil", err)
			}
		})
	}
}

func TestBoundedConnectionAcceptsExactLimitCRLF(t *testing.T) {
	frame := `{"jsonrpc":"2.0","method":"test"}`
	conn := connectTestTransport(t, frame+"\r\n", len(frame), 2)
	if _, err := conn.Read(context.Background()); err != nil {
		t.Fatalf("Read error = %v, want exact-limit CRLF accepted", err)
	}
}

func TestBoundedConnectionReadsFrameLargerThanScannerLimit(t *testing.T) {
	value := strings.Repeat("x", 96*1024)
	frame := `{"jsonrpc":"2.0","method":"large","params":{"value":"` + value + `"}}`
	conn := connectTestTransport(t, frame+"\n", len(frame), 2)

	message, err := conn.Read(context.Background())
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if request := message.(*jsonrpc.Request); request.Method != "large" {
		t.Fatalf("Read() method = %q, want large", request.Method)
	}
}

func TestBoundedConnectionEnforcesJSONNesting(t *testing.T) {
	tests := []struct {
		name      string
		frame     string
		limit     int
		wantError bool
	}{
		{
			name:  "depth at limit accepted",
			frame: `{"jsonrpc":"2.0","method":"test","params":{"nested":{}}}`,
			limit: 3,
		},
		{
			name:      "object beyond limit rejected",
			frame:     `{"jsonrpc":"2.0","method":"test","params":{"nested":{}}}`,
			limit:     2,
			wantError: true,
		},
		{
			name:      "array beyond limit rejected",
			frame:     `{"jsonrpc":"2.0","method":"test","params":[[[]]]}`,
			limit:     3,
			wantError: true,
		},
		{
			name:  "delimiters in escaped string ignored",
			frame: `{"jsonrpc":"2.0","method":"test","params":{"value":"\\\"[[{{}}]]"}}`,
			limit: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn := connectTestTransport(t, tt.frame+"\n", len(tt.frame), tt.limit)
			_, err := conn.Read(context.Background())
			if tt.wantError && !errors.Is(err, ErrJSONNestingTooDeep) {
				t.Fatalf("Read() error = %v, want ErrJSONNestingTooDeep", err)
			}
			if !tt.wantError && err != nil {
				t.Fatalf("Read() error = %v, want nil", err)
			}
		})
	}
}

func TestBoundedConnectionConcurrentWritesAreWholeFrames(t *testing.T) {
	var output bytes.Buffer
	transport, err := newBoundedTransport(io.NopCloser(strings.NewReader("")), &output, 1024, 8)
	if err != nil {
		t.Fatalf("newBoundedTransport() error = %v", err)
	}
	conn, err := transport.Connect(context.Background())
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	const writes = 50
	var wg sync.WaitGroup
	for i := 0; i < writes; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			params, _ := json.Marshal(map[string]int{"sequence": i})
			message := &jsonrpc.Request{Method: "notification", Params: params}
			if err := conn.Write(context.Background(), message); err != nil {
				t.Errorf("Write() error = %v", err)
			}
		}(i)
	}
	wg.Wait()

	lines := bytes.Split(bytes.TrimSuffix(output.Bytes(), []byte{'\n'}), []byte{'\n'})
	if len(lines) != writes {
		t.Fatalf("written frame count = %d, want %d", len(lines), writes)
	}
	for i, line := range lines {
		if _, err := jsonrpc.DecodeMessage(line); err != nil {
			t.Fatalf("frame %d is not a complete JSON-RPC message: %v", i, err)
		}
	}
}

func TestBoundedConnectionCloseUnblocksRead(t *testing.T) {
	reader := newBlockingReadCloser()
	transport, err := newBoundedTransport(reader, io.Discard, 1024, 8)
	if err != nil {
		t.Fatalf("newBoundedTransport() error = %v", err)
	}
	conn, err := transport.Connect(context.Background())
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	<-reader.started

	readResult := make(chan error, 1)
	go func() {
		_, err := conn.Read(context.Background())
		readResult <- err
	}()
	if err := conn.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	select {
	case err := <-readResult:
		if !errors.Is(err, io.EOF) {
			t.Fatalf("Read() error after Close = %v, want io.EOF", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close() did not unblock Read()")
	}
	if err := conn.Write(context.Background(), &jsonrpc.Request{Method: "late"}); !errors.Is(err, mcp.ErrConnectionClosed) {
		t.Fatalf("Write() error after Close = %v, want mcp.ErrConnectionClosed", err)
	}
}

func connectTestTransport(t *testing.T, input string, maxFrameBytes, maxDepth int) mcp.Connection {
	t.Helper()
	transport, err := newBoundedTransport(io.NopCloser(strings.NewReader(input)), io.Discard, maxFrameBytes, maxDepth)
	if err != nil {
		t.Fatalf("newBoundedTransport() error = %v", err)
	}
	conn, err := transport.Connect(context.Background())
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

type blockingReadCloser struct {
	started   chan struct{}
	closed    chan struct{}
	startOnce sync.Once
	closeOnce sync.Once
}

func newBlockingReadCloser() *blockingReadCloser {
	return &blockingReadCloser{started: make(chan struct{}), closed: make(chan struct{})}
}

func (r *blockingReadCloser) Read([]byte) (int, error) {
	r.startOnce.Do(func() { close(r.started) })
	<-r.closed
	return 0, io.EOF
}

func (r *blockingReadCloser) Close() error {
	r.closeOnce.Do(func() { close(r.closed) })
	return nil
}
