package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseValidConfig(t *testing.T) {
	cfg, err := Parse([]byte(validConfig()), env(map[string]string{
		"MCP_DB_AUDIT_PATH": "/tmp/siia-audit.log",
		"APP_DB_PASSWORD":   "secret",
	}))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if cfg.Connection.Password != "secret" {
		t.Fatalf("password was not interpolated")
	}
	if cfg.Redacted().Connection.Password != redactedValue {
		t.Fatalf("redacted config leaked password")
	}
}

func TestParseAllowsTLSDisableForPrivateIP(t *testing.T) {
	config := strings.Replace(validConfig(), "host: localhost", "host: 192.168.2.105", 1)
	_, err := Parse([]byte(config), env(map[string]string{
		"MCP_DB_AUDIT_PATH": "/tmp/siia-audit.log",
		"APP_DB_PASSWORD":   "secret",
	}))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
}

func TestParseAllowsEmptyAllowlist(t *testing.T) {
	config := strings.Replace(validConfig(),
		"  allow:\n    schemas:\n    - public\n    views:\n    - schema: reporting\n      name: active_accounts\n    materialized_views: []",
		"  allow:\n    schemas: []\n    views: []\n    materialized_views: []", 1)
	cfg, err := Parse([]byte(config), env(map[string]string{
		"MCP_DB_AUDIT_PATH": "/tmp/siia-audit.log",
		"APP_DB_PASSWORD":   "secret",
	}))
	if err != nil {
		t.Fatalf("Parse(empty allowlist) error = %v", err)
	}
	allow := cfg.Connection.Allow
	if allow == nil || !allow.empty() {
		t.Fatalf("Allow = %#v, want empty allowlist (unrestricted)", allow)
	}
}

func TestParseAllowsMissingAllowlist(t *testing.T) {
	config := strings.Replace(validConfig(),
		"  allow:\n    schemas:\n    - public\n    views:\n    - schema: reporting\n      name: active_accounts\n    materialized_views: []",
		"", 1)
	cfg, err := Parse([]byte(config), env(map[string]string{
		"MCP_DB_AUDIT_PATH": "/tmp/siia-audit.log",
		"APP_DB_PASSWORD":   "secret",
	}))
	if err != nil {
		t.Fatalf("Parse(no allowlist) error = %v", err)
	}
	if allow := cfg.Connection.Allow; allow != nil && !allow.empty() {
		t.Fatalf("Allow = %#v, want nil or empty (unrestricted)", allow)
	}
}

func TestParseAllowsBaseConfigWithoutConnection(t *testing.T) {
	config := validConfig()[:strings.Index(validConfig(), "\nconnection:")] + "\n"
	_, err := Parse([]byte(config), env(map[string]string{
		"MCP_DB_AUDIT_PATH": "/tmp/siia-audit.log",
	}))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
}

