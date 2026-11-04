// Package audit records immutable, redacted evidence of security-relevant
// operations.
package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/Weiki886/relythra/internal/security/redact"
)

const MaxParameterBytes = 64 * 1024

var (
	ErrInvalidEvent      = errors.New("invalid audit event")
	ErrParameterTooLarge = errors.New("audit parameters exceed size limit")

	safeIdentifier = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)
)

type Action string

const (
	ActionConfigChanged    Action = "config.changed"
	ActionConnectionTested Action = "connection.tested"
	ActionWebhookReceived  Action = "webhook.received"
	ActionToolCalled       Action = "tool.called"
	ActionNotificationSent Action = "notification.sent"
	ActionReportGenerated  Action = "report.generated"
)

type Result string

const (
	ResultSucceeded Result = "SUCCEEDED"
	ResultFailed    Result = "FAILED"
	ResultDenied    Result = "DENIED"
)

type Event struct {
	Actor      string
	Action     Action
	ObjectType string
	ObjectID   string
	Result     Result
	RequestID  string
	Parameters map[string]any
	OccurredAt time.Time
}

type Recorder interface {
	Record(context.Context, Event) (int64, error)
}

func ValidateEvent(event Event) error {
	if strings.TrimSpace(event.Actor) == "" || len(event.Actor) > 256 ||
		!allowedAction(event.Action) ||
		strings.TrimSpace(event.ObjectType) == "" || len(event.ObjectType) > 128 ||
		strings.TrimSpace(event.ObjectID) == "" || len(event.ObjectID) > 256 ||
		!allowedResult(event.Result) ||
		!safeIdentifier.MatchString(event.RequestID) ||
		event.OccurredAt.IsZero() {
		return ErrInvalidEvent
	}
	return nil
}

func sanitizeEvent(ctx context.Context, filter *redact.Filter, event Event) (Event, error) {
	if filter == nil {
		return Event{}, fmt.Errorf("%w: redaction filter is required", ErrInvalidEvent)
	}

	var err error
	if event.Actor, err = filter.Text(ctx, event.Actor); err != nil {
		return Event{}, fmt.Errorf("%w: actor could not be redacted", ErrInvalidEvent)
	}
	if event.ObjectType, err = filter.Text(ctx, event.ObjectType); err != nil {
		return Event{}, fmt.Errorf("%w: object type could not be redacted", ErrInvalidEvent)
	}
	if event.ObjectID, err = filter.Text(ctx, event.ObjectID); err != nil {
		return Event{}, fmt.Errorf("%w: object ID could not be redacted", ErrInvalidEvent)
	}
	if event.RequestID, err = filter.Text(ctx, event.RequestID); err != nil {
		return Event{}, fmt.Errorf("%w: request ID could not be redacted", ErrInvalidEvent)
	}
	return event, nil
}

func redactParameters(
	ctx context.Context,
	filter *redact.Filter,
	parameters map[string]any,
) (json.RawMessage, error) {
	if filter == nil {
		return nil, fmt.Errorf("%w: redaction filter is required", ErrInvalidEvent)
	}
	if parameters == nil {
		parameters = map[string]any{}
	}
	redacted, err := filter.JSON(ctx, parameters)
	if err != nil {
		return nil, fmt.Errorf("%w: parameters could not be redacted", ErrInvalidEvent)
	}
	encoded, err := json.Marshal(redacted)
	if err != nil {
		return nil, fmt.Errorf("%w: parameters are not serializable", ErrInvalidEvent)
	}
	if len(encoded) > MaxParameterBytes {
		return nil, ErrParameterTooLarge
	}
	return encoded, nil
}

func allowedAction(action Action) bool {
	switch action {
	case ActionConfigChanged,
		ActionConnectionTested,
		ActionWebhookReceived,
		ActionToolCalled,
		ActionNotificationSent,
		ActionReportGenerated:
		return true
	default:
		return false
	}
}

func allowedResult(result Result) bool {
	switch result {
	case ResultSucceeded, ResultFailed, ResultDenied:
		return true
	default:
		return false
	}
}
