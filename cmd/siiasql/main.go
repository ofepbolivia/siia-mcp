package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/siia/siia-mcp/internal/config"
	"github.com/siia/siia-mcp/internal/manager"
	"github.com/siia/siia-mcp/internal/mcpserver"
	"github.com/siia/siia-mcp/internal/postgres"
)

var version = "dev"

const (
	programName    = "siiasql"
	implementation = "siiasql"
	configDirName  = "siiasql"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "--help", "-h", "help":
			printRootHelp(stdout)
			return 0
		case "doctor":
			return runDoctor(args[1:], stdout, stderr)
		case "init":
			return runInit(args[1:], stdout, stderr)
		}
	}
	return runServer(args, stderr)
}

// runServer starts the stdio MCP server. stdout is reserved for MCP protocol
// frames; all human-facing logs and errors go to stderr.
func runServer(args []string, stderr io.Writer) int {
	flags := flag.NewFlagSet(programName, flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "path to the SIIASQL configuration file")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *configPath == "" || flags.NArg() != 0 {
		printRootUsage(stderr)
		return 2
	}

	paths, err := defaultPaths(*configPath)
	if err != nil {
		fmt.Fprintf(stderr, "configuration error: %v\n", err)
		return 1
	}
	cfg, err := config.LoadWithEnvFile(paths.configPath, paths.envPath)
	if err != nil {
		fmt.Fprintf(stderr, "configuration error: %v\n", err)
		return 1
	}

	logger := newLogger(cfg, stderr)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)

	mgr, err := manager.New(cfg, logger)
	if err != nil {
		fmt.Fprintf(stderr, "startup error: %v\n", err)
		return 1
	}
	if err := mgr.Init(ctx); err != nil {
		fmt.Fprintf(stderr, "connection initialization error: %v\n", err)
		return 1
	}

	impl := &mcp.Implementation{Name: implementation, Version: version}
	server := mcpserver.New(impl, mgr.HandleTool, logger)
	logger.Info("MCP server started; waiting for stdio messages", "event", "server.ready")

	runErr := make(chan error, 1)
	go func() { runErr <- server.Run(ctx, cfg.Limits.MCPFrameBytes, cfg.Limits.JSONNestingDepth) }()

	// Single shutdown sequence on signal, fatal audit, transport/EOF error.
	exitCode := 0
	consumedRunErr := false
	select {
	case <-signals:
		logger.Info("signal received; shutting down", "event", "shutdown.start")
	case <-mgr.Fatal():
		exitCode = 1
		logger.Error("fatal state; shutting down", "event", "shutdown.start")
	case serr := <-runErr:
		consumedRunErr = true
		if serr != nil {
			exitCode = 1
			logger.Warn("transport ended", "event", "shutdown.start", "error", serr)
		} else {
			logger.Info("transport disconnected; shutting down", "event", "shutdown.start")
		}
	}
	cancel()
	if !consumedRunErr {
		logTransportShutdownError(logger, <-runErr)
	}

	mgr.Close(context.Background())
	logMetrics(logger, mgr.MetricsSnapshot())
	return exitCode
}

