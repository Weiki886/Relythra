package audit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Weiki886/relythra/internal/security/redact"
)

type PostgresRecorder struct {
	db     *sql.DB
	filter *redact.Filter
}

func NewPostgresRecorder(db *sql.DB, filter *redact.Filter) (*PostgresRecorder, error) {
	if db == nil {
		return nil, errors.New("audit database is required")
	}
	if filter == nil {
		return nil, errors.New("audit redaction filter is required")
	}
	return &PostgresRecorder{db: db, filter: filter}, nil
}

func (recorder *PostgresRecorder) Record(ctx context.Context, event Event) (int64, error) {
	if recorder == nil || recorder.db == nil || recorder.filter == nil {
		return 0, fmt.Errorf("%w: recorder is not configured", ErrInvalidEvent)
	}
	event, err := sanitizeEvent(ctx, recorder.filter, event)
	if err != nil {
		return 0, err
	}
	if err := ValidateEvent(event); err != nil {
		return 0, err
	}
	parameters, err := redactParameters(ctx, recorder.filter, event.Parameters)
	if err != nil {
		return 0, err
	}

	var id int64
	err = recorder.db.QueryRowContext(ctx, `
		INSERT INTO audit_events (
			actor, action, object_type, object_id, result, request_id,
			parameters, occurred_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8)
		RETURNING id
	`,
		event.Actor,
		string(event.Action),
		event.ObjectType,
		event.ObjectID,
		string(event.Result),
		event.RequestID,
		string(parameters),
		event.OccurredAt,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("record audit event: %w", err)
	}
	return id, nil
}
