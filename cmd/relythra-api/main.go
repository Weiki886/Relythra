package main

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Weiki886/relythra/internal/api"
	"github.com/Weiki886/relythra/internal/config"
	"github.com/Weiki886/relythra/internal/health"
	"github.com/Weiki886/relythra/internal/store"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
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

	db, err := sql.Open("pgx", cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()

	server := &http.Server{
		Addr:              cfg.Address,
		Handler:           newHandler(db, slog.Default()),
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

func newHandler(db *sql.DB, logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/health/", health.NewHandler(db, logger))
	mux.Handle("/api/v1/", api.NewHandler(store.NewPostgres(db), logger))
	return mux
}
