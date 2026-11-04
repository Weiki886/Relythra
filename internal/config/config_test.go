package config_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Weiki886/relythra/internal/config"
)

func environment(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func TestLoadAPIUsesDefaults(t *testing.T) {
	t.Parallel()

	cfg, err := config.LoadAPI(environment(map[string]string{
		"RELYTHRA_DATABASE_URL": "postgres://localhost/relythra",
	}))
	if err != nil {
		t.Fatalf("LoadAPI() error = %v", err)
	}
	if cfg.Address != ":8080" {
		t.Errorf("Address = %q, want :8080", cfg.Address)
	}
	if cfg.ShutdownTimeout != 10*time.Second {
		t.Errorf("ShutdownTimeout = %v, want 10s", cfg.ShutdownTimeout)
	}
	if cfg.DatabaseURL.Reveal() != "postgres://localhost/relythra" {
		t.Fatal("database URL was not resolved")
	}
}

func TestLoadAPIRequiresDatabaseURL(t *testing.T) {
	t.Parallel()

	_, err := config.LoadAPI(environment(nil))
	if err == nil {
		t.Fatal("LoadAPI() error = nil, want missing database URL error")
	}
}

func TestLoadAPIRejectsInvalidShutdownTimeout(t *testing.T) {
	t.Parallel()

	_, err := config.LoadAPI(environment(map[string]string{
		"RELYTHRA_DATABASE_URL":     "postgres://localhost/relythra",
		"RELYTHRA_SHUTDOWN_TIMEOUT": "forever",
	}))
	if err == nil {
		t.Fatal("LoadAPI() error = nil, want invalid duration error")
	}
}

func TestLoadAPIResolvesDatabaseURLReference(t *testing.T) {
	t.Setenv("RELYTHRA_TEST_DATABASE_SECRET", "postgres://reader:canary@database/relythra")

	cfg, err := config.LoadAPI(environment(map[string]string{
		"RELYTHRA_DATABASE_URL_REF": "env:RELYTHRA_TEST_DATABASE_SECRET",
	}))
	if err != nil {
		t.Fatalf("LoadAPI() error = %v", err)
	}
	if cfg.DatabaseURL.Reveal() != "postgres://reader:canary@database/relythra" {
		t.Fatal("database URL reference was not resolved")
	}
	if strings.Contains(fmt.Sprintf("%+v", cfg), "canary") {
		t.Fatalf("formatted config exposed database secret: %+v", cfg)
	}
}

func TestLoadAPIBuildsDatabaseURLFromSecretFile(t *testing.T) {
	const canary = "compose-file-canary-4e80"
	secretFile := filepath.Join(t.TempDir(), "postgres-password")
	if err := os.WriteFile(secretFile, []byte(canary+"\n"), 0o600); err != nil {
		t.Fatalf("write secret fixture: %v", err)
	}

	cfg, err := config.LoadAPI(environment(map[string]string{
		"RELYTHRA_DATABASE_HOST":         "database.internal",
		"RELYTHRA_DATABASE_PORT":         "6432",
		"RELYTHRA_DATABASE_NAME":         "relythra",
		"RELYTHRA_DATABASE_USER":         "diagnoser",
		"RELYTHRA_DATABASE_PASSWORD_REF": "file:" + secretFile,
		"RELYTHRA_DATABASE_SSL_MODE":     "require",
	}))
	if err != nil {
		t.Fatalf("LoadAPI() error = %v", err)
	}
	databaseURL := cfg.DatabaseURL.Reveal()
	for _, expected := range []string{
		"postgres://diagnoser:",
		"@database.internal:6432/relythra",
		"sslmode=require",
	} {
		if !strings.Contains(databaseURL, expected) {
			t.Errorf("database URL missing %q", expected)
		}
	}
	if !strings.Contains(databaseURL, canary) {
		t.Fatal("database URL did not include resolved password")
	}
}

func TestLoadAPIRejectsIncompleteDatabaseSecretConfiguration(t *testing.T) {
	const canary = "missing-parts-canary-90fa"
	_, err := config.LoadAPI(environment(map[string]string{
		"RELYTHRA_DATABASE_PASSWORD_REF": "env:RELYTHRA_TEST_MISSING_PASSWORD",
	}))
	if err == nil {
		t.Fatal("incomplete database configuration was accepted")
	}
	if strings.Contains(err.Error(), canary) {
		t.Fatal("configuration error exposed a secret")
	}
}

func TestLoadWorkerUsesDefaults(t *testing.T) {
	t.Parallel()

	cfg, err := config.LoadWorker(environment(nil))
	if err != nil {
		t.Fatalf("LoadWorker() error = %v", err)
	}
	if cfg.Address != ":8081" {
		t.Errorf("Address = %q, want :8081", cfg.Address)
	}
	if cfg.HeartbeatInterval != 5*time.Second {
		t.Errorf("HeartbeatInterval = %v, want 5s", cfg.HeartbeatInterval)
	}
}

func TestLoadWorkerRejectsNonPositiveHeartbeatInterval(t *testing.T) {
	t.Parallel()

	_, err := config.LoadWorker(environment(map[string]string{
		"RELYTHRA_WORKER_HEARTBEAT_INTERVAL": "0s",
	}))
	if err == nil {
		t.Fatal("LoadWorker() error = nil, want positive duration error")
	}
}
