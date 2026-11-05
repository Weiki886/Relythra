package main

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Weiki886/relythra/internal/config"
	"github.com/Weiki886/relythra/internal/security/audit"
	"github.com/Weiki886/relythra/internal/security/outbound"
	"github.com/Weiki886/relythra/internal/security/redact"
	"github.com/Weiki886/relythra/internal/security/secrets"
)

func TestNewHandlerMountsHealthAndDomainAPI(t *testing.T) {
	handler := newHandler(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	t.Run("liveness", func(t *testing.T) {
		response := httptest.NewRecorder()
		handler.ServeHTTP(
			response,
			httptest.NewRequest(http.MethodGet, "/health/live", nil),
		)

		if response.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", response.Code, response.Body.String())
		}
		if !strings.Contains(response.Body.String(), `"status":"ok"`) {
			t.Fatalf("body = %s", response.Body.String())
		}
	})

	t.Run("domain API", func(t *testing.T) {
		response := httptest.NewRecorder()
		handler.ServeHTTP(
			response,
			httptest.NewRequest(http.MethodGet, "/api/v1/unknown", nil),
		)

		if response.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404: %s", response.Code, response.Body.String())
		}
		if response.Header().Get("X-Request-ID") == "" {
			t.Error("domain API response is missing X-Request-ID")
		}
		if !strings.Contains(response.Body.String(), `"code":"not_found"`) {
			t.Fatalf("body = %s", response.Body.String())
		}
	})
}

type mainCapturingAuditRecorder struct {
	events []audit.Event
}

func (recorder *mainCapturingAuditRecorder) Record(
	_ context.Context,
	event audit.Event,
) (int64, error) {
	recorder.events = append(recorder.events, event)
	return int64(len(recorder.events)), nil
}

func TestConfiguredHandlerWiresManagementAuditRecorder(t *testing.T) {
	t.Parallel()

	token, err := secrets.New("management-token-canary")
	if err != nil {
		t.Fatalf("new management token: %v", err)
	}
	recorder := &mainCapturingAuditRecorder{}
	handler, err := newConfiguredHandlerWithAuditRecorderFactory(
		new(sql.DB),
		config.API{
			AdminToken: token,
			DataSourcePolicy: outbound.PolicyConfig{
				AllowedHosts: []string{"metrics.example.test"},
				AllowedPorts: []int{443},
			},
		},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		func(*sql.DB, *redact.Filter) (audit.Recorder, error) {
			return recorder, nil
		},
	)
	if err != nil {
		t.Fatalf("new configured handler: %v", err)
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(
		response,
		httptest.NewRequest(http.MethodGet, "/api/v1/data-source-mappings", nil),
	)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401: %s", response.Code, response.Body.String())
	}
	if len(recorder.events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(recorder.events))
	}
	event := recorder.events[0]
	if event.Result != audit.ResultDenied ||
		event.Parameters["error_code"] != "unauthorized" {
		t.Fatalf("audit event = %#v", event)
	}
}
