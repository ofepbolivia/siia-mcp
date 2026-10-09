package manager

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/siia/siia-mcp/internal/admission"
	"github.com/siia/siia-mcp/internal/audit"
	"github.com/siia/siia-mcp/internal/config"
	"github.com/siia/siia-mcp/internal/connection"
	"github.com/siia/siia-mcp/internal/contract"
	"github.com/siia/siia-mcp/internal/engine"
	"github.com/siia/siia-mcp/internal/requestctx"
)

func testConfig() *config.Config {
	return &config.Config{
		Version: 1,
		Server: config.ServerConfig{
			RequestTimeout: config.MinRequestTimeout,
		},
		MetadataCache: config.CacheConfig{
			TTL:        time.Hour,
			MaxEntries: 10,
			MaxBytes:   1 << 20,
		},
		Audit: config.AuditConfig{
			Sink:         "file",
			Path:         filepath.Join(os.TempDir(), "siia-manager-test-audit.log"),
			WriteTimeout: time.Second,
			QueueSize:    64,
			MaxFileBytes: 1 << 20,
			MaxFiles:     3,
		},
		Connection: &config.Connection{
			Name:       "app_dev",
			Engine:     "postgres",
			Mode:       "readonly",
			Initialize: "lazy",
			Host:       "localhost",
			Port:       5432,
			Database:   "app",
			User:       "reader",
			Password:   "secret",
			TLS:        config.TLSConfig{Mode: "disable"},
			Pool: config.PoolConfig{
				MaxConnections:        4,
				MaxConnectionLifetime: 30 * time.Minute,
				MaxConnectionIdleTime: 5 * time.Minute,
				HealthCheckPeriod:     30 * time.Second,
			},
			Allow: &config.Allowlist{Schemas: []string{"public"}},
		},
	}
}

