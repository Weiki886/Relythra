package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
