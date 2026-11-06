package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Weiki886/relythra/internal/api"
	"github.com/Weiki886/relythra/internal/config"
	connectorhealth "github.com/Weiki886/relythra/internal/connector/health"
	"github.com/Weiki886/relythra/internal/health"
	securityaudit "github.com/Weiki886/relythra/internal/security/audit"
	securitylogging "github.com/Weiki886/relythra/internal/security/logging"
	"github.com/Weiki886/relythra/internal/security/outbound"
	"github.com/Weiki886/relythra/internal/security/redact"
	"github.com/Weiki886/relythra/internal/security/secrets"
	"github.com/Weiki886/relythra/internal/store"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	logger, err := securitylogging.NewDefaultLogger(os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "initialize API logger failed")
		os.Exit(1)
	}
	slog.SetDefault(logger)
	if err := run(); err != nil {
		slog.Error("API stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.LoadAPI(os.Getenv)
	if err != nil {
		return err
	}
	knownSecrets := runtimeKnownSecrets(cfg)
	filter, err := redact.NewFilter(knownSecrets...)
	if err != nil {
		return err
	}
	logger, err := securitylogging.NewDefaultLogger(os.Stderr, knownSecrets...)
	if err != nil {
		return err
	}
	slog.SetDefault(logger)

	db, err := sql.Open("pgx", cfg.DatabaseURL.Reveal())
	if err != nil {
		return err
	}
	defer db.Close()

	handler, err := newConfiguredHandler(db, cfg, logger, filter)
	if err != nil {
		return err
	}
	server := &http.Server{
		Addr:              cfg.Address,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errorChannel := make(chan error, 1)
	go func() {
		slog.Info("API listening", "address", cfg.Address)
		errorChannel <- server.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
	case err := <-errorChannel:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}

	shutdownContext, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	return server.Shutdown(shutdownContext)
}

func runtimeKnownSecrets(cfg config.API) []string {
	secrets := []string{cfg.DatabaseURL.Reveal()}
	if cfg.AdminToken.Reveal() != "" {
		secrets = append(secrets, cfg.AdminToken.Reveal())
	}
	if cfg.DeploymentWebhookSecret.Reveal() != "" {
		secrets = append(secrets, cfg.DeploymentWebhookSecret.Reveal())
	}
	if cfg.GrafanaWebhookSecret.Reveal() != "" {
		secrets = append(secrets, cfg.GrafanaWebhookSecret.Reveal())
	}
	return secrets
}

func newHandler(db *sql.DB, logger *slog.Logger, filters ...*redact.Filter) http.Handler {
	handler, err := newConfiguredHandler(db, config.API{}, logger, filters...)
	if err != nil {
		panic("configure API handler: " + err.Error())
	}
	return handler
}

func newConfiguredHandler(
	db *sql.DB,
	cfg config.API,
	logger *slog.Logger,
	filters ...*redact.Filter,
) (http.Handler, error) {
	return newConfiguredHandlerWithAuditRecorderFactory(
		db,
		cfg,
		logger,
		func(db *sql.DB, filter *redact.Filter) (securityaudit.Recorder, error) {
			return securityaudit.NewPostgresRecorder(db, filter)
		},
		filters...,
	)
}

type auditRecorderFactory func(*sql.DB, *redact.Filter) (securityaudit.Recorder, error)

func newConfiguredHandlerWithAuditRecorderFactory(
	db *sql.DB,
	cfg config.API,
	logger *slog.Logger,
	newAuditRecorder auditRecorderFactory,
	filters ...*redact.Filter,
) (http.Handler, error) {
	filter, err := redact.NewFilter()
	if err != nil {
		return nil, err
	}
	if len(filters) > 0 && filters[0] != nil {
		filter = filters[0]
	}
	repository := store.NewPostgres(db)
	dependencies := api.Dependencies{
		Reader:                  repository,
		Deployments:             repository,
		AdminAuthenticator:      api.ManagementUnavailableAuthenticator{},
		DeploymentWebhookSecret: cfg.DeploymentWebhookSecret,
		DeploymentWebhookSource: cfg.DeploymentWebhookSource,
		GrafanaWebhookSecret:    cfg.GrafanaWebhookSecret,
		GrafanaAlerts:           repository,
		ManagementActor:         cfg.ManagementActor,
	}
	if cfg.AdminToken.Reveal() != "" {
		authenticator, authErr := api.NewBearerTokenAuthenticator(cfg.AdminToken)
		if authErr != nil {
			return nil, authErr
		}
		dependencies.AdminAuthenticator = authenticator
	}
	if cfg.AdminToken.Reveal() != "" || cfg.DeploymentWebhookSecret.Reveal() != "" || cfg.GrafanaWebhookSecret.Reveal() != "" {
		recorder, recorderErr := newAuditRecorder(db, filter)
		if recorderErr != nil {
			return nil, recorderErr
		}
		dependencies.AuditRecorder = recorder
	}
	if len(cfg.DataSourcePolicy.AllowedHosts) > 0 {
		policy, policyErr := outbound.NewPolicy(cfg.DataSourcePolicy)
		if policyErr != nil {
			return nil, policyErr
		}
		tester, testerErr := connectorhealth.NewTester(policy, secrets.NewResolver(nil, nil))
		if testerErr != nil {
			return nil, testerErr
		}
		dependencies.Mappings = repository
		dependencies.ConnectionTester = tester
	}
	mux := http.NewServeMux()
	mux.Handle("/health/", health.NewHandler(db, logger))
	mux.Handle("/api/v1/", api.NewHandlerWithDependencies(dependencies, logger, filter))
	return mux, nil
}
