package health_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Weiki886/relythra/internal/health"
)

type checkerFunc func(context.Context) error

func (fn checkerFunc) PingContext(ctx context.Context) error { return fn(ctx) }

func TestLivenessDoesNotCheckDependencies(t *testing.T) {
	t.Parallel()

	called := false
	handler := health.NewHandler(checkerFunc(func(context.Context) error {
		called = true
		return errors.New("database unavailable")
	}), slog.New(slog.NewTextHandler(io.Discard, nil)))

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health/live", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	if called {
		t.Fatal("liveness called dependency checker")
	}
}

func TestReadinessReportsReadyWhenDatabaseIsAvailable(t *testing.T) {
	t.Parallel()

	handler := health.NewHandler(checkerFunc(func(context.Context) error {
		return nil
	}), slog.New(slog.NewTextHandler(io.Discard, nil)))

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health/ready", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"status":"ready"`) {
		t.Errorf("body = %s, want ready status", response.Body.String())
	}
}

func TestReadinessHidesDependencyError(t *testing.T) {
	t.Parallel()

	const sensitiveError = "password=do-not-expose"
	handler := health.NewHandler(checkerFunc(func(context.Context) error {
		return errors.New(sensitiveError)
	}), slog.New(slog.NewTextHandler(io.Discard, nil)))

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health/ready", nil))

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", response.Code)
	}
	if strings.Contains(response.Body.String(), sensitiveError) {
		t.Fatalf("body leaked dependency error: %s", response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"error":"dependency unavailable"`) {
		t.Errorf("body = %s, want stable error", response.Body.String())
	}
}
