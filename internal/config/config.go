package config

import (
	"fmt"
	"strings"
	"time"
)

const (
	defaultAPIAddress        = ":8080"
	defaultWorkerAddress     = ":8081"
	defaultShutdownTimeout   = 10 * time.Second
	defaultHeartbeatInterval = 5 * time.Second
)

type API struct {
	Address         string
	DatabaseURL     string
	ShutdownTimeout time.Duration
}

type Worker struct {
	Address           string
	HeartbeatInterval time.Duration
}

func LoadAPI(getenv func(string) string) (API, error) {
	databaseURL := strings.TrimSpace(getenv("RELYTHRA_DATABASE_URL"))
	if databaseURL == "" {
		return API{}, fmt.Errorf("RELYTHRA_DATABASE_URL is required")
	}

	shutdownTimeout, err := positiveDuration(getenv, "RELYTHRA_SHUTDOWN_TIMEOUT", defaultShutdownTimeout)
	if err != nil {
		return API{}, err
	}

	return API{
		Address:         valueOrDefault(getenv("RELYTHRA_API_ADDR"), defaultAPIAddress),
		DatabaseURL:     databaseURL,
		ShutdownTimeout: shutdownTimeout,
	}, nil
}

func LoadWorker(getenv func(string) string) (Worker, error) {
	heartbeatInterval, err := positiveDuration(getenv, "RELYTHRA_WORKER_HEARTBEAT_INTERVAL", defaultHeartbeatInterval)
	if err != nil {
		return Worker{}, err
	}

	return Worker{
		Address:           valueOrDefault(getenv("RELYTHRA_WORKER_ADDR"), defaultWorkerAddress),
		HeartbeatInterval: heartbeatInterval,
	}, nil
}

func positiveDuration(getenv func(string) string, key string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(getenv(key))
	if raw == "" {
		return fallback, nil
	}

	value, err := time.ParseDuration(raw)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", key)
	}
	return value, nil
}

func valueOrDefault(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
