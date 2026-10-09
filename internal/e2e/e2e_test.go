//go:build e2e

// Package e2e verifies the compiled siiasql binary over stdio as a real MCP
// subprocess (SDD §20.4). It exercises the protocol boundary, tool surface,
// envelope shape, stdout purity, stderr/audit separation, cancellation, and
// graceful shutdown without requiring a live PostgreSQL.
package e2e

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	startupTimeout = 10 * time.Second
	shutdownGrace  = 5 * time.Second
)

// e2eConfig writes a valid SIIASQL YAML config whose only connection is
// optional, lazy, and points at a guaranteed-closed port (59999). The process
// boots with no live database and any lazy init attempt fails deterministically
// with CONNECTION_UNAVAILABLE. Audit is written to a temp file; logs go to
// stderr.
func e2eConfig(t *testing.T) (configPath, auditPath string) {
	t.Helper()
	dir := t.TempDir()
	code := fmt.Sprintf(`
version: 1
server:
  request_timeout: 30s
  shutdown_timeout: 5s
  active_requests: 8
  queued_requests: 16
  queue_timeout: 2s
  max_total_pool_connections: 8
limits:
  mcp_frame_bytes: 8388608
  json_nesting_depth: 64
  query_bytes: 65536
  parameter_count: 128
  parameter_bytes: 1048576
  parameters_total_bytes: 4194304
  query_rows: 1000
  sample_rows: 100
  value_bytes: 1048576
  response_bytes: 8388608
  metadata_page_size: 500
metadata_cache:
  ttl: 60s
  max_entries: 1000
  max_bytes: 8388608
logging:
  level: debug
  sink: stderr
audit:
  sink: file
  path: %s
  write_timeout: 1s
  queue_size: 1024
  max_file_bytes: 10485760
  max_files: 4
connection:
  name: it
  engine: postgres
  mode: readonly
  required: false
  initialize: lazy
  host: 127.0.0.1
  port: 59999
  database: itdb
  user: ituser
  password: itpass
  tls:
    mode: disable
  pool:
    max_connections: 2
    min_connections: 0
    max_connection_lifetime: 30m
    max_connection_idle_time: 5m
    health_check_period: 30s
  allow:
    schemas:
      - public
`, filepath.Join(dir, "audit.jsonl"))
	configPath = filepath.Join(dir, "siia.yaml")
	if err := os.WriteFile(configPath, []byte(code), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return configPath, filepath.Join(dir, "audit.jsonl")
}

// mustConfig returns just the config path from e2eConfig.
func mustConfig(t *testing.T) string {
	t.Helper()
	configPath, _ := e2eConfig(t)
	return configPath
}

// binaryPath locates the compiled siiasql binary, building it from source on
// demand so the E2E test always exercises current code.
func binaryPath(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "siiasql")
	cmd := exec.Command("go", "build", "-o", bin, "github.com/siia/siia-mcp/cmd/siiasql")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build siiasql: %v\n%s", err, out)
	}
	return bin
}

// server is a running siiasql subprocess talking stdio.
type server struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	// scanErr and taskErr surface subprocess-side failures for assertion.
	stderr *syncedBuffer
}

// syncedBuffer captures stderr for inspection without data races.
type syncedBuffer struct {
	mu sync.Mutex
	b  *strings.Builder
}

func (s *syncedBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncedBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// start launches the binary and returns the subprocess with open pipes.
func start(t *testing.T, configPath string) (*server, *exec.Cmd, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, binaryPath(t), "--config", configPath)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		t.Fatalf("stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		t.Fatalf("stdout pipe: %v", err)
	}
	var stderr syncedBuffer
	stderr.b = &strings.Builder{}
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("start server: %v", err)
	}
	s := &server{cmd: cmd, stdin: stdin, stdout: stdout, stderr: &stderr}
	return s, cmd, cancel
}

// connect dials the MCP protocol over the subprocess pipes.
func connect(t *testing.T, s *server) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "e2e", Version: "0"}, &mcp.ClientOptions{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	transport := &mcp.IOTransport{
		Reader: s.stdout,
		Writer: s.stdin,
	}
	ctx, cancel := context.WithTimeout(context.Background(), startupTimeout)
	defer cancel()
	cs, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("MCP connect: %v", err)
	}
	return cs
}

