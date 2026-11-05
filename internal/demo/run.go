package demo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/Weiki886/relythra/internal/config"
	_ "github.com/jackc/pgx/v5/stdlib"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

type postgresStock struct{ db *sql.DB }

func (s postgresStock) Stock(ctx context.Context) (int, error) {
	var q int
	err := s.db.QueryRowContext(ctx, "SELECT quantity FROM demo_stock WHERE sku = 'demo-sku' AND schema_version = 1").Scan(&q)
	return q, err
}
func (s postgresStock) SlowQuery(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, "SELECT pg_sleep(0.2)")
	return err
}

// Run is shared process wiring; service identity is fixed by each binary.
func Run(service string) error {
	container, err := os.Hostname()
	if err != nil {
		return errors.New("read container identity failed")
	}
	cfg := Config{Service: service, Project: "relythra-demo", Environment: "demo", Release: os.Getenv("DEMO_RELEASE"), Host: os.Getenv("DEMO_HOST_ID"), Container: container, InventoryURL: os.Getenv("DEMO_INVENTORY_URL"), EnableFaults: os.Getenv("DEMO_ENABLE_FAULTS") == "true"}
	logfile, err := NewRotatingLog("/var/log/demo/"+service+".log", 4*1024*1024)
	if err != nil {
		return errors.New("open demo log failed")
	}
	defer logfile.Close()
	logger := slog.New(slog.NewJSONHandler(io.MultiWriter(os.Stdout, logfile), nil))
	var stock StockStore
	if service == "inventory" {
		dbCfg, err := config.LoadAPI(os.Getenv)
		if err != nil {
			return errors.New("invalid demo database config")
		}
		db, err := sql.Open("pgx", dbCfg.DatabaseURL.Reveal())
		if err != nil {
			return errors.New("open demo database failed")
		}
		defer db.Close()
		db.SetMaxOpenConns(4)
		db.SetMaxIdleConns(2)
		stock = postgresStock{db: db}
	}
	handler, err := NewHandler(cfg, stock, nil, logger)
	if err != nil {
		return err
	}
	server := &http.Server{Addr: ":8080", Handler: handler, ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errs := make(chan error, 1)
	go func() { errs <- server.ListenAndServe() }()
	select {
	case <-ctx.Done():
	case err := <-errs:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("demo HTTP server failed")
		}
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return server.Shutdown(shutdown)
}