func runDoctor(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "--help", "-h", "help":
			printDoctorHelp(stdout)
			return 0
		}
	}
	flags := flag.NewFlagSet(programName+" doctor", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "path to the SIIASQL configuration file")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		printDoctorUsage(stderr)
		return 2
	}

	paths, err := defaultPaths(*configPath)
	if err != nil {
		fmt.Fprintf(stderr, "doctor error: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, "SIIASQL doctor")
	fmt.Fprintf(stdout, "  config: %s\n", paths.configPath)
	fmt.Fprintf(stdout, "  env: %s\n", paths.envPath)

	cfg, err := config.LoadWithEnvFile(paths.configPath, paths.envPath)
	if err != nil {
		fmt.Fprintf(stdout, "config: failed - %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, "config: ok")
	fmt.Fprintln(stdout, "env: ok")

	mgr, err := manager.New(cfg, newLogger(cfg, stderr))
	if err != nil {
		fmt.Fprintf(stdout, "audit: failed - %v\n", err)
		return 1
	}
	defer mgr.Close(context.Background())
	fmt.Fprintln(stdout, "audit: ok")

	if cfg.Connection == nil {
		fmt.Fprintln(stdout, "connection: none configured")
		return 0
	}

	exitCode := 0
	if err := doctorPingConnection(cfg, *cfg.Connection, stdout); err != nil {
		fmt.Fprintf(stdout, "connection %s: failed - %v\n", cfg.Connection.Name, err)
		exitCode = 1
	}
	return exitCode
}

func doctorPingConnection(cfg *config.Config, connCfg config.Connection, stdout io.Writer) error {
	timeout := cfg.Server.RequestTimeout
	if timeout <= 0 {
		timeout = config.MinRequestTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	conn, err := postgres.New(ctx, connCfg, cfg.Limits, nil)
	if err != nil {
		return err
	}
	defer conn.Close(context.Background())
	res, err := conn.Ping(ctx)
	if err != nil {
		return err
	}
	if res.ServerMajor > 0 {
		fmt.Fprintf(stdout, "connection %s: ok - PostgreSQL %d\n", connCfg.Name, res.ServerMajor)
		return nil
	}
	fmt.Fprintf(stdout, "connection %s: ok\n", connCfg.Name)
	return nil
}

// runInit writes a minimal PostgreSQL-only, read-only base config.
func runInit(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "--help", "-h", "help":
			printInitHelp(stdout)
			return 0
		}
	}
	flags := flag.NewFlagSet(programName+" init", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "path where the config file will be created")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		printInitUsage(stderr)
		return 2
	}

	paths, err := defaultPaths(*configPath)
	if err != nil {
		fmt.Fprintf(stderr, "init error: %v\n", err)
		return 1
	}
	if err := initBaseConfig(paths); err != nil {
		fmt.Fprintf(stderr, "init error: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "SIIASQL initialized\n")
	fmt.Fprintf(stdout, "  config: %s\n", paths.configPath)
	fmt.Fprintf(stdout, "  env: %s\n", paths.envPath)
	return 0
}

type paths struct {
	configPath string
	envPath    string
	auditPath  string
}

// defaultPaths resolves the config and env file locations. The env file holds
// secrets and is resolved independently so it can live outside the config dir.
func defaultPaths(configPath string) (paths, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return paths{}, fmt.Errorf("resolve config directory: %w", err)
	}
	stateDir, err := userStateDir()
	if err != nil {
		return paths{}, fmt.Errorf("resolve state directory: %w", err)
	}
	if configPath == "" {
		configPath = filepath.Join(configDir, configDirName, "config.yaml")
	}
	envPath := os.Getenv("SIIASQL_ENV")
	if envPath == "" {
		envPath = filepath.Join(configDir, configDirName, "env")
	}
	return paths{
		configPath: configPath,
		envPath:    envPath,
		auditPath:  filepath.Join(stateDir, configDirName, "audit.jsonl"),
	}, nil
}

func userStateDir() (string, error) {
	if value := os.Getenv("XDG_STATE_HOME"); value != "" {
		return value, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state"), nil
}

func newLogger(cfg *config.Config, stderr io.Writer) *slog.Logger {
	level := slog.LevelInfo
	if cfg != nil {
		switch cfg.Logging.Level {
		case "debug":
			level = slog.LevelDebug
		case "warn":
			level = slog.LevelWarn
		case "error":
			level = slog.LevelError
		}
	}
	return slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: level}))
}

func logMetrics(logger *slog.Logger, summary map[string]int64) {
	keys := make([]string, 0, len(summary))
	for k := range summary {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		logger.Info("metric", "name", k, "value", summary[k])
	}
}

func logTransportShutdownError(logger *slog.Logger, err error) {
	if err != nil && !errors.Is(err, context.Canceled) {
		logger.Warn("transport error after shutdown", "event", "transport.error", "error", err)
	}
}

func initBaseConfig(p paths) error {
	if err := os.MkdirAll(filepath.Dir(p.configPath), 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(p.auditPath), 0o700); err != nil {
		return fmt.Errorf("create audit directory: %w", err)
	}
	if err := initEnvFile(p.envPath); err != nil {
		return err
	}
	file, err := os.OpenFile(p.auditPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create audit file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close audit file: %w", err)
	}
	configFile, err := os.OpenFile(p.configPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("config already exists: %s", p.configPath)
		}
		return fmt.Errorf("create config file: %w", err)
	}
	if _, err := configFile.Write([]byte(baseConfigYAML(p.auditPath))); err != nil {
		_ = configFile.Close()
		return fmt.Errorf("write config file: %w", err)
	}
	return configFile.Close()
}

