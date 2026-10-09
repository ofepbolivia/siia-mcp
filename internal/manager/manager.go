// Package manager wires configuration, connection lifecycle, and the engine
// adapters behind the MCP tool set.
package manager

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/siia/siia-mcp/internal/admission"
	"github.com/siia/siia-mcp/internal/audit"
	"github.com/siia/siia-mcp/internal/cache"
	"github.com/siia/siia-mcp/internal/config"
	"github.com/siia/siia-mcp/internal/connection"
	"github.com/siia/siia-mcp/internal/contract"
	"github.com/siia/siia-mcp/internal/engine"
	"github.com/siia/siia-mcp/internal/postgres"
	"github.com/siia/siia-mcp/internal/requestctx"
)

const maxAuditReserve = 2 * time.Second

// Manager owns the logical connection registry and routes tool requests.
type Manager struct {
	cfg           *config.Config
	registry      *connection.Registry
	byName        map[string]config.Connection
	logger        *slog.Logger
	meta          *cache.Cache
	policyDigests map[string]string
	audit         *audit.Queue
	gate          *admission.Gate
	metrics       *metrics
	fatalCh       chan struct{}
	fatalOnce     sync.Once
	auditFailed   atomic.Bool
	// newEngine is a test seam defaulting to the postgres adapter; it may be
	// replaced to drive tool handlers without a live database.
	newEngine func(ctx context.Context, name string, h *connection.Handle) (engine.Connection, error)
}

// New builds a Manager with a handle per configured connection and an audit
// sink. A broken sink path fails startup so the process refuses to run
// without a confirmed audit layer (SDD §18.1).
func New(cfg *config.Config, logger *slog.Logger) (*Manager, error) {
	reg := connection.NewRegistry()
	byName := map[string]config.Connection{}
	if cfg.Connection != nil {
		c := *cfg.Connection
		byName[c.Name] = c
		h := connection.NewHandle(c.Name, c.Required, c.Initialize == "eager")
		if err := reg.Add(h); err != nil {
			// the single connection cannot collide; a duplicate here is a
			// programming error.
			panic(err)
		}
	}
	meta := cache.New(cache.Config{
		TTL:        cfg.MetadataCache.TTL,
		MaxEntries: cfg.MetadataCache.MaxEntries,
		MaxBytes:   cfg.MetadataCache.MaxBytes,
		BudgetTO:   cfg.Server.RequestTimeout,
	})
	digests := map[string]string{}
	for name, c := range byName {
		digests[name] = policyDigest(c)
	}
	aQ, err := audit.NewQueue(audit.Config{
		Path:         cfg.Audit.Path,
		WriteTimeout: cfg.Audit.WriteTimeout,
		QueueSize:    cfg.Audit.QueueSize,
		MaxFileBytes: cfg.Audit.MaxFileBytes,
		MaxFiles:     cfg.Audit.MaxFiles,
	})
	if err != nil {
		return nil, err
	}
	// Sanity defaults for hand-built configs (tests); production config
	// validation guarantees active/queued >= 1 (SDD §17.1).
	active := cfg.Server.ActiveRequests
	if active < 1 {
		active = 8
	}
	queued := cfg.Server.QueuedRequests
	if queued < 1 {
		queued = 8
	}
	return &Manager{
		cfg: cfg, registry: reg, byName: byName, logger: logger,
		meta: meta, policyDigests: digests, audit: aQ,
		gate:    admission.New(active, queued),
		metrics: newMetrics(), fatalCh: make(chan struct{}),
	}, nil
}

