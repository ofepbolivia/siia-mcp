package config

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

const (
	Version = 1

	MaxMCPFrameBytes        = 8 * 1024 * 1024
	MaxJSONNestingDepth     = 64
	MaxQueryBytes           = 64 * 1024
	MaxParameterCount       = 128
	MaxParameterBytes       = 1024 * 1024
	MaxParametersTotalBytes = 4 * 1024 * 1024
	MaxRequestTimeout       = 60 * time.Second
	MaxQueryRows            = 1000
	MaxSampleRows           = 100
	MaxValueBytes           = 1024 * 1024
	MaxResponseBytes        = 8 * 1024 * 1024
	MaxMetadataPageSize     = 500
	MaxActiveRequests       = 32
	MaxQueuedRequests       = 64
	MaxQueueTimeout         = 10 * time.Second
	MaxPoolConnections      = 32
	MaxCacheTTL             = 10 * time.Minute
	MaxCacheEntries         = 10000
	MaxCacheBytes           = 64 * 1024 * 1024
	MaxAuditQueueSize       = 1024
	MaxAuditWriteTimeout    = 5 * time.Second
	MaxShutdownTimeout      = 30 * time.Second
	MaxTotalPoolConnections = 128
	MaxAuditFileBytes       = 1024 * 1024 * 1024
	MaxAuditFiles           = 16
	MinRequestTimeout       = 5 * time.Second
	MinQueueTimeout         = 100 * time.Millisecond
	MinCacheTTL             = time.Second
	MinAuditWriteTimeout    = 100 * time.Millisecond
)

var connectionNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)

func ValidConnectionName(name string) bool {
	return connectionNamePattern.MatchString(name)
}

type Config struct {
	Version       int           `yaml:"version"`
	Server        ServerConfig  `yaml:"server"`
	Limits        LimitsConfig  `yaml:"limits"`
	MetadataCache CacheConfig   `yaml:"metadata_cache"`
	Logging       LoggingConfig `yaml:"logging"`
	Audit         AuditConfig   `yaml:"audit"`
	Network       NetworkConfig `yaml:"network"`
	Connection    *Connection   `yaml:"connection"`
}

type ServerConfig struct {
	RequestTimeout          time.Duration `yaml:"request_timeout"`
	ShutdownTimeout         time.Duration `yaml:"shutdown_timeout"`
	ActiveRequests          int           `yaml:"active_requests"`
	QueuedRequests          int           `yaml:"queued_requests"`
	QueueTimeout            time.Duration `yaml:"queue_timeout"`
	MaxTotalPoolConnections int           `yaml:"max_total_pool_connections"`
}

type LimitsConfig struct {
	MCPFrameBytes        int `yaml:"mcp_frame_bytes"`
	JSONNestingDepth     int `yaml:"json_nesting_depth"`
	QueryBytes           int `yaml:"query_bytes"`
	ParameterCount       int `yaml:"parameter_count"`
	ParameterBytes       int `yaml:"parameter_bytes"`
	ParametersTotalBytes int `yaml:"parameters_total_bytes"`
	QueryRows            int `yaml:"query_rows"`
	SampleRows           int `yaml:"sample_rows"`
	ValueBytes           int `yaml:"value_bytes"`
	ResponseBytes        int `yaml:"response_bytes"`
	MetadataPageSize     int `yaml:"metadata_page_size"`
}

type CacheConfig struct {
	TTL        time.Duration `yaml:"ttl"`
	MaxEntries int           `yaml:"max_entries"`
	MaxBytes   int           `yaml:"max_bytes"`
}

type LoggingConfig struct {
	Level string `yaml:"level"`
	Sink  string `yaml:"sink"`
}

type AuditConfig struct {
	Sink         string        `yaml:"sink"`
	Path         string        `yaml:"path"`
	WriteTimeout time.Duration `yaml:"write_timeout"`
	QueueSize    int           `yaml:"queue_size"`
	MaxFileBytes int           `yaml:"max_file_bytes"`
	MaxFiles     int           `yaml:"max_files"`
}

type NetworkConfig struct {
	IntranetCIDRs []string `yaml:"intranet_cidrs"`
}

