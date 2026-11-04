package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Weiki886/relythra/internal/config"
	securitylogging "github.com/Weiki886/relythra/internal/security/logging"
	"github.com/Weiki886/relythra/internal/worker"
)

func main() {
	logger, err := securitylogging.NewDefaultLogger(os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "initialize worker logger failed")
		os.Exit(1)
	}
	slog.SetDefault(logger)
	if err := run(); err != nil {
		slog.Error("worker stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.LoadWorker(os.Getenv)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	heartbeat := worker.NewHeartbeat(cfg.HeartbeatInterval)
	go heartbeat.Start(ctx)

	server := &http.Server{
		Addr:              cfg.Address,
		Handler:           heartbeat.MetricsHandler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	errorChannel := make(chan error, 1)
	go func() {
		slog.Info("worker metrics listening", "address", cfg.Address)
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

	shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return server.Shutdown(shutdownContext)
}
