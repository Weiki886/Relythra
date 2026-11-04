package audit

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Weiki886/relythra/internal/security/redact"
)

const auditCanary = "audit-canary-8e03"

func TestRecorderRejectsInvalidEventsBeforeStorage(t *testing.T) {
	t.Parallel()

	filter, err := redact.NewFilter(auditCanary)
	if err != nil {
		t.Fatalf("new filter: %v", err)
	}
	recorder, err := NewPostgresRecorder(nil, filter)
	if err == nil || recorder != nil {
		t.Fatalf("nil database was accepted: recorder=%v err=%v", recorder, err)
	}

	tests := []struct {
		name  string
		event Event
	}{
		{
			name: "missing actor",
			event: validEvent(map[string]any{
				"source": "github",
			}),
		},
		{
			name: "unknown action",
			event: func() Event {
				event := validEvent(map[string]any{})
				event.Action = "raw.secret"
				return event
			}(),
		},
	}
	tests[0].event.Actor = ""

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			if err := ValidateEvent(testCase.event); !errors.Is(err, ErrInvalidEvent) {
				t.Fatalf("error = %v, want ErrInvalidEvent", err)
			}
		})
	}
}

func TestRedactedAuditParametersNeverContainSecret(t *testing.T) {
	t.Parallel()

	filter, err := redact.NewFilter(auditCanary)
	if err != nil {
		t.Fatalf("new filter: %v", err)
	}
	parameters, err := redactParameters(context.Background(), filter, map[string]any{
		"url": "https://admin:password@example.test/hook?token=" + auditCanary,
		"headers": map[string]any{
			"Authorization": "Bearer " + auditCanary,
		},
		"source": "grafana",
	})
	if err != nil {
		t.Fatalf("redact audit parameters: %v", err)
	}
	if strings.Contains(string(parameters), auditCanary) ||
		strings.Contains(string(parameters), "password@") {
		t.Fatalf("audit parameters exposed secret: %s", parameters)
	}
	if !strings.Contains(string(parameters), `"source":"grafana"`) {
		t.Fatalf("audit parameters lost non-sensitive data: %s", parameters)
	}
}

func TestRecorderRejectsOversizedParameters(t *testing.T) {
	t.Parallel()

	filter, err := redact.NewFilter()
	if err != nil {
		t.Fatalf("new filter: %v", err)
	}
	_, err = redactParameters(context.Background(), filter, map[string]any{
		"debug": strings.Repeat("x", MaxParameterBytes+1),
	})
	if !errors.Is(err, ErrParameterTooLarge) {
		t.Fatalf("error = %v, want ErrParameterTooLarge", err)
	}
}

func TestAuditIdentifiersAreRedactedBeforeValidation(t *testing.T) {
	t.Parallel()

	filter, err := redact.NewFilter(auditCanary)
	if err != nil {
		t.Fatalf("new filter: %v", err)
	}
	event := validEvent(map[string]any{})
	event.Actor = "Authorization: Bearer " + auditCanary
	event.ObjectID = "https://user:password@example.test/item?token=" + auditCanary

	safe, err := sanitizeEvent(context.Background(), filter, event)
	if err != nil {
		t.Fatalf("sanitize event: %v", err)
	}
	if strings.Contains(safe.Actor, auditCanary) || strings.Contains(safe.ObjectID, auditCanary) {
		t.Fatalf("sanitized audit identifiers exposed secret: %+v", safe)
	}
	if err := ValidateEvent(safe); err != nil {
		t.Fatalf("sanitized event is invalid: %v", err)
	}
}

func validEvent(parameters map[string]any) Event {
	return Event{
		Actor:      "system",
		Action:     ActionConnectionTested,
		ObjectType: "data_source_mapping",
		ObjectID:   "11111111-1111-4111-8111-111111111111",
		Result:     ResultSucceeded,
		RequestID:  "request-audit-123",
		Parameters: parameters,
		OccurredAt: time.Date(2026, 11, 4, 6, 0, 0, 0, time.UTC),
	}
}
