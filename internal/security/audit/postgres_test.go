package audit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Weiki886/relythra/internal/security/redact"
	"github.com/Weiki886/relythra/migrations"
	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

func TestPostgresRecorderPersistsRedactedImmutableAuditEvents(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("RELYTHRA_TEST_DATABASE_URL"))
	if databaseURL == "" {
		t.Skip("RELYTHRA_TEST_DATABASE_URL is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db := openIsolatedDatabase(t, ctx, databaseURL)

	filter, err := redact.NewFilter(auditCanary)
	if err != nil {
		t.Fatalf("new filter: %v", err)
	}
	recorder, err := NewPostgresRecorder(db, filter)
	if err != nil {
		t.Fatalf("new recorder: %v", err)
	}
	event := validEvent(map[string]any{
		"source": "grafana",
		"url":    "https://admin:password@example.test/hook?token=" + auditCanary,
		"headers": map[string]any{
			"Authorization": "Bearer " + auditCanary,
		},
	})
	event.Actor = "Authorization: Bearer " + auditCanary
	event.ObjectID = "https://user:password@example.test/item?token=" + auditCanary

	id, err := recorder.Record(ctx, event)
	if err != nil {
		t.Fatalf("record audit event: %v", err)
	}
	var parameters string
	var actor string
	var objectID string
	if err := db.QueryRowContext(ctx, `
		SELECT actor, object_id, parameters::text
		FROM audit_events
		WHERE id = $1
	`, id).Scan(&actor, &objectID, &parameters); err != nil {
		t.Fatalf("read audit event: %v", err)
	}
	for field, value := range map[string]string{
		"actor":      actor,
		"object ID":  objectID,
		"parameters": parameters,
	} {
		if strings.Contains(value, auditCanary) || strings.Contains(value, "password@") {
			t.Fatalf("stored audit %s exposed secret: %s", field, value)
		}
	}
	if !strings.Contains(parameters, `"source": "grafana"`) {
		t.Fatalf("stored audit parameters lost operation context: %s", parameters)
	}

	assertAuditMutationRejected(t, ctx, db, `
		UPDATE audit_events SET actor = 'tampered' WHERE id = $1
	`, id)
	assertAuditMutationRejected(t, ctx, db, `
		DELETE FROM audit_events WHERE id = $1
	`, id)
	assertAuditMutationRejected(t, ctx, db, `
		TRUNCATE audit_events
	`)
}

func openIsolatedDatabase(t *testing.T, ctx context.Context, databaseURL string) *sql.DB {
	t.Helper()

	adminDB, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("open admin database: %v", err)
	}
	schema := fmt.Sprintf("relythra_audit_test_%d", time.Now().UnixNano())
	if _, err := adminDB.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		adminDB.Close()
		t.Fatalf("create isolated schema: %v", err)
	}

	testURL, err := url.Parse(databaseURL)
	if err != nil {
		adminDB.Close()
		t.Fatalf("parse database URL: %v", err)
	}
	query := testURL.Query()
	query.Set("search_path", schema)
	testURL.RawQuery = query.Encode()
	db, err := sql.Open("pgx", testURL.String())
	if err != nil {
		adminDB.Close()
		t.Fatalf("open isolated database: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() {
		db.Close()
		_, _ = adminDB.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		adminDB.Close()
	})

	goose.SetBaseFS(migrations.Files)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("set goose dialect: %v", err)
	}
	if err := goose.UpContext(ctx, db, "."); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	return db
}

func assertAuditMutationRejected(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	statement string,
	args ...any,
) {
	t.Helper()
	_, err := db.ExecContext(ctx, statement, args...)
	var databaseError *pgconn.PgError
	if !errors.As(err, &databaseError) || databaseError.Code != "23001" {
		t.Fatalf("audit mutation error = %v, want SQLSTATE 23001", err)
	}
}