func toolCall(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), startupTimeout)
	defer cancel()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	if res == nil {
		t.Fatalf("call %s: nil result", name)
	}
	return res
}

// envelope decodes the structured content of a result into a generic map.
func envelope(t *testing.T, res *mcp.CallToolResult) map[string]any {
	t.Helper()
	raw, ok := res.StructuredContent.(json.RawMessage)
	if !ok {
		data, err := json.Marshal(res.StructuredContent)
		if err != nil {
			t.Fatalf("marshal structured content: %v", err)
		}
		raw = data
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("decode structured content: %v\n%s", err, raw)
	}
	return m
}

func TestListsFourteenTools(t *testing.T) {
	s, _, cancel := start(t, mustConfig(t))
	defer cancel()
	defer s.stdin.Close()

	cs := connect(t, s)
	defer cs.Close()

	ctx, cancelList := context.WithTimeout(context.Background(), startupTimeout)
	defer cancelList()
	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if got := len(res.Tools); got != 14 {
		t.Fatalf("tool count = %d, want 14", got)
	}
	seen := map[string]bool{}
	for _, tool := range res.Tools {
		if tool.Name == "" || tool.InputSchema == nil {
			t.Fatalf("tool %q missing name or schema", tool.Name)
		}
		seen[tool.Name] = true
	}
	for _, name := range []string{
		"db_list_connections", "db_ping", "db_list_schemas", "db_list_tables",
		"db_describe_table", "db_list_indexes", "db_list_relationships",
		"db_suggest_relationships", "db_search_columns", "db_find_references",
		"db_sample_rows", "db_count_rows", "db_query", "db_explain",
	} {
		if !seen[name] {
			t.Errorf("missing tool %q", name)
		}
	}
}

func TestListConnectionsSuccessEnvelope(t *testing.T) {
	s, _, cancel := start(t, mustConfig(t))
	defer cancel()
	defer s.stdin.Close()

	cs := connect(t, s)
	defer cs.Close()

	res := toolCall(t, cs, "db_list_connections", nil)
	if res.IsError {
		t.Fatalf("db_list_connections unexpected error: %s", res.Content)
	}
	m := envelope(t, res)
	if m["schema_version"] != "1" {
		t.Fatalf("schema_version = %v, want 1", m["schema_version"])
	}
	data, ok := m["data"].([]any)
	if !ok {
		t.Fatalf("data type = %T, want []any", m["data"])
	}
	if len(data) != 1 {
		t.Fatalf("connection count = %d, want 1", len(data))
	}
	item, ok := data[0].(map[string]any)
	if !ok || item["name"] != "it" || item["state"] != "uninitialized" || item["required"] != false {
		t.Fatalf("connection item = %v", data[0])
	}
	// OpenCode exposes structuredContent when Content is empty. A summary such
	// as "OK." would hide the connection identifiers from the model.
	if len(res.Content) != 0 {
		t.Fatalf("Content len = %d, want structured-only result", len(res.Content))
	}
}

func TestUninitializedOptionalConnectionYieldsStableError(t *testing.T) {
	s, _, cancel := start(t, mustConfig(t))
	defer cancel()
	defer s.stdin.Close()

	cs := connect(t, s)
	defer cs.Close()

	res := toolCall(t, cs, "db_ping", map[string]any{"connection": "it"})
	if !res.IsError {
		t.Fatalf("expected error for uninitialized optional connection")
	}
	m := envelope(t, res)
	code, ok := m["error"].(map[string]any)["code"].(string)
	if !ok {
		t.Fatalf("missing error.code in envelope: %v", m)
	}
	if code == "" || strings.Contains(code, "INTERNAL") {
		t.Fatalf("error code should be stable and public, got %q", code)
	}
	// Same input, stable output: retry yields the identical code.
	res2 := toolCall(t, cs, "db_ping", map[string]any{"connection": "it"})
	if !res2.IsError {
		t.Fatalf("expected stable error on retry")
	}
	m2 := envelope(t, res2)
	code2 := m2["error"].(map[string]any)["code"].(string)
	if code2 != code {
		t.Fatalf("retry code changed: %q != %q", code2, code)
	}
}

