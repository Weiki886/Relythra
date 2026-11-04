package logging

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/Weiki886/relythra/internal/security/redact"
)

func TestHandlerRedactsMessagesAttributesAndErrors(t *testing.T) {
	t.Parallel()

	const canary = "logging-canary-6a41"
	filter, err := redact.NewFilter(canary)
	if err != nil {
		t.Fatalf("new filter: %v", err)
	}
	var output bytes.Buffer
	inner := slog.NewJSONHandler(&output, nil)
	logger := slog.New(NewHandler(inner, filter)).With(
		"component",
		"Authorization: Bearer "+canary,
	)

	logger.ErrorContext(
		context.Background(),
		"connector failed with "+canary,
		"error",
		errors.New("postgres://admin:"+canary+"@database:5432/relythra"),
		"request",
		map[string]any{
			"url":     "https://user:password@example.test/hook?token=" + canary,
			"api_key": canary,
		},
	)
	logger.Info("credential received", "password", "ordinary-value-that-must-not-leave")

	logged := output.String()
	if strings.Contains(logged, canary) || strings.Contains(logged, "password@") {
		t.Fatalf("log output exposed secret: %s", logged)
	}
	if strings.Contains(logged, "ordinary-value-that-must-not-leave") {
		t.Fatalf("log output exposed a value under a sensitive key: %s", logged)
	}
	if !strings.Contains(logged, "[REDACTED]") {
		t.Fatalf("log output has no redaction marker: %s", logged)
	}
	if !strings.Contains(logged, "postgres://") || !strings.Contains(logged, "database:5432") {
		t.Fatalf("log output lost diagnostic error text: %s", logged)
	}
	for _, field := range []string{"component", "error", "request", "msg"} {
		if !strings.Contains(logged, `"`+field+`"`) {
			t.Errorf("log output lost field %q: %s", field, logged)
		}
	}
}

func TestNewDefaultLoggerRegistersRuntimeSecrets(t *testing.T) {
	t.Parallel()

	const canary = "default-logger-canary-4f29"
	var output bytes.Buffer
	logger, err := NewDefaultLogger(&output, canary)
	if err != nil {
		t.Fatalf("new default logger: %v", err)
	}
	logger.Info("connection failed with " + canary)

	if strings.Contains(output.String(), canary) {
		t.Fatalf("default logger exposed runtime secret: %s", output.String())
	}
	if !strings.Contains(output.String(), redact.RedactedValue) {
		t.Fatalf("default logger has no redaction marker: %s", output.String())
	}
}