type Connection struct {
	Name       string            `yaml:"name"`
	Engine     string            `yaml:"engine"`
	Mode       string            `yaml:"mode"`
	Required   bool              `yaml:"required"`
	Initialize string            `yaml:"initialize"`
	Host       string            `yaml:"host"`
	Port       int               `yaml:"port"`
	Database   string            `yaml:"database"`
	User       string            `yaml:"user"`
	Password   string            `yaml:"password"`
	TLS        TLSConfig         `yaml:"tls"`
	Pool       PoolConfig        `yaml:"pool"`
	Limits     *ConnectionLimits `yaml:"limits"`
	Allow      *Allowlist        `yaml:"allow"`
}

type TLSConfig struct {
	Mode       string `yaml:"mode"`
	RootCA     string `yaml:"root_ca"`
	ServerName string `yaml:"server_name"`
}

type PoolConfig struct {
	MaxConnections        int           `yaml:"max_connections"`
	MinConnections        int           `yaml:"min_connections"`
	MaxConnectionLifetime time.Duration `yaml:"max_connection_lifetime"`
	MaxConnectionIdleTime time.Duration `yaml:"max_connection_idle_time"`
	HealthCheckPeriod     time.Duration `yaml:"health_check_period"`
}

type ConnectionLimits struct {
	RequestTimeout   time.Duration `yaml:"request_timeout"`
	QueryRows        int           `yaml:"query_rows"`
	SampleRows       int           `yaml:"sample_rows"`
	ValueBytes       int           `yaml:"value_bytes"`
	ResponseBytes    int           `yaml:"response_bytes"`
	MetadataPageSize int           `yaml:"metadata_page_size"`
}

type Allowlist struct {
	Schemas           []string    `yaml:"schemas"`
	Views             []ObjectRef `yaml:"views"`
	MaterializedViews []ObjectRef `yaml:"materialized_views"`
}