// policyDigest is a stable hash of a connection's allowlist so cache keys bind
// policy changes to the process identity (SDD §16.2).
func policyDigest(c config.Connection) string {
	raw, _ := json.Marshal(c.Allow)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// MetricsSnapshot is a sanitized counter snapshot for shutdown summaries.
func (m *Manager) MetricsSnapshot() map[string]int64 { return m.metrics.Snapshot() }

// Init eagerly initializes configured connections. Only failures on required
// connections abort startup; optional connections transition to unavailable
// and are logged.
func (m *Manager) Init(ctx context.Context) error {
	for _, h := range m.registry.List() {
		err := h.Init(ctx, func(ctx context.Context) (engine.Connection, error) {
			return m.initConnection(ctx, h.Name(), h)
		})
		if err != nil {
			if h.Required() {
				return err
			}
			m.logger.Warn("connection unavailable", "connection", h.Name())
		}
	}
	return nil
}

// initConnection builds the engine adapter for one handle.
func (m *Manager) initConnection(ctx context.Context, name string, h *connection.Handle) (engine.Connection, error) {
	if m.newEngine != nil {
		return m.newEngine(ctx, name, h)
	}
	cfg := m.byName[name]
	return postgres.New(ctx, cfg, m.cfg.Limits, m.meta)
}

// HandleTool is the MCP router; it dispatches by tool name and emits one
// sanitized audit attempt + outcome per request (SDD §18.1). Every tool call
// consumes a global admission unit (SDD §17.1): the ingress token is taken
// without blocking, and a saturated process is rejected with SERVER_BUSY
// before lookup, cache, or database work starts.
func (m *Manager) HandleTool(ctx context.Context, name string, args json.RawMessage) (any, error) {
	if m.auditFailed.Load() || m.audit.Failed() {
		return nil, &contract.PublicError{Code: contract.CodeServerBusy, Message: "audit sink failed; admission stopped"}
	}
	reqID, err := randomID()
	if err != nil {
		m.failClosed()
		return nil, auditUnavailableError()
	}
	conn := toolConnection(args)

	// Derive one total deadline and an earlier working deadline. Queueing, lazy
	// init, acquisition, parsing, execution, and encoding use the working
	// context; rollback/reset and audit retain bounded reservations (§15.1).
	effectiveTimeout, timeoutErr := m.effectiveRequestTimeout(name, conn, args)
	responseLimit, responseErr := m.effectiveResponseLimit(name, conn, args)
	totalCtx, workCtx, cancelRequest := m.requestContexts(ctx, effectiveTimeout)
	defer cancelRequest()

	err = m.gate.Reserve(workCtx)
	if err != nil {
		return m.rejectAdmission(totalCtx, reqID, name, conn, err, false)
	}
	if err := m.auditEvent(workCtx, reqID, audit.PhaseAttempt, name, conn, "", "", 0); err != nil {
		m.gate.Cancel()
		return nil, auditUnavailableError()
	}

	queueCtx := workCtx
	queueCancel := func() {}
	if m.cfg.Server.QueueTimeout > 0 {
		queueCtx, queueCancel = context.WithTimeout(workCtx, m.cfg.Server.QueueTimeout)
	}
	err = m.gate.Activate(queueCtx)
	queueCancel()
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) && workCtx.Err() == nil {
			err = admission.ErrBusy
		}
		result, rejectErr := m.rejectAdmission(totalCtx, reqID, name, conn, err, true)
		m.gate.Cancel()
		return result, rejectErr
	}
	defer m.gate.Leave()

	m.metrics.Enter()
	defer m.metrics.Leave()
	start := time.Now()
	var data any
	if timeoutErr != nil {
		err = timeoutErr
	} else if responseErr != nil {
		err = responseErr
	} else {
		data, err = m.dispatch(workCtx, name, args)
		if err == nil && successEnvelopeBytes(data) > responseLimit {
			err = &contract.PublicError{Code: contract.CodeResultValueTooLarge, Message: "response exceeds byte limit"}
		}
	}
	code, outcome := "", audit.OutcomeOK
	if err != nil {
		code = string(contract.CodeFromError(err))
		outcome = "error"
	}
	m.metrics.Inc("request." + name + "." + outcome)
	if auditErr := m.auditEvent(totalCtx, reqID, audit.PhaseOutcome, name, conn, outcome, code, elapsedMS(start)); auditErr != nil {
		return nil, auditUnavailableError()
	}

	if err != nil {
		return nil, err
	}
	return data, nil
}

func (m *Manager) requestContexts(parent context.Context, timeout time.Duration) (context.Context, context.Context, context.CancelFunc) {
	totalCtx := parent
	totalCancel := func() {}
	if timeout > 0 {
		totalCtx, totalCancel = context.WithTimeout(parent, timeout)
	}
	totalDeadline, ok := totalCtx.Deadline()
	if !ok {
		return totalCtx, totalCtx, totalCancel
	}
	auditReserve := m.cfg.Audit.WriteTimeout
	if auditReserve <= 0 || auditReserve > maxAuditReserve {
		auditReserve = maxAuditReserve
	}
	cleanupDeadline := totalDeadline.Add(-auditReserve)
	workDeadline := cleanupDeadline.Add(-requestctx.MaxCleanupDuration)
	workCtx, workCancel := context.WithDeadline(totalCtx, workDeadline)
	workCtx = requestctx.WithCleanupDeadline(workCtx, cleanupDeadline)
	return totalCtx, workCtx, func() {
		workCancel()
		totalCancel()
	}
}

