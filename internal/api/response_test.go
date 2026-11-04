package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Weiki886/relythra/internal/domain"
)

func TestWithRequestIDReusesSafeValue(t *testing.T) {
	var seen string
	handler := WithRequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = RequestIDFromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("X-Request-ID", "upstream.request-123")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if seen != "upstream.request-123" {
		t.Fatalf("context request ID = %q", seen)
	}
	if response.Header().Get("X-Request-ID") != seen {
		t.Fatalf("response request ID = %q, want %q", response.Header().Get("X-Request-ID"), seen)
	}
}

func TestWithRequestIDReplacesUnsafeOrOversizedValue(t *testing.T) {
	for _, requestID := range []string{
		"contains a space",
		"line\nbreak",
		stringsOfLength(129),
	} {
		t.Run(requestID[:min(12, len(requestID))], func(t *testing.T) {
			var seen string
			handler := WithRequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen = RequestIDFromContext(r.Context())
				w.WriteHeader(http.StatusNoContent)
			}))
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			request.Header.Set("X-Request-ID", requestID)
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			if seen == requestID {
				t.Fatalf("unsafe request ID %q was reused", requestID)
			}
			if !domain.IsID(seen) {
				t.Fatalf("generated request ID %q is not a UUID", seen)
			}
			if response.Header().Get("X-Request-ID") != seen {
				t.Fatalf("response request ID = %q, want %q", response.Header().Get("X-Request-ID"), seen)
			}
		})
	}
}

func TestRequestIDContextHelperReturnsEmptyWithoutMiddleware(t *testing.T) {
	if got := RequestIDFromContext(context.Background()); got != "" {
		t.Fatalf("request ID = %q, want empty", got)
	}
}

func stringsOfLength(length int) string {
	result := make([]byte, length)
	for index := range result {
		result[index] = 'a'
	}
	return string(result)
}
