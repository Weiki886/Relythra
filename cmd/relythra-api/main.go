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
	"github.com/Weiki886/relythra/internal/health"
	securitylogging "github.com/Weiki886/relythra/internal/security/logging"
	"github.com/Weiki886/relythra/internal/security/redact"
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
	filter, err := redact.NewFilter(cfg.DatabaseURL.Reveal())
	if err != nil {
		return err
	}
	logger, err := securitylogging.NewDefaultLogger(os.Stderr, cfg.DatabaseURL.Reveal())
	if err != nil {
		return err
	}
	slog.SetDefault(logger)

	db, err := sql.Open("pgx", cfg.DatabaseURL.Reveal())
	if err != nil {
		return err
	}
	defer db.Close()

	server := &http.Server{
		Addr:              cfg.Address,
		Handler:           newHandler(db, logger, filter),
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

func newHandler(db *sql.DB, logger *slog.Logger, filters ...*redact.Filter) http.Handler {
	filter, err := redact.NewFilter()
	if err != nil {
		panic("initialize redaction filter: " + err.Error())
	}
	if len(filters) > 0 && filters[0] != nil {
		filter = filters[0]
	}
	mux := http.NewServeMux()
	mux.Handle("/health/", health.NewHandler(db, logger))
	mux.Handle("/api/v1/", api.NewHandler(store.NewPostgres(db), logger, filter))
	return mux
}
