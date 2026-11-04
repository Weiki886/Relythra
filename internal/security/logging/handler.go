// Package logging wraps slog with the project's fail-closed redaction filter.
package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	"github.com/Weiki886/relythra/internal/security/redact"
)

const failedRedaction = "[REDACTION_FAILED]"

type Handler struct {
	inner          slog.Handler
	filter         *redact.Filter
	sensitiveGroup bool
}

func NewHandler(inner slog.Handler, filter *redact.Filter) slog.Handler {
	if inner == nil {
		inner = slog.NewTextHandler(io.Discard, nil)
	}
	if filter == nil {
		filter, _ = redact.NewFilter()
	}
	return &Handler{inner: inner, filter: filter}
}

func NewDefaultLogger(writer io.Writer, knownSecrets ...string) (*slog.Logger, error) {
	filter, err := redact.NewFilter(knownSecrets...)
	if err != nil {
		return nil, err
	}
	return slog.New(NewHandler(slog.NewJSONHandler(writer, nil), filter)), nil
}

func (handler *Handler) Enabled(ctx context.Context, level slog.Level) bool {
	return handler.inner.Enabled(ctx, level)
}

func (handler *Handler) Handle(ctx context.Context, record slog.Record) error {
	safeMessage, err := handler.filter.Text(ctx, record.Message)
	if err != nil {
		safeMessage = failedRedaction
	}
	safeRecord := slog.NewRecord(record.Time, record.Level, safeMessage, record.PC)
	record.Attrs(func(attribute slog.Attr) bool {
		safeRecord.AddAttrs(handler.sanitizeAttribute(ctx, attribute))
		return true
	})
	return handler.inner.Handle(ctx, safeRecord)
}

func (handler *Handler) WithAttrs(attributes []slog.Attr) slog.Handler {
	safe := make([]slog.Attr, 0, len(attributes))
	for _, attribute := range attributes {
		safe = append(safe, handler.sanitizeAttribute(context.Background(), attribute))
	}
	return &Handler{
		inner:          handler.inner.WithAttrs(safe),
		filter:         handler.filter,
		sensitiveGroup: handler.sensitiveGroup,
	}
}

func (handler *Handler) WithGroup(name string) slog.Handler {
	safeName, err := handler.filter.Text(context.Background(), name)
	if err != nil {
		safeName = failedRedaction
	}
	return &Handler{
		inner:          handler.inner.WithGroup(safeName),
		filter:         handler.filter,
		sensitiveGroup: handler.sensitiveGroup || redact.IsSensitiveKey(name),
	}
}

func (handler *Handler) sanitizeAttribute(ctx context.Context, attribute slog.Attr) slog.Attr {
	safeKey, err := handler.filter.Text(ctx, attribute.Key)
	if err != nil {
		return slog.String(failedRedaction, failedRedaction)
	}
	attribute.Key = safeKey
	attribute.Value = attribute.Value.Resolve()
	if handler.sensitiveGroup || redact.IsSensitiveKey(attribute.Key) {
		return slog.String(attribute.Key, redact.RedactedValue)
	}
	switch attribute.Value.Kind() {
	case slog.KindString:
		safe, err := handler.filter.Text(ctx, attribute.Value.String())
		if err != nil {
			return slog.String(attribute.Key, failedRedaction)
		}
		return slog.String(attribute.Key, safe)
	case slog.KindGroup:
		group := attribute.Value.Group()
		safeGroup := make([]slog.Attr, 0, len(group))
		for _, child := range group {
			safeGroup = append(safeGroup, handler.sanitizeAttribute(ctx, child))
		}
		return slog.Group(attribute.Key, attrsToAny(safeGroup)...)
	case slog.KindAny:
		value := attribute.Value.Any()
		if _, isError := value.(error); isError {
			safe, err := handler.filter.Text(ctx, fmt.Sprint(value))
			if err != nil {
				return slog.String(attribute.Key, failedRedaction)
			}
			return slog.String(attribute.Key, safe)
		}
		if safeJSON, err := handler.filter.JSON(ctx, value); err == nil {
			return slog.Any(attribute.Key, safeJSON)
		}
		safe, err := handler.filter.Text(ctx, fmt.Sprint(value))
		if err != nil {
			return slog.String(attribute.Key, failedRedaction)
		}
		return slog.String(attribute.Key, safe)
	default:
		return attribute
	}
}

func attrsToAny(attributes []slog.Attr) []any {
	values := make([]any, len(attributes))
	for index, attribute := range attributes {
		values[index] = attribute
	}
	return values
}
