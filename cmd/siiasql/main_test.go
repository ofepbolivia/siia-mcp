package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/siia/siia-mcp/internal/config"
)

func TestRunInitCreatesEnvSkeleton(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	t.Setenv("XDG_STATE_HOME", base)
	t.Setenv("SIIASQL_ENV", "")

	var stdout, stderr bytes.Buffer
	if code := run([]string{"init"}, &stdout, &stderr); code != 0 {
		t.Fatalf("init exit=%d stderr=%s", code, stderr.String())
	}

	envPath := filepath.Join(base, "siiasql", "env")
	data, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("env not created: %v", err)
	}
	for _, variable := range []string{
		"SIIA_DB_HOST=", "SIIA_DB_DATABASE=", "SIIA_DB_USER=", "SIIA_DB_PASSWORD=",
	} {
		if !strings.Contains(string(data), variable) {
			t.Fatalf("env missing %q:\n%s", variable, data)
		}
	}
	info, err := os.Stat(envPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("env permission = %o, want 0600", got)
	}
}

func TestInitConfigValidatesOnceEnvFilled(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	t.Setenv("XDG_STATE_HOME", base)
	t.Setenv("SIIASQL_ENV", "")
	t.Setenv("SIIA_DB_HOST", "127.0.0.1")
	t.Setenv("SIIA_DB_DATABASE", "postgres")
	t.Setenv("SIIA_DB_USER", "postgres")
	t.Setenv("SIIA_DB_PASSWORD", "secret")

	var stdout, stderr bytes.Buffer
	if code := run([]string{"init"}, &stdout, &stderr); code != 0 {
		t.Fatalf("init exit=%d stderr=%s", code, stderr.String())
	}

	configPath := filepath.Join(base, "siiasql", "config.yaml")
	if _, err := config.Load(configPath); err != nil {
		t.Fatalf("init config does not validate once env is filled: %v", err)
	}
}

func TestInitEnvFileDoesNotOverwrite(t *testing.T) {
	envPath := filepath.Join(t.TempDir(), "env")
	if err := initEnvFile(envPath); err != nil {
		t.Fatalf("first init: %v", err)
	}
	if err := os.WriteFile(envPath, []byte("SIIA_DB_HOST=db\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := initEnvFile(envPath); err != nil {
		t.Fatalf("second init: %v", err)
	}
	data, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "SIIA_DB_HOST=db\n" {
		t.Fatalf("env was overwritten: %q", data)
	}
}