func TestUnknownToolRejectedStably(t *testing.T) {
	s, _, cancel := start(t, mustConfig(t))
	defer cancel()
	defer s.stdin.Close()

	cs := connect(t, s)
	defer cs.Close()

	// The SDK surfaces an unknown tool as a transport-level JSON-RPC error, not
	// a CallToolResult. Assert it is a stable, non-panicking rejection.
	ctx, ct := context.WithTimeout(context.Background(), startupTimeout)
	defer ct()
	_, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "db_drop", Arguments: map[string]any{"connection": "it"}})
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}

	// Retry must yield the same class of rejection without wedging the server.
	_, err2 := cs.CallTool(ctx, &mcp.CallToolParams{Name: "db_drop", Arguments: nil})
	if err2 == nil {
		t.Fatal("expected stable rejection on retry")
	}
}

func TestStdioAndAuditSeparation(t *testing.T) {
	configPath, auditPath := e2eConfig(t)
	s, cmd, cancel := start(t, configPath)
	defer cancel()
	defer s.stdin.Close()

	cs := connect(t, s)

	// One tool call so audit writes at least one event pair.
	res := toolCall(t, cs, "db_list_connections", nil)
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if err := cs.Close(); err != nil {
		t.Fatalf("close session: %v", err)
	}
	// Closing stdin lets the transport observe EOF and the process exit.
	_ = s.stdin.Close()

	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	select {
	case err := <-waited:
		if err != nil {
			t.Fatalf("server exited nonzero: %v\nstderr:\n%s", err, s.stderr.String())
		}
	case <-time.After(shutdownGrace):
		_ = cmd.Process.Kill()
		t.Fatal("server did not exit cleanly on EOF")
	}

	// Logs land exclusively on stderr; metric lines prove the summary ran.
	stderrText := s.stderr.String()
	if !strings.Contains(stderrText, "metric") {
		t.Errorf("expected metrics summary on stderr, got:\n%s", stderrText)
	}
	if strings.Contains(stderrText, "\x00") || hasFrameNoise(stderrText) {
		t.Errorf("stderr must not contain protocol frame noise:\n%s", stderrText)
	}

	raw, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("audit file is empty; expected at least one event")
	}
	// Sanitization: audit never contains credentials, DSNs, or DB fields.
	sc := string(raw)
	for _, secret := range []string{"itpass", "itdb", "ituser", "password"} {
		if strings.Contains(sc, secret) {
			t.Errorf("audit leaked secret %q", secret)
		}
	}
}

// TestCancellationPropagates assumes a live database is not required; it
// verifies the client can cancel an in-flight tool call without wedging the
// server, then a subsequent call still succeeds.
func TestCancellationDoesNotWedgeServer(t *testing.T) {
	s, _, cancel := start(t, mustConfig(t))
	defer cancel()
	defer s.stdin.Close()

	cs := connect(t, s)
	defer cs.Close()

	ctx, c := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = cs.CallTool(ctx, &mcp.CallToolParams{Name: "db_list_connections", Arguments: nil})
	}()
	<-time.After(50 * time.Millisecond)
	c()
	<-done

	// Server remains responsive after cancellation.
	res := toolCall(t, cs, "db_list_connections", nil)
	if res.IsError {
		t.Fatalf("server wedged after cancellation: %s", res.Content)
	}
}

func TestInvalidRequestRejected(t *testing.T) {
	s, _, cancel := start(t, mustConfig(t))
	defer cancel()
	defer s.stdin.Close()

	cs := connect(t, s)
	defer cs.Close()

	// Missing required connection for a ping.
	res := toolCall(t, cs, "db_ping", map[string]any{})
	if !res.IsError {
		t.Fatal("expected error for missing connection")
	}
	m := envelope(t, res)
	code, _ := m["error"].(map[string]any)["code"].(string)
	if code == "" {
		t.Fatalf("missing error code: %v", m)
	}
}

func hasFrameNoise(s string) bool {
	scanner := bufio.NewScanner(strings.NewReader(s))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" && (strings.HasPrefix(line, `{"`) || strings.HasPrefix(line, "Content-Length")) {
			return true
		}
	}
	return false
}