func (m *Manager) effectiveResponseLimit(name, connectionName string, args json.RawMessage) (int, error) {
	ceiling := m.cfg.Limits.ResponseBytes
	if ceiling <= 0 {
		ceiling = config.MaxResponseBytes
	}
	if cfg, ok := m.byName[connectionName]; ok && cfg.Limits != nil && cfg.Limits.ResponseBytes > 0 {
		ceiling = cfg.Limits.ResponseBytes
	}
	var input struct {
		Limits *struct {
			MaxResponseBytes *int `json:"max_response_bytes"`
		} `json:"limits"`
	}
	if name != "db_query" && name != "db_explain" || len(args) == 0 || json.Unmarshal(args, &input) != nil ||
		input.Limits == nil || input.Limits.MaxResponseBytes == nil {
		return ceiling, nil
	}
	requested := *input.Limits.MaxResponseBytes
	if requested <= 0 || requested > ceiling {
		return ceiling, &contract.PublicError{Code: contract.CodeInvalidRequest, Message: "max_response_bytes exceeds configured limit"}
	}
	return requested, nil
}

func successEnvelopeBytes(data any) int {
	meta := contract.SuccessMeta{DurationMS: int64(^uint64(0) >> 1)}
	switch result := data.(type) {
	case contract.QueryResult:
		meta.Truncated = result.Truncated
		meta.TruncationReason = result.TruncationReason
	case map[string]any:
		if sample, ok := result["result"].(contract.QueryResult); ok {
			meta.Truncated = sample.Truncated
			meta.TruncationReason = sample.TruncationReason
		}
	}
	raw, err := json.Marshal(contract.SuccessEnvelope{SchemaVersion: contract.SchemaVersion, Data: data, Meta: meta})
	if err != nil {
		return int(^uint(0) >> 1)
	}
	return len(raw)
}

func (m *Manager) effectiveRequestTimeout(name, connectionName string, args json.RawMessage) (time.Duration, error) {
	ceiling := m.cfg.Server.RequestTimeout
	if cfg, ok := m.byName[connectionName]; ok && cfg.Limits != nil && cfg.Limits.RequestTimeout > 0 &&
		(ceiling <= 0 || cfg.Limits.RequestTimeout < ceiling) {
		ceiling = cfg.Limits.RequestTimeout
	}
	override := requestTimeoutOverride(name, args)
	if override == nil {
		return ceiling, nil
	}
	if *override < int(config.MinRequestTimeout.Milliseconds()) || (ceiling > 0 && int64(*override) > ceiling.Milliseconds()) {
		return ceiling, &contract.PublicError{Code: contract.CodeInvalidRequest, Message: "timeout_ms is outside configured limits"}
	}
	requested := time.Duration(*override) * time.Millisecond
	return requested, nil
}

func requestTimeoutOverride(name string, args json.RawMessage) *int {
	var input struct {
		TimeoutMS *int `json:"timeout_ms"`
		Limits    *struct {
			TimeoutMS *int `json:"timeout_ms"`
		} `json:"limits"`
	}
	if len(args) == 0 || json.Unmarshal(args, &input) != nil {
		return nil
	}
	switch name {
	case "db_ping", "db_sample_rows", "db_count_rows":
		if input.TimeoutMS != nil {
			return input.TimeoutMS
		}
	case "db_query", "db_explain":
		if input.Limits != nil && input.Limits.TimeoutMS != nil {
			return input.Limits.TimeoutMS
		}
	}
	return nil
}