func TestDBListConnectionsNoCredentials(t *testing.T) {
	m, err := New(testConfig(), slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := m.HandleTool(context.Background(), "db_list_connections", nil)
	if err != nil {
		t.Fatalf("db_list_connections error = %v", err)
	}
	items, ok := out.([]contract.ConnectionItem)
	if !ok {
		t.Fatalf("type = %T", out)
	}
	if len(items) != 1 {
		t.Fatalf("len = %d, want 1", len(items))
	}
	if items[0].Name != "app_dev" {
		t.Fatalf("name = %q", items[0].Name)
	}
	if items[0].Required {
		t.Fatal("connection should not be required")
	}
}

// fakeEngine records engine calls while satisfying engine.Connection by
// embedding the interface (panics on any untouched method).
type fakeEngine struct {
	engine.Connection
	listTablesCalls int
	listTablesErr   error
}

func (f *fakeEngine) ListTables(ctx context.Context, req engine.ListTablesRequest) (contract.Page[contract.TableItem], error) {
	f.listTablesCalls++
	if f.listTablesErr != nil {
		return contract.Page[contract.TableItem]{}, f.listTablesErr
	}
	return contract.Page[contract.TableItem]{
		Items: []contract.TableItem{{Schema: "public", Name: "books", Kind: "table"}},
	}, nil
}

func (f *fakeEngine) Close(context.Context) error { return nil }

func TestMetadataCachePreservesPublicErrors(t *testing.T) {
	cfg := testConfig()
	cfg.Audit.Path = filepath.Join(t.TempDir(), "audit.jsonl")
	m, err := New(cfg, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeEngine{listTablesErr: &contract.PublicError{Code: contract.CodeInvalidCursor, Message: "invalid cursor"}}
	m.newEngine = func(context.Context, string, *connection.Handle) (engine.Connection, error) { return fake, nil }
	t.Cleanup(func() { m.Close(context.Background()) })

	args := json.RawMessage(`{"connection":"app_dev","cursor":"bad"}`)
	_, err = m.HandleTool(context.Background(), "db_list_tables", args)
	pe, ok := err.(*contract.PublicError)
	if !ok || pe.Code != contract.CodeInvalidCursor {
		t.Fatalf("error = %#v, want INVALID_CURSOR", err)
	}
}

func TestMetadataCacheRefreshesOnce(t *testing.T) {
	m, err := New(testConfig(), slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	fake := &fakeEngine{}
	m.newEngine = func(ctx context.Context, name string, h *connection.Handle) (engine.Connection, error) {
		return fake, nil
	}
	args, _ := json.Marshal(contract.ListTablesInput{Connection: "app_dev"})
	for i := 0; i < 3; i++ {
		if _, err := m.HandleTool(context.Background(), "db_list_tables", args); err != nil {
			t.Fatalf("db_list_tables call %d = %v", i, err)
		}
	}
	if fake.listTablesCalls != 1 {
		t.Fatalf("ListTables engine calls = %d, want 1 (cached)", fake.listTablesCalls)
	}
}

// TestHandleToolBusyRejected verifies the §17.1 gate: a request that cannot
// acquire an admission token is rejected with SERVER_BUSY before any handler
// work runs.
func TestHandleToolBusyRejected(t *testing.T) {
	m, err := New(testConfig(), slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Control the gate directly: active=1/queued=0 => a single occupied token
	// saturates ingress, so the next call must be rejected.
	m.gate = admission.New(1, 0)
	if err := m.gate.Enter(context.Background()); err != nil {
		t.Fatalf("gate.Enter: %v", err)
	}

	_, err = m.HandleTool(context.Background(), "db_list_connections", nil)
	pe, ok := err.(*contract.PublicError)
	if !ok || pe.Code != contract.CodeServerBusy {
		t.Fatalf("expected SERVER_BUSY public error, got %#v", err)
	}

	// Once the occupied slot is released, the same tool is admitted again.
	m.gate.Leave()
	if _, err := m.HandleTool(context.Background(), "db_list_connections", nil); err != nil {
		t.Fatalf("db_list_connections after release = %v", err)
	}
}

func TestEffectiveRequestTimeoutUsesStrictestCeiling(t *testing.T) {
	cfg := testConfig()
	cfg.Server.RequestTimeout = 10 * time.Second
	cfg.Connection.Limits = &config.ConnectionLimits{RequestTimeout: 8 * time.Second}
	m := &Manager{cfg: cfg, byName: map[string]config.Connection{"app_dev": *cfg.Connection}}

	tests := []struct {
		name    string
		tool    string
		args    string
		want    time.Duration
		wantErr bool
	}{
		{name: "connection ceiling", tool: "db_ping", args: `{"connection":"app_dev"}`, want: 8 * time.Second},
		{name: "top level reduction", tool: "db_ping", args: `{"connection":"app_dev","timeout_ms":5000}`, want: 5 * time.Second},
		{name: "nested reduction", tool: "db_query", args: `{"connection":"app_dev","limits":{"timeout_ms":6000}}`, want: 6 * time.Second},
		{name: "cannot raise", tool: "db_query", args: `{"connection":"app_dev","limits":{"timeout_ms":9000}}`, want: 8 * time.Second, wantErr: true},
		{name: "cannot go below minimum", tool: "db_ping", args: `{"connection":"app_dev","timeout_ms":4999}`, want: 8 * time.Second, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := m.effectiveRequestTimeout(tt.tool, "app_dev", json.RawMessage(tt.args))
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("timeout = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestRequestContextsReserveCleanupAndAudit(t *testing.T) {
	cfg := testConfig()
	cfg.Audit.WriteTimeout = time.Second
	m := &Manager{cfg: cfg}
	total, work, cancel := m.requestContexts(context.Background(), config.MinRequestTimeout)
	defer cancel()

	totalDeadline, totalOK := total.Deadline()
	workDeadline, workOK := work.Deadline()
	if !totalOK || !workOK {
		t.Fatal("request contexts must have deadlines")
	}
	wantGap := requestctx.MaxCleanupDuration + cfg.Audit.WriteTimeout
	if got := totalDeadline.Sub(workDeadline); got != wantGap {
		t.Fatalf("reserved duration = %s, want %s", got, wantGap)
	}
	cleanup, cancelCleanup := requestctx.CleanupContext(work)
	defer cancelCleanup()
	cleanupDeadline, ok := cleanup.Deadline()
	if !ok || cleanupDeadline.After(totalDeadline.Add(-cfg.Audit.WriteTimeout)) {
		t.Fatalf("cleanup deadline = %v, total deadline = %v", cleanupDeadline, totalDeadline)
	}
}

func TestHandleToolRejectsUnknownFields(t *testing.T) {
	cfg := testConfig()
	cfg.Audit.Path = filepath.Join(t.TempDir(), "audit.jsonl")
	m, err := New(cfg, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { m.Close(context.Background()) })

	args := json.RawMessage(`{"connection":"app_dev","unexpected":true}`)
	_, err = m.HandleTool(context.Background(), "db_ping", args)
	pe, ok := err.(*contract.PublicError)
	if !ok || pe.Code != contract.CodeInvalidRequest {
		t.Fatalf("error = %#v, want INVALID_REQUEST", err)
	}
}

func TestQueueTimeoutBoundsOnlyAdmissionWait(t *testing.T) {
	cfg := testConfig()
	cfg.Server.QueueTimeout = 20 * time.Millisecond
	cfg.Audit.Path = filepath.Join(t.TempDir(), "audit.jsonl")
	m, err := New(cfg, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.gate = admission.New(1, 1)
	if err := m.gate.Enter(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		m.gate.Leave()
		m.Close(context.Background())
	})

	start := time.Now()
	_, err = m.HandleTool(context.Background(), "db_list_connections", nil)
	if elapsed := time.Since(start); elapsed < cfg.Server.QueueTimeout || elapsed > 500*time.Millisecond {
		t.Fatalf("queue wait = %s, want bounded near %s", elapsed, cfg.Server.QueueTimeout)
	}
	pe, ok := err.(*contract.PublicError)
	if !ok || pe.Code != contract.CodeServerBusy {
		t.Fatalf("error = %#v, want SERVER_BUSY", err)
	}
}

func TestEffectiveResponseLimitRejectsRaises(t *testing.T) {
	cfg := testConfig()
	cfg.Limits.ResponseBytes = 1000
	m := &Manager{cfg: cfg, byName: map[string]config.Connection{"app_dev": *cfg.Connection}}
	limit, err := m.effectiveResponseLimit("db_query", "app_dev", json.RawMessage(`{"limits":{"max_response_bytes":1001}}`))
	if limit != 1000 {
		t.Fatalf("limit = %d, want 1000", limit)
	}
	pe, ok := err.(*contract.PublicError)
	if !ok || pe.Code != contract.CodeInvalidRequest {
		t.Fatalf("error = %#v, want INVALID_REQUEST", err)
	}
}

func TestHandleToolEnforcesFinalResponseLimit(t *testing.T) {
	cfg := testConfig()
	cfg.Limits.ResponseBytes = 1
	cfg.Audit.Path = filepath.Join(t.TempDir(), "audit.jsonl")
	m, err := New(cfg, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close(context.Background()) })
	_, err = m.HandleTool(context.Background(), "db_list_connections", nil)
	pe, ok := err.(*contract.PublicError)
	if !ok || pe.Code != contract.CodeResultValueTooLarge {
		t.Fatalf("error = %#v, want RESULT_VALUE_TOO_LARGE", err)
	}
}

func TestMetadataRefreshUsesConnectionTimeout(t *testing.T) {
	cfg := testConfig()
	cfg.Connection.Limits = &config.ConnectionLimits{RequestTimeout: 20 * time.Millisecond}
	m := &Manager{byName: map[string]config.Connection{"app_dev": *cfg.Connection}}
	refresh := m.metadataRefresh("app_dev", func(ctx context.Context) (any, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	start := time.Now()
	_, err := refresh(context.Background())
	if err != context.DeadlineExceeded {
		t.Fatalf("refresh error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(start); elapsed < 20*time.Millisecond || elapsed > 500*time.Millisecond {
		t.Fatalf("refresh duration = %s", elapsed)
	}
}

func TestCancelledRequestDoesNotPoisonAudit(t *testing.T) {
	cfg := testConfig()
	cfg.Audit.Path = filepath.Join(t.TempDir(), "audit.jsonl")
	m, err := New(cfg, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close(context.Background()) })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m.auditEvent(ctx, "request", audit.PhaseOutcome, "db_query", "app_dev", "error", string(contract.CodeCancelled), 1)
	select {
	case <-m.Fatal():
		t.Fatal("ordinary request cancellation poisoned the audit sink")
	default:
	}
}

func TestHandleToolAuditsCancelledAdmission(t *testing.T) {
	cfg := testConfig()
	cfg.Audit.Path = filepath.Join(t.TempDir(), "audit.jsonl")
	m, err := New(cfg, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close(context.Background()) })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = m.HandleTool(ctx, "db_list_connections", nil)
	pe, ok := err.(*contract.PublicError)
	if !ok || pe.Code != contract.CodeCancelled {
		t.Fatalf("error = %#v, want CANCELLED", err)
	}
	select {
	case <-m.Fatal():
		t.Fatal("auditing a cancelled admission triggered fail-closed")
	default:
	}
}

func TestAuditEventFailureTriggersFailClosed(t *testing.T) {
	cfg := testConfig()
	cfg.Audit.Path = filepath.Join(t.TempDir(), "audit.jsonl")
	m, err := New(cfg, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.audit.Close(); err != nil {
		t.Fatal(err)
	}

	err = m.auditEvent(context.Background(), "request", audit.PhaseAttempt, "db_query", "app_dev", "", "", 0)
	if err == nil {
		t.Fatal("auditEvent error = nil, want sink failure")
	}
	if !m.auditFailed.Load() {
		t.Fatal("manager did not retain fail-closed state")
	}
	select {
	case <-m.Fatal():
	default:
		t.Fatal("audit failure did not signal fatal shutdown")
	}
}
