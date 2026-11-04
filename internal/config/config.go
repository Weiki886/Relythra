package config

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Weiki886/relythra/internal/security/secrets"
)

const (
	defaultAPIAddress        = ":8080"
	defaultWorkerAddress     = ":8081"
	defaultShutdownTimeout   = 10 * time.Second
	defaultHeartbeatInterval = 5 * time.Second
)

type API struct {
	Address         string
	DatabaseURL     secrets.Secret
	ShutdownTimeout time.Duration
}

type Worker struct {
	Address           string
	HeartbeatInterval time.Duration
}

func LoadAPI(getenv func(string) string) (API, error) {
	if getenv == nil {
		return API{}, fmt.Errorf("environment lookup is required")
	}
	databaseURL, err := loadDatabaseURL(getenv)
	if err != nil {
		return API{}, err
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

func loadDatabaseURL(getenv func(string) string) (secrets.Secret, error) {
	resolver := secrets.NewResolver(nil, nil)
	ctx := context.Background()

	if rawReference := strings.TrimSpace(getenv("RELYTHRA_DATABASE_URL_REF")); rawReference != "" {
		reference, err := secrets.ParseReference(rawReference)
		if err != nil {
			return secrets.Secret{}, fmt.Errorf("RELYTHRA_DATABASE_URL_REF is invalid")
		}
		value, err := resolver.Resolve(ctx, reference)
		if err != nil {
			return secrets.Secret{}, fmt.Errorf("RELYTHRA_DATABASE_URL_REF could not be resolved")
		}
		return value, nil
	}

	if rawReference := strings.TrimSpace(getenv("RELYTHRA_DATABASE_PASSWORD_REF")); rawReference != "" {
		return buildDatabaseURL(getenv, resolver, rawReference)
	}

	rawURL := strings.TrimSpace(getenv("RELYTHRA_DATABASE_URL"))
	if rawURL == "" {
		return secrets.Secret{}, fmt.Errorf(
			"RELYTHRA_DATABASE_URL, RELYTHRA_DATABASE_URL_REF, or RELYTHRA_DATABASE_PASSWORD_REF is required",
		)
	}
	value, err := secrets.New(rawURL)
	if err != nil {
		return secrets.Secret{}, fmt.Errorf("RELYTHRA_DATABASE_URL is invalid")
	}
	return value, nil
}

func buildDatabaseURL(
	getenv func(string) string,
	resolver *secrets.Resolver,
	rawReference string,
) (secrets.Secret, error) {
	reference, err := secrets.ParseReference(rawReference)
	if err != nil {
		return secrets.Secret{}, fmt.Errorf("RELYTHRA_DATABASE_PASSWORD_REF is invalid")
	}
	password, err := resolver.Resolve(context.Background(), reference)
	if err != nil {
		return secrets.Secret{}, fmt.Errorf("RELYTHRA_DATABASE_PASSWORD_REF could not be resolved")
	}

	host := strings.Trim(strings.TrimSpace(getenv("RELYTHRA_DATABASE_HOST")), "[]")
	port := valueOrDefault(getenv("RELYTHRA_DATABASE_PORT"), "5432")
	name := strings.TrimSpace(getenv("RELYTHRA_DATABASE_NAME"))
	user := strings.TrimSpace(getenv("RELYTHRA_DATABASE_USER"))
	sslMode := valueOrDefault(getenv("RELYTHRA_DATABASE_SSL_MODE"), "disable")

	portNumber, portErr := strconv.Atoi(port)
	if host == "" || strings.ContainsAny(host, "/\\?#@ \t\r\n") ||
		portErr != nil || portNumber < 1 || portNumber > 65535 ||
		name == "" || strings.ContainsAny(name, "/\\?# \t\r\n") ||
		user == "" || strings.ContainsAny(user, "/\\?#@ \t\r\n") ||
		!allowedSSLMode(sslMode) {
		return secrets.Secret{}, fmt.Errorf("database connection fields are invalid")
	}

	databaseURL := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(user, password.Reveal()),
		Host:   net.JoinHostPort(host, port),
		Path:   "/" + name,
		RawQuery: url.Values{
			"sslmode": {sslMode},
		}.Encode(),
	}
	value, err := secrets.New(databaseURL.String())
	if err != nil {
		return secrets.Secret{}, fmt.Errorf("database connection URL is invalid")
	}
	return value, nil
}

func allowedSSLMode(value string) bool {
	switch value {
	case "disable", "allow", "prefer", "require", "verify-ca", "verify-full":
		return true
	default:
		return false
	}
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
