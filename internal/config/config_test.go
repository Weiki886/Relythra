package config_test

import (
	"testing"
	"time"

	"github.com/Weiki886/Relythra/internal/config"
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
