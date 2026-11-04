package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Weiki886/relythra/internal/store"
)

const (
	reportID     = "11111111-1111-4111-8111-111111111111"
	evidenceID   = "22222222-2222-4222-8222-222222222222"
	deploymentID = "33333333-3333-4333-8333-333333333333"
)

type fakeReader struct {
	report     store.Report
	evidence   store.Evidence
	deployment store.DeploymentEvent
	err        error
	calls      int
}

func (reader *fakeReader) GetReport(context.Context, string) (store.Report, error) {
	reader.calls++
	return reader.report, reader.err
}

func (reader *fakeReader) GetEvidence(context.Context, string) (store.Evidence, error) {
	reader.calls++
	return reader.evidence, reader.err
}

func (reader *fakeReader) GetDeploymentEvent(
	context.Context,
	string,
) (store.DeploymentEvent, error) {
	reader.calls++
	return reader.deployment, reader.err
}

func TestReadEndpointsReturnDataWithMatchingRequestID(t *testing.T) {
	reader := &fakeReader{
		report: store.Report{
			ID:              reportID,
			InvestigationID: "44444444-4444-4444-8444-444444444444",
			ResultStatus:    "COMPLETED",
			Confidence:      "HIGH",
			Summary:         "Deployment introduced a slow query",
		},
		evidence: store.Evidence{
			ID:              evidenceID,
			InvestigationID: "44444444-4444-4444-8444-444444444444",
			SourceType:      "PROMETHEUS",
			EntityType:      "service",
			Completeness:    "COMPLETE",
		},
		deployment: store.DeploymentEvent{
			ID:          deploymentID,
			Environment: "prod",
			CommitSHA:   strings.Repeat("a", 40),
		},
	}
	handler := NewHandler(reader, discardLogger())

	for _, testCase := range []struct {
		name string
		path string
		id   string
	}{
		{name: "report", path: "/api/v1/reports/" + reportID, id: reportID},
		{name: "evidence", path: "/api/v1/evidence/" + evidenceID, id: evidenceID},
		{name: "deployment", path: "/api/v1/deployments/" + deploymentID, id: deploymentID},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, testCase.path, nil)
			request.Header.Set("X-Request-ID", "caller-request-123")
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200: %s", response.Code, response.Body.String())
			}
			if response.Header().Get("X-Request-ID") != "caller-request-123" {
				t.Fatalf("response request ID = %q", response.Header().Get("X-Request-ID"))
			}
			var envelope struct {
				Data      map[string]any `json:"data"`
				RequestID string         `json:"request_id"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if envelope.RequestID != "caller-request-123" {
				t.Errorf("envelope request ID = %q", envelope.RequestID)
			}
			if envelope.Data["id"] != testCase.id {
				t.Errorf("data ID = %v, want %s", envelope.Data["id"], testCase.id)
			}
			if strings.Contains(response.Body.String(), "secret_ref") {
				t.Error("response exposed secret_ref")
			}
		})
	}
}

func TestReadEndpointsReturnStableErrors(t *testing.T) {
	t.Run("invalid ID", func(t *testing.T) {
		reader := &fakeReader{}
		response := serve(
			NewHandler(reader, discardLogger()),
			http.MethodGet,
			"/api/v1/reports/not-a-uuid",
		)
		assertErrorResponse(t, response, http.StatusBadRequest, "invalid_request")
		if reader.calls != 0 {
			t.Fatalf("store calls = %d, want 0", reader.calls)
		}
	})

	t.Run("not found", func(t *testing.T) {
		response := serve(
			NewHandler(&fakeReader{err: store.ErrNotFound}, discardLogger()),
			http.MethodGet,
			"/api/v1/evidence/"+evidenceID,
		)
		assertErrorResponse(t, response, http.StatusNotFound, "not_found")
	})

	t.Run("dependency error is redacted", func(t *testing.T) {
		response := serve(
			NewHandler(
				&fakeReader{err: errors.New("postgres://admin:secret@database:5432/relythra")},
				discardLogger(),
			),
			http.MethodGet,
			"/api/v1/deployments/"+deploymentID,
		)
		assertErrorResponse(t, response, http.StatusInternalServerError, "internal_error")
		if strings.Contains(response.Body.String(), "secret") ||
			strings.Contains(response.Body.String(), "postgres://") {
			t.Fatal("response exposed internal dependency details")
		}
	})

	t.Run("method not allowed", func(t *testing.T) {
		response := serve(
			NewHandler(&fakeReader{}, discardLogger()),
			http.MethodPost,
			"/api/v1/reports/"+reportID,
		)
		assertErrorResponse(t, response, http.StatusMethodNotAllowed, "method_not_allowed")
		if response.Header().Get("Allow") != http.MethodGet {
			t.Errorf("Allow = %q, want GET", response.Header().Get("Allow"))
		}
	})

	t.Run("unknown route", func(t *testing.T) {
		response := serve(
			NewHandler(&fakeReader{}, discardLogger()),
			http.MethodGet,
			"/api/v1/unknown",
		)
		assertErrorResponse(t, response, http.StatusNotFound, "not_found")
	})
}

func serve(handler http.Handler, method string, path string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func assertErrorResponse(
	t *testing.T,
	response *httptest.ResponseRecorder,
	status int,
	code string,
) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("status = %d, want %d: %s", response.Code, status, response.Body.String())
	}
	if response.Header().Get("Content-Type") != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q", response.Header().Get("Content-Type"))
	}
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if envelope.Error.Code != code {
		t.Errorf("error code = %q, want %q", envelope.Error.Code, code)
	}
	if envelope.Error.Message == "" {
		t.Error("error message is empty")
	}
	if envelope.RequestID == "" ||
		envelope.RequestID != response.Header().Get("X-Request-ID") {
		t.Errorf("request ID header/body mismatch: %q/%q",
			response.Header().Get("X-Request-ID"), envelope.RequestID)
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