// envTemplate is the secrets skeleton written by init. Values are intentionally
// empty so the operator fills them; config.LoadWithEnvFile fails loudly while a
// referenced variable is empty.
const envTemplate = `# SIIASQL secrets — referenced from config.yaml via ${VAR} interpolation.
# Resolved first from the process environment, then from this file.
# Permissions must be 0600 or stricter.
SIIA_DB_HOST=
SIIA_DB_DATABASE=
SIIA_DB_USER=
SIIA_DB_PASSWORD=
`

// initEnvFile creates the env file with the secret variable skeleton at 0600,
// without overwriting an existing file. The env path is resolved independently
// so it can live outside the config directory.
func initEnvFile(envPath string) error {
	if err := os.MkdirAll(filepath.Dir(envPath), 0o700); err != nil {
		return fmt.Errorf("create env directory: %w", err)
	}
	file, err := os.OpenFile(envPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil
		}
		return fmt.Errorf("create env file: %w", err)
	}
	if _, err := file.WriteString(envTemplate); err != nil {
		_ = file.Close()
		return fmt.Errorf("write env file: %w", err)
	}
	return file.Close()
}

func baseConfigYAML(auditPath string) string {
	return fmt.Sprintf(`version: 1
server:
  request_timeout: 15s
  shutdown_timeout: 10s
  active_requests: 4
  queued_requests: 4
  queue_timeout: 2s
  max_total_pool_connections: 16
limits:
  mcp_frame_bytes: 4194304
  json_nesting_depth: 32
  query_bytes: 32768
  parameter_count: 64
  parameter_bytes: 262144
  parameters_total_bytes: 1048576
  query_rows: 200
  sample_rows: 20
  value_bytes: 262144
  response_bytes: 2097152
  metadata_page_size: 100
metadata_cache:
  ttl: 60s
  max_entries: 1024
  max_bytes: 16777216
logging:
  level: info
  sink: stderr
audit:
  sink: file
  path: %q
  write_timeout: 2s
  queue_size: 64
  max_file_bytes: 268435456
  max_files: 8
network:
  intranet_cidrs: []
# SIIASQL targets a single read-only connection to SER v3.0. Values in ${...}
# are resolved from the env file created next to this config (or the process
# environment); fill them there and this config works as-is.
connection:
  name: siia_ser
  engine: postgres
  mode: readonly
  required: true
  initialize: eager
  host: ${SIIA_DB_HOST}
  port: 5432
  database: ${SIIA_DB_DATABASE}
  user: ${SIIA_DB_USER}
  password: ${SIIA_DB_PASSWORD}
  tls:
    mode: disable
  pool:
    max_connections: 4
    min_connections: 0
    max_connection_lifetime: 30m
    max_connection_idle_time: 5m
    health_check_period: 30s
`, auditPath)
}

func printRootUsage(w io.Writer) {
	fmt.Fprintf(w, "usage: %s --config <path>\n", programName)
	fmt.Fprintf(w, "       %s init [--config <path>]\n", programName)
	fmt.Fprintf(w, "       %s doctor [--config <path>]\n", programName)
}

func printRootHelp(w io.Writer) {
	printRootUsage(w)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "SIIASQL is a read-only PostgreSQL MCP server over stdio.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  --config <path>    Start the MCP server for the given config")
	fmt.Fprintln(w, "  init               Create a base config and audit file")
	fmt.Fprintln(w, "  doctor             Check config, env, audit and connection health")
}

func printDoctorUsage(w io.Writer) {
	fmt.Fprintf(w, "usage: %s doctor [--config <path>]\n", programName)
}

func printDoctorHelp(w io.Writer) {
	printDoctorUsage(w)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Validates the config and env file, verifies the audit sink is writable,")
	fmt.Fprintln(w, "and pings every configured database connection.")
}

func printInitUsage(w io.Writer) {
	fmt.Fprintf(w, "usage: %s init [--config <path>]\n", programName)
}

func printInitHelp(w io.Writer) {
	printInitUsage(w)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Creates the base PostgreSQL-only config, audit file, and env skeleton.")
}