// rejectAdmission audits the attempt+outcome pair for a request that failed to
// enter the gate (saturation or cancellation while queued) and returns the
// corresponding public error, without running the handler (SDD §17.1).
func (m *Manager) rejectAdmission(ctx context.Context, reqID, name, conn string, err error, attempted bool) (any, error) {
	code := string(contract.CodeServerBusy)
	if errors.Is(err, context.Canceled) {
		code = string(contract.CodeCancelled)
	} else if errors.Is(err, context.DeadlineExceeded) {
		code = string(contract.CodeTimeout)
	}
	m.metrics.Inc("request." + name + ".admission_rejected")
	if !attempted {
		if auditErr := m.auditEvent(ctx, reqID, audit.PhaseAttempt, name, conn, "", "", 0); auditErr != nil {
			return nil, auditUnavailableError()
		}
	}
	if auditErr := m.auditEvent(ctx, reqID, audit.PhaseOutcome, name, conn, "error", code, 0); auditErr != nil {
		return nil, auditUnavailableError()
	}
	message := "server busy; maximum concurrent requests reached"
	if code == string(contract.CodeCancelled) {
		message = "request cancelled"
	} else if code == string(contract.CodeTimeout) {
		message = "request timed out"
	}
	return nil, &contract.PublicError{Code: contract.PublicErrorCode(code), Message: message}
}

func auditUnavailableError() error {
	return &contract.PublicError{Code: contract.CodeServerBusy, Message: "audit sink unavailable; admission stopped"}
}

func (m *Manager) dispatch(ctx context.Context, name string, args json.RawMessage) (any, error) {
	switch name {
	case "db_list_connections":
		return m.dbListConnections(ctx)
	case "db_ping":
		return m.dbPing(ctx, args)
	case "db_list_schemas":
		return m.dbListSchemas(ctx, args)
	case "db_list_tables":
		return m.dbListTables(ctx, args)
	case "db_describe_table":
		return m.dbDescribeTable(ctx, args)
	case "db_list_indexes":
		return m.dbListIndexes(ctx, args)
	case "db_list_relationships":
		return m.dbListRelationships(ctx, args)
	case "db_suggest_relationships":
		return m.dbSuggestRelationships(ctx, args)
	case "db_search_columns":
		return m.dbSearchColumns(ctx, args)
	case "db_find_references":
		return m.dbFindReferences(ctx, args)
	case "db_sample_rows":
		return m.dbSampleRows(ctx, args)
	case "db_count_rows":
		return m.dbCountRows(ctx, args)
	case "db_query":
		return m.dbQuery(ctx, args)
	case "db_explain":
		return m.dbExplain(ctx, args)
	default:
		return nil, &contract.PublicError{
			Code:    contract.CodeInternalError,
			Message: fmt.Sprintf("tool %q is not implemented", name),
		}
	}
}

// auditEvent enqueues one sanitized event. On sink failure the process is
// marked fatal: new admission stops and the fatal channel closes so the caller
// can start the §19 shutdown sequence.
func (m *Manager) auditEvent(ctx context.Context, reqID, phase, tool, conn, outcome, code string, duration int64) error {
	var codePtr *string
	if code != "" {
		codePtr = &code
	}
	auditTimeout := m.cfg.Audit.WriteTimeout
	if auditTimeout <= 0 || auditTimeout > maxAuditReserve {
		auditTimeout = maxAuditReserve
	}
	auditCtx, cancel := requestctx.DetachedContext(ctx, auditTimeout)
	defer cancel()
	err := m.audit.Enqueue(auditCtx, audit.Event{
		EventVersion: 1,
		Timestamp:    time.Now().UTC(),
		RequestID:    reqID,
		Phase:        phase,
		Tool:         tool,
		Connection:   conn,
		Outcome:      outcome,
		ErrorCode:    codePtr,
		DurationMS:   duration,
	})
	if err != nil {
		m.failClosed()
		return err
	}
	return nil
}

// failClosed transitions the process to the audit_failed state (SDD §18.1).
func (m *Manager) failClosed() {
	m.auditFailed.Store(true)
	m.fatalOnce.Do(func() {
		close(m.fatalCh)
		m.logger.Error("audit sink failed; shutting down", "event", "audit_failed")
	})
}

// Fatal reports the process-level fatal state.
func (m *Manager) Fatal() <-chan struct{} { return m.fatalCh }

// Close shuts down all connections and flushes the audit sink.
func (m *Manager) Close(ctx context.Context) {
	m.registry.CloseAll(ctx)
	_ = m.audit.Close()
}

func randomID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// toolConnection extracts the opaque connection name from tool arguments for
// the audit event; empty for db_list_connections.
func toolConnection(args json.RawMessage) string {
	var in struct {
		Connection string `json:"connection"`
	}
	if len(args) == 0 {
		return ""
	}
	_ = json.Unmarshal(args, &in)
	return in.Connection
}

func elapsedMS(start time.Time) int64 {
	return time.Since(start).Milliseconds()
}