type ObjectRef struct {
	Schema string `yaml:"schema"`
	Name   string `yaml:"name"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(data, os.LookupEnv)
}

func LoadWithEnvFile(path, envPath string) (*Config, error) {
	env, err := ReadEnvFile(envPath)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(data, func(name string) (string, bool) {
		if value, ok := os.LookupEnv(name); ok {
			return value, ok
		}
		value, ok := env[name]
		return value, ok
	})
}

func ReadEnvFile(path string) (map[string]string, error) {
	values := map[string]string{}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return values, nil
		}
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("env file permissions must be 0600 or stricter: %s", path)
	}
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("env file line %d must be KEY=VALUE", i+1)
		}
		key = strings.TrimSpace(key)
		if !validEnvName(key) {
			return nil, fmt.Errorf("env file line %d has invalid variable name %q", i+1, key)
		}
		values[key] = unquoteEnvValue(strings.TrimSpace(value))
	}
	return values, nil
}

func validEnvName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		if r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r == '_' || i > 0 && r >= '0' && r <= '9' {
			continue
		}
		return false
	}
	return true
}

func unquoteEnvValue(value string) string {
	if unquoted, err := strconv.Unquote(value); err == nil {
		return unquoted
	}
	return value
}

func Parse(data []byte, lookupEnv func(string) (string, bool)) (*Config, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	if len(root.Content) != 1 {
		return nil, errors.New("configuration must contain one document")
	}
	if err := rejectDuplicateKeys(root.Content[0], ""); err != nil {
		return nil, err
	}
	if err := interpolateScalars(root.Content[0], lookupEnv); err != nil {
		return nil, err
	}

	var cfg Config
	buf := bytes.NewBuffer(nil)
	enc := yaml.NewEncoder(buf)
	if err := enc.Encode(root.Content[0]); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	dec := yaml.NewDecoder(buf)
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (cfg *Config) Validate() error {
	if cfg.Version != Version {
		return fmt.Errorf("version must be %d", Version)
	}
	if err := cfg.Server.validate(); err != nil {
		return fmt.Errorf("server: %w", err)
	}
	if err := cfg.Limits.validate(); err != nil {
		return fmt.Errorf("limits: %w", err)
	}
	if err := cfg.MetadataCache.validate(); err != nil {
		return fmt.Errorf("metadata_cache: %w", err)
	}
	if err := cfg.Logging.validate(); err != nil {
		return fmt.Errorf("logging: %w", err)
	}
	if err := cfg.Audit.validate(); err != nil {
		return fmt.Errorf("audit: %w", err)
	}
	if cfg.Logging.Sink == cfg.Audit.Sink {
		return errors.New("logging and audit sinks must differ")
	}
	if err := cfg.Network.validate(); err != nil {
		return fmt.Errorf("network: %w", err)
	}
	// v1 targets a single read-only connection to SER v3.0. The connection is
	// optional at parse time so `init` can write a base config; the server
	// requires it before accepting operations.
	if cfg.Connection != nil {
		if err := cfg.Connection.validate(cfg.Server.RequestTimeout, cfg.Limits); err != nil {
			return fmt.Errorf("connection: %w", err)
		}
		if cfg.Connection.Pool.MaxConnections > cfg.Server.MaxTotalPoolConnections ||
			cfg.Connection.Pool.MaxConnections > MaxTotalPoolConnections {
			return errors.New("pool connections exceed configured or internal ceiling")
		}
	}
	return nil
}

func (c ServerConfig) validate() error {
	if err := durationBetween("request_timeout", c.RequestTimeout, MinRequestTimeout, MaxRequestTimeout); err != nil {
		return err
	}
	if err := durationBetween("shutdown_timeout", c.ShutdownTimeout, time.Nanosecond, MaxShutdownTimeout); err != nil {
		return err
	}
	if err := intBetween("active_requests", c.ActiveRequests, 1, MaxActiveRequests); err != nil {
		return err
	}
	if err := intBetween("queued_requests", c.QueuedRequests, 1, MaxQueuedRequests); err != nil {
		return err
	}
	if err := durationBetween("queue_timeout", c.QueueTimeout, MinQueueTimeout, MaxQueueTimeout); err != nil {
		return err
	}
	return intBetween("max_total_pool_connections", c.MaxTotalPoolConnections, 1, MaxTotalPoolConnections)
}

func (l LimitsConfig) validate() error {
	checks := []struct {
		name       string
		value, max int
	}{
		{"mcp_frame_bytes", l.MCPFrameBytes, MaxMCPFrameBytes},
		{"json_nesting_depth", l.JSONNestingDepth, MaxJSONNestingDepth},
		{"query_bytes", l.QueryBytes, MaxQueryBytes},
		{"parameter_count", l.ParameterCount, MaxParameterCount},
		{"parameter_bytes", l.ParameterBytes, MaxParameterBytes},
		{"parameters_total_bytes", l.ParametersTotalBytes, MaxParametersTotalBytes},
		{"query_rows", l.QueryRows, MaxQueryRows},
		{"sample_rows", l.SampleRows, MaxSampleRows},
		{"value_bytes", l.ValueBytes, MaxValueBytes},
		{"response_bytes", l.ResponseBytes, MaxResponseBytes},
		{"metadata_page_size", l.MetadataPageSize, MaxMetadataPageSize},
	}
	for _, check := range checks {
		if err := intBetween(check.name, check.value, 1, check.max); err != nil {
			return err
		}
	}
	return nil
}

func (c CacheConfig) validate() error {
	if err := durationBetween("ttl", c.TTL, MinCacheTTL, MaxCacheTTL); err != nil {
		return err
	}
	if err := intBetween("max_entries", c.MaxEntries, 1, MaxCacheEntries); err != nil {
		return err
	}
	return intBetween("max_bytes", c.MaxBytes, 1, MaxCacheBytes)
}

func (l LoggingConfig) validate() error {
	if l.Level != "debug" && l.Level != "info" && l.Level != "warn" && l.Level != "error" {
		return errors.New("level must be debug, info, warn, or error")
	}
	if l.Sink != "stderr" {
		return errors.New("sink must be stderr")
	}
	return nil
}

func (a AuditConfig) validate() error {
	if a.Sink != "file" {
		return errors.New("sink must be file")
	}
	if strings.TrimSpace(a.Path) == "" || strings.Contains(a.Path, "${") {
		return errors.New("path must be a non-empty expanded value")
	}
	if err := durationBetween("write_timeout", a.WriteTimeout, MinAuditWriteTimeout, MaxAuditWriteTimeout); err != nil {
		return err
	}
	if err := intBetween("queue_size", a.QueueSize, 1, MaxAuditQueueSize); err != nil {
		return err
	}
	if err := intBetween("max_file_bytes", a.MaxFileBytes, 1, MaxAuditFileBytes); err != nil {
		return err
	}
	return intBetween("max_files", a.MaxFiles, 1, MaxAuditFiles)
}

func (n NetworkConfig) validate() error {
	for _, value := range n.IntranetCIDRs {
		ip, network, err := net.ParseCIDR(value)
		if err != nil {
			return fmt.Errorf("invalid CIDR %q", value)
		}
		if ip.IsUnspecified() || ip.IsMulticast() || isAllNetwork(network) {
			return fmt.Errorf("CIDR %q is not allowed", value)
		}
	}
	return nil
}

func (c Connection) validate(globalRequestTimeout time.Duration, global LimitsConfig) error {
	if !ValidConnectionName(c.Name) {
		return fmt.Errorf("invalid connection name %q", c.Name)
	}
	if c.Engine != "postgres" {
		return errors.New("engine must be postgres")
	}
	if c.Mode != "readonly" {
		return errors.New("mode must be readonly")
	}
	if c.Initialize != "eager" && c.Initialize != "lazy" {
		return errors.New("initialize must be eager or lazy")
	}
	if c.Required && c.Initialize != "eager" {
		return errors.New("required connections must initialize eagerly")
	}
	if strings.TrimSpace(c.Host) == "" || strings.TrimSpace(c.Database) == "" || strings.TrimSpace(c.User) == "" {
		return errors.New("host, database, and user are required")
	}
	if strings.TrimSpace(c.Password) == "" || strings.Contains(c.Password, "${") {
		return errors.New("password must be a non-empty expanded value")
	}
	if strings.ContainsFunc(c.Password, unicode.IsSpace) {
		return errors.New("password must not contain whitespace")
	}
	if err := intBetween("port", c.Port, 1, 65535); err != nil {
		return err
	}
	if err := c.TLS.validate(c.Host); err != nil {
		return fmt.Errorf("tls: %w", err)
	}
	if err := c.Pool.validate(); err != nil {
		return fmt.Errorf("pool: %w", err)
	}
	// allow is optional. When nil (or empty), the connection exposes every
	// object the read-only role can read (SDD §8.3). When present, it is a
	// default-deny allowlist.
	if c.Allow != nil && !c.Allow.empty() {
		if err := c.Allow.validate(); err != nil {
			return fmt.Errorf("allow: %w", err)
		}
	}
	if c.Limits != nil {
		if err := c.Limits.validate(globalRequestTimeout, global); err != nil {
			return fmt.Errorf("limits: %w", err)
		}
	}
	return nil
}

func (t TLSConfig) validate(host string) error {
	if t.Mode != "verify-full" && t.Mode != "disable" {
		return errors.New("mode must be verify-full or disable")
	}
	if t.Mode == "disable" && !tlsDisableAllowed(host) {
		return errors.New("disable is only allowed for Unix sockets, loopback hosts, or private IP addresses")
	}
	if t.Mode == "verify-full" {
		if strings.TrimSpace(t.ServerName) == "" {
			return errors.New("server_name is required for verify-full")
		}
		if t.RootCA != "" {
			if _, err := os.Stat(t.RootCA); err != nil {
				return fmt.Errorf("root_ca is not readable: %w", err)
			}
		}
	}
	return nil
}

func (p PoolConfig) validate() error {
	if err := intBetween("max_connections", p.MaxConnections, 1, MaxPoolConnections); err != nil {
		return err
	}
	if err := intBetween("min_connections", p.MinConnections, 0, p.MaxConnections); err != nil {
		return err
	}
	if err := positiveDuration("max_connection_lifetime", p.MaxConnectionLifetime); err != nil {
		return err
	}
	if err := positiveDuration("max_connection_idle_time", p.MaxConnectionIdleTime); err != nil {
		return err
	}
	return positiveDuration("health_check_period", p.HealthCheckPeriod)
}

func (l ConnectionLimits) validate(globalRequestTimeout time.Duration, global LimitsConfig) error {
	if l.RequestTimeout != 0 {
		if err := durationBetween("request_timeout", l.RequestTimeout, MinRequestTimeout, globalRequestTimeout); err != nil {
			return err
		}
	}
	checks := []struct {
		name          string
		value, global int
	}{
		{"query_rows", l.QueryRows, global.QueryRows},
		{"sample_rows", l.SampleRows, global.SampleRows},
		{"value_bytes", l.ValueBytes, global.ValueBytes},
		{"response_bytes", l.ResponseBytes, global.ResponseBytes},
		{"metadata_page_size", l.MetadataPageSize, global.MetadataPageSize},
	}
	for _, check := range checks {
		if check.value != 0 {
			if err := intBetween(check.name, check.value, 1, check.global); err != nil {
				return err
			}
		}
	}
	return nil
}

func (a Allowlist) empty() bool {
	return len(a.Schemas) == 0 && len(a.Views) == 0 && len(a.MaterializedViews) == 0
}

// Empty reports whether the allowlist restricts nothing; an empty allowlist
// means the connection is unrestricted (everything the role can read is
// exposed, SDD §8.3).
func (a Allowlist) Empty() bool {
	return a.empty()
}

func (a Allowlist) validate() error {
	seenSchemas := map[string]struct{}{}
	for _, schema := range a.Schemas {
		if err := validatePGName("schema", schema); err != nil {
			return err
		}
		if _, ok := seenSchemas[schema]; ok {
			return fmt.Errorf("duplicate schema %q", schema)
		}
		seenSchemas[schema] = struct{}{}
	}
	if err := validateObjectRefs("view", a.Views); err != nil {
		return err
	}
	return validateObjectRefs("materialized_view", a.MaterializedViews)
}

func validateObjectRefs(kind string, refs []ObjectRef) error {
	seen := map[string]struct{}{}
	for _, ref := range refs {
		if err := validatePGName(kind+" schema", ref.Schema); err != nil {
			return err
		}
		if err := validatePGName(kind+" name", ref.Name); err != nil {
			return err
		}
		key := ref.Schema + "." + ref.Name
		if _, ok := seen[key]; ok {
			return fmt.Errorf("duplicate %s %q", kind, key)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func validatePGName(kind, value string) error {
	if value == "" || len(value) > 63 || !utf8.ValidString(value) || strings.ContainsAny(value, `.*"`) {
		return fmt.Errorf("invalid %s %q", kind, value)
	}
	return nil
}