func TestLoadWithEnvFile(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	envPath := filepath.Join(dir, "env")
	if err := os.WriteFile(configPath, []byte(validConfig()), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(envPath, []byte("MCP_DB_AUDIT_PATH=/tmp/siia-audit.log\nAPP_DB_PASSWORD=filesecret\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadWithEnvFile(configPath, envPath)
	if err != nil {
		t.Fatalf("LoadWithEnvFile() error = %v", err)
	}
	if cfg.Connection.Password != "filesecret" {
		t.Fatalf("password = %q, want env file value", cfg.Connection.Password)
	}
}

func TestLoadWithEnvFilePrefersProcessEnv(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	envPath := filepath.Join(dir, "env")
	if err := os.WriteFile(configPath, []byte(validConfig()), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(envPath, []byte("MCP_DB_AUDIT_PATH=/tmp/siia-audit.log\nAPP_DB_PASSWORD=filesecret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APP_DB_PASSWORD", "processsecret")

	cfg, err := LoadWithEnvFile(configPath, envPath)
	if err != nil {
		t.Fatalf("LoadWithEnvFile() error = %v", err)
	}
	if cfg.Connection.Password != "processsecret" {
		t.Fatalf("password = %q, want process env value", cfg.Connection.Password)
	}
}

func TestReadEnvFileRejectsInsecurePermissions(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, "env")
	if err := os.WriteFile(envPath, []byte("APP_DB_PASSWORD=secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ReadEnvFile(envPath)
	if err == nil || !strings.Contains(err.Error(), "permissions") {
		t.Fatalf("ReadEnvFile() error = %v, want permissions error", err)
	}
}

func TestParseRejectsInvalidConfig(t *testing.T) {
	tests := []struct {
		name    string
		config  string
		wantErr string
	}{
		{
			name:    "duplicate key",
			config:  strings.Replace(validConfig(), "version: 1", "version: 1\nversion: 1", 1),
			wantErr: "duplicate key",
		},
		{
			name:    "unknown key",
			config:  strings.Replace(validConfig(), "server:", "unexpected: true\nserver:", 1),
			wantErr: "field unexpected not found",
		},
		{
			name:    "dsn key",
			config:  strings.Replace(validConfig(), "host: localhost", "dsn: postgres://example\n    host: localhost", 1),
			wantErr: "not allowed",
		},
		{
			name:    "missing environment variable",
			config:  validConfig(),
			wantErr: "unset or empty",
		},
		{
			name:    "password contains whitespace",
			config:  validConfig(),
			wantErr: "password must not contain whitespace",
		},
		{
			name:    "connection name starts with number",
			config:  strings.Replace(validConfig(), "name: app_dev", "name: 1app_dev", 1),
			wantErr: "invalid connection name",
		},
		{
			name:    "connection name contains hyphen",
			config:  strings.Replace(validConfig(), "name: app_dev", "name: app-dev", 1),
			wantErr: "invalid connection name",
		},
		{
			name:    "required lazy connection",
			config:  strings.Replace(validConfig(), "initialize: eager", "initialize: lazy", 1),
			wantErr: "required connections must initialize eagerly",
		},
		{
			name:    "connection elevates global limit",
			config:  strings.Replace(validConfig(), "query_rows: 100", "query_rows: 201", 1),
			wantErr: "query_rows must be between 1 and 200",
		},
		{
			name:    "non-loopback tls disable",
			config:  strings.Replace(validConfig(), "host: localhost", "host: db.internal.example", 1),
			wantErr: "disable is only allowed",
		},
		{
			name:    "public IP tls disable",
			config:  strings.Replace(validConfig(), "host: localhost", "host: 203.0.113.10", 1),
			wantErr: "disable is only allowed",
		},
		{
			name:    "invalid CIDR",
			config:  strings.Replace(validConfig(), "intranet_cidrs: []", "intranet_cidrs:\n    - 0.0.0.0/0", 1),
			wantErr: "not allowed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values := map[string]string{
				"MCP_DB_AUDIT_PATH": "/tmp/siia-audit.log",
				"APP_DB_PASSWORD":   "secret",
			}
			if tt.name == "missing environment variable" {
				values = map[string]string{}
			} else if tt.name == "password contains whitespace" {
				values["APP_DB_PASSWORD"] = "secret password"
			}
			_, err := Parse([]byte(tt.config), env(values))
			if err == nil {
				t.Fatal("Parse() error = nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Parse() error = %q, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func env(values map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		value, ok := values[name]
		return value, ok
	}
}

func validConfig() string {
	return `version: 1
server:
  request_timeout: 15s
  shutdown_timeout: 10s
  active_requests: 4
  queued_requests: 4
  queue_timeout: 2s
  max_total_pool_connections: 64
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
  path: ${MCP_DB_AUDIT_PATH}
  write_timeout: 2s
  queue_size: 64
  max_file_bytes: 268435456
  max_files: 8
network:
  intranet_cidrs: []
connection:
  name: app_dev
  engine: postgres
  mode: readonly
  required: true
  initialize: eager
  host: localhost
  port: 5432
  database: app
  user: mcp_reader
  password: ${APP_DB_PASSWORD}
  tls:
    mode: disable
  pool:
    max_connections: 4
    min_connections: 0
    max_connection_lifetime: 30m
    max_connection_idle_time: 5m
    health_check_period: 30s
  limits:
    request_timeout: 10s
    query_rows: 100
    response_bytes: 1048576
  allow:
    schemas:
    - public
    views:
    - schema: reporting
      name: active_accounts
    materialized_views: []
`
}
