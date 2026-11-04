package main

import (
	"database/sql"
	"fmt"
	"log/slog"
	"os"

	"github.com/Weiki886/relythra/internal/config"
	securitylogging "github.com/Weiki886/relythra/internal/security/logging"
	"github.com/Weiki886/relythra/migrations"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

func main() {
	logger, err := securitylogging.NewDefaultLogger(os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "initialize migration logger failed")
		os.Exit(1)
	}
	slog.SetDefault(logger)
	if err := run(os.Args[1:]); err != nil {
		slog.Error("migration failed", "error", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: relythra-migrate up|down|status")
	}

	cfg, err := config.LoadAPI(os.Getenv)
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

	goose.SetBaseFS(migrations.Files)
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}

	switch args[0] {
	case "up":
		return goose.Up(db, ".")
	case "down":
		return goose.Down(db, ".")
	case "status":
		return goose.Status(db, ".")
	default:
		return fmt.Errorf("unknown migration command %q", args[0])
	}
}