func rejectDuplicateKeys(node *yaml.Node, path string) error {
	if node.Kind == yaml.MappingNode {
		seen := map[string]struct{}{}
		for i := 0; i < len(node.Content); i += 2 {
			key := node.Content[i].Value
			full := key
			if path != "" {
				full = path + "." + key
			}
			if _, ok := seen[key]; ok {
				return fmt.Errorf("duplicate key %q", full)
			}
			seen[key] = struct{}{}
			if key == "dsn" || key == "url" {
				return fmt.Errorf("%q fields are not allowed", key)
			}
			if err := rejectDuplicateKeys(node.Content[i+1], full); err != nil {
				return err
			}
		}
	}
	if node.Kind == yaml.SequenceNode {
		for i, child := range node.Content {
			if err := rejectDuplicateKeys(child, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	}
	return nil
}

func interpolateScalars(node *yaml.Node, lookupEnv func(string) (string, bool)) error {
	if node.Kind == yaml.ScalarNode && node.Tag == "!!str" {
		value, err := interpolate(node.Value, lookupEnv)
		if err != nil {
			return err
		}
		node.Value = value
		return nil
	}
	if node.Kind == yaml.MappingNode {
		for i := 1; i < len(node.Content); i += 2 {
			if err := interpolateScalars(node.Content[i], lookupEnv); err != nil {
				return err
			}
		}
	}
	if node.Kind == yaml.SequenceNode {
		for _, child := range node.Content {
			if err := interpolateScalars(child, lookupEnv); err != nil {
				return err
			}
		}
	}
	return nil
}

func interpolate(value string, lookupEnv func(string) (string, bool)) (string, error) {
	for {
		start := strings.Index(value, "${")
		if start == -1 {
			return value, nil
		}
		end := strings.IndexByte(value[start+2:], '}')
		if end == -1 {
			return "", fmt.Errorf("unterminated environment interpolation in %q", value)
		}
		end += start + 2
		name := value[start+2 : end]
		if name == "" || strings.ContainsAny(name, ":-/ `$") {
			return "", fmt.Errorf("unsupported environment interpolation %q", "${"+name+"}")
		}
		replacement, ok := lookupEnv(name)
		if !ok || replacement == "" {
			return "", fmt.Errorf("environment variable %q is unset or empty", name)
		}
		value = value[:start] + replacement + value[end+1:]
	}
}

func intBetween(name string, value, min, max int) error {
	if value < min || value > max {
		return fmt.Errorf("%s must be between %d and %d", name, min, max)
	}
	return nil
}

func durationBetween(name string, value, min, max time.Duration) error {
	if value < min || value > max {
		return fmt.Errorf("%s must be between %s and %s", name, min, max)
	}
	return nil
}

func positiveDuration(name string, value time.Duration) error {
	if value <= 0 {
		return fmt.Errorf("%s must be positive", name)
	}
	return nil
}

func tlsDisableAllowed(host string) bool {
	if filepath.IsAbs(host) {
		return true
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate())
}

func isAllNetwork(network *net.IPNet) bool {
	ones, bits := network.Mask.Size()
	return ones == 0 && (bits == 32 || bits == 128)
}
