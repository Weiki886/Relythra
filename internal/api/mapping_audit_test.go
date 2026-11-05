package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Weiki886/relythra/internal/connector/health"
	"github.com/Weiki886/relythra/internal/security/audit"
	"github.com/Weiki886/relythra/internal/security/secrets"
)

type capturingAuditRecorder struct {
	mu     sync.Mutex
	events []audit.Event
	err    error
}

func (recorder *capturingAuditRecorder) Record(
	_ context.Context,
	event audit.Event,
) (int64, error) {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	recorder.events = append(recorder.events, event)
	if recorder.err != nil {
		return 0, recorder.err
	}
	return int64(len(recorder.events)), nil
}

func (recorder *capturingAuditRecorder) lastEvent(t *testing.T) audit.Event {
	t.Helper()
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if len(recorder.events) == 0 {
		t.Fatal("no audit event was recorded")
	}
	return recorder.events[len(recorder.events)-1]
}

func (recorder *capturingAuditRecorder) eventCount() int {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return len(recorder.events)
}

func TestManagementAuthorizationFailuresAreAudited(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		authenticator AdminAuthenticator
		authorization string
		wantStatus    int
		wantCode      string
		wantErrorCode string
	}{
		{
			name:          "missing token",
			authenticator: mustAuthenticator(t, "management-token-canary"),
			wantStatus:    http.StatusUnauthorized,
			wantCode:      "unauthorized",
			wantErrorCode: "unauthorized",
		},
		{
			name:          "wrong token",
			authenticator: mustAuthenticator(t, "management-token-canary"),
			authorization: "Bearer wrong-token",
			wantStatus:    http.StatusUnauthorized,
			wantCode:      "unauthorized",
			wantErrorCode: "unauthorized",
		},
		{
			name:          "token not configured",
			authenticator: ManagementUnavailableAuthenticator{},
			wantStatus:    http.StatusServiceUnavailable,
			wantCode:      "management_unavailable",
			wantErrorCode: "management_unavailable",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			recorder := &capturingAuditRecorder{}
			handler := newMappingTestHandlerWithDependencies(
				t,
				&fakeMappingStore{},
				nil,
				testCase.authenticator,
				recorder,
			)
			request := httptest.NewRequest(
				http.MethodGet,
				"/api/v1/data-source-mappings",
				nil,
			)
			if testCase.authorization != "" {
				request.Header.Set("Authorization", testCase.authorization)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)

			assertErrorResponse(t, response, testCase.wantStatus, testCase.wantCode)
			event := recorder.lastEvent(t)
			if event.Action != audit.ActionConfigChanged ||
				event.Result != audit.ResultDenied ||
				event.Parameters["error_code"] != testCase.wantErrorCode {
				t.Fatalf("audit event = %#v", event)
			}
		})
	}
}

func TestDatasourceManagementOperationsAreAudited(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		method       string
		path         string
		body         string
		wantAction   audit.Action
		wantObjectID string
	}{
		{
			name:       "list",
			method:     http.MethodGet,
			path:       "/api/v1/data-source-mappings",
			wantAction: audit.ActionConfigChanged,
		},
		{
			name:   "create",
			method: http.MethodPost,
			path:   "/api/v1/data-source-mappings",
			body: `{
				"project_id":"66666666-6666-4666-8666-666666666666",
				"service_id":"77777777-7777-4777-8777-777777777777",
				"environment":"prod",
				"source_type":"PROMETHEUS",
				"public_settings":{"endpoint":"https://metrics.example.test"},
				"template_ids":["prometheus.service.latency.v1"],
				"secret_ref":"env:MAPPING_SECRET_CANARY"
			}`,
			wantAction:   audit.ActionConfigChanged,
			wantObjectID: mappingID,
		},
		{
			name:       "read",
			method:     http.MethodGet,
			path:       "/api/v1/data-source-mappings/" + mappingID,
			wantAction: audit.ActionConfigChanged,
		},
		{
			name:       "update",
			method:     http.MethodPut,
			path:       "/api/v1/data-source-mappings/" + mappingID,
			body:       validUpdateBody(),
			wantAction: audit.ActionConfigChanged,
		},
		{
			name:       "delete",
			method:     http.MethodDelete,
			path:       "/api/v1/data-source-mappings/" + mappingID + "?expected_version=3",
			wantAction: audit.ActionConfigChanged,
		},
		{
			name:       "validate",
			method:     http.MethodPost,
			path:       "/api/v1/data-source-mappings/" + mappingID + "/validate",
			wantAction: audit.ActionConfigChanged,
		},
		{
			name:   "query plan",
			method: http.MethodPost,
			path:   "/api/v1/data-source-mappings/" + mappingID + "/queries",
			body: `{
				"template_id":"prometheus.service.latency.v1",
				"parameters":{
					"service":"checkout-api",
					"start":"2026-11-04T05:00:00Z",
					"end":"2026-11-04T06:00:00Z"
				}
			}`,
			wantAction: audit.ActionToolCalled,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			recorder := &capturingAuditRecorder{}
			handler := newMappingTestHandlerWithDependencies(
				t,
				&fakeMappingStore{mapping: validPrometheusMapping()},
				nil,
				mustAuthenticator(t, "management-token-canary"),
				recorder,
			)
			request := authenticatedJSONRequest(testCase.method, testCase.path, testCase.body)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)

			if response.Code < http.StatusOK || response.Code >= http.StatusMultipleChoices {
				t.Fatalf("status = %d: %s", response.Code, response.Body.String())
			}
			event := recorder.lastEvent(t)
			if event.Action != testCase.wantAction ||
				event.Result != audit.ResultSucceeded ||
				event.ObjectType != "data_source_mapping" ||
				event.ObjectID == "" ||
				(testCase.wantObjectID != "" && event.ObjectID != testCase.wantObjectID) ||
				event.RequestID == "" ||
				event.OccurredAt.IsZero() {
				t.Fatalf("audit event = %#v", event)
			}
			assertAuditParametersAreBounded(t, event.Parameters)
		})
	}
}

func TestRejectedConnectionWritesAuditWithoutSecret(t *testing.T) {
	t.Parallel()

	const canary = "audit-mapping-canary-f61a"
	recorder := &capturingAuditRecorder{}
	mapping := validPrometheusMapping()
	mapping.SecretRef = "env:" + strings.ToUpper(strings.ReplaceAll(canary, "-", "_"))
	tester := &fakeConnectionTester{
		err: &health.TestError{Category: health.ErrorPolicyRejected},
	}
	handler := newMappingTestHandlerWithDependencies(
		t,
		&fakeMappingStore{mapping: mapping},
		tester,
		mustAuthenticator(t, "management-token-canary"),
		recorder,
	)
	request := authenticatedJSONRequest(
		http.MethodPost,
		"/api/v1/data-source-mappings/"+mappingID+"/connection-test",
		"",
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	assertErrorResponse(t, response, http.StatusUnprocessableEntity, "policy_rejected")
	event := recorder.lastEvent(t)
	if event.Action != audit.ActionConnectionTested ||
		event.Result != audit.ResultDenied ||
		event.Parameters["error_code"] != "policy_rejected" ||
		event.Parameters["source_type"] != "PROMETHEUS" {
		t.Fatalf("audit event = %#v", event)
	}
	if strings.Contains(strings.ToLower(eventText(event)), "audit-mapping-canary") ||
		strings.Contains(strings.ToLower(eventText(event)), "secret_ref") {
		t.Fatalf("audit event exposed secret material: %#v", event)
	}
	assertAuditParametersAreBounded(t, event.Parameters)
}

func TestAuditFailureStopsExternalConnectionTest(t *testing.T) {
	t.Parallel()

	recorder := &capturingAuditRecorder{err: errors.New("audit store unavailable")}
	tester := &fakeConnectionTester{result: health.Result{
		Connectivity: health.ConnectivityConnected,
		Permission:   health.PermissionGranted,
	}}
	handler := newMappingTestHandlerWithDependencies(
		t,
		&fakeMappingStore{mapping: validPrometheusMapping()},
		tester,
		mustAuthenticator(t, "management-token-canary"),
		recorder,
	)
	request := authenticatedJSONRequest(
		http.MethodPost,
		"/api/v1/data-source-mappings/"+mappingID+"/connection-test",
		"",
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	assertErrorResponse(t, response, http.StatusInternalServerError, "internal_error")
	if tester.calls != 0 {
		t.Fatalf("external connection tester calls = %d, want 0", tester.calls)
	}
	if recorder.eventCount() != 1 {
		t.Fatalf("audit event count = %d, want 1 failed preflight", recorder.eventCount())
	}
}

func TestRemotePermissionDenialIsAuditedAsDenied(t *testing.T) {
	t.Parallel()

	recorder := &capturingAuditRecorder{}
	tester := &fakeConnectionTester{result: health.Result{
		Connectivity: health.ConnectivityConnected,
		Permission:   health.PermissionDenied,
	}}
	handler := newMappingTestHandlerWithDependencies(
		t,
		&fakeMappingStore{mapping: validPrometheusMapping()},
		tester,
		mustAuthenticator(t, "management-token-canary"),
		recorder,
	)
	request := authenticatedJSONRequest(
		http.MethodPost,
		"/api/v1/data-source-mappings/"+mappingID+"/connection-test",
		"",
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	event := recorder.lastEvent(t)
	if event.Action != audit.ActionConnectionTested ||
		event.Result != audit.ResultDenied ||
		event.Parameters["error_code"] != "permission_denied" {
		t.Fatalf("audit event = %#v", event)
	}
}

func TestSuccessfulConnectionTestIsAudited(t *testing.T) {
	t.Parallel()

	recorder := &capturingAuditRecorder{}
	tester := &fakeConnectionTester{result: health.Result{
		Connectivity: health.ConnectivityConnected,
		Permission:   health.PermissionGranted,
		Version:      "3.5.0",
	}}
	handler := newMappingTestHandlerWithDependencies(
		t,
		&fakeMappingStore{mapping: validPrometheusMapping()},
		tester,
		mustAuthenticator(t, "management-token-canary"),
		recorder,
	)
	request := authenticatedJSONRequest(
		http.MethodPost,
		"/api/v1/data-source-mappings/"+mappingID+"/connection-test",
		"",
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if recorder.eventCount() != 2 {
		t.Fatalf("audit event count = %d, want preflight and final", recorder.eventCount())
	}
	event := recorder.lastEvent(t)
	if event.Action != audit.ActionConnectionTested ||
		event.Result != audit.ResultSucceeded ||
		event.Parameters["error_code"] != nil {
		t.Fatalf("audit event = %#v", event)
	}
}

func TestConnectionTestFailuresHaveStableCategories(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		category      health.ErrorCategory
		wantStatus    int
		wantErrorCode string
	}{
		{
			name:          "TLS",
			category:      health.ErrorTLS,
			wantStatus:    http.StatusBadGateway,
			wantErrorCode: "tls_error",
		},
		{
			name:          "network",
			category:      health.ErrorNetwork,
			wantStatus:    http.StatusBadGateway,
			wantErrorCode: "network_error",
		},
		{
			name:          "remote unavailable",
			category:      health.ErrorRemoteUnavailable,
			wantStatus:    http.StatusBadGateway,
			wantErrorCode: "remote_unavailable",
		},
		{
			name:          "invalid response",
			category:      health.ErrorInvalidResponse,
			wantStatus:    http.StatusBadGateway,
			wantErrorCode: "invalid_response",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			recorder := &capturingAuditRecorder{}
			tester := &fakeConnectionTester{
				err: &health.TestError{Category: testCase.category},
			}
			handler := newMappingTestHandlerWithDependencies(
				t,
				&fakeMappingStore{mapping: validPrometheusMapping()},
				tester,
				mustAuthenticator(t, "management-token-canary"),
				recorder,
			)
			request := authenticatedJSONRequest(
				http.MethodPost,
				"/api/v1/data-source-mappings/"+mappingID+"/connection-test",
				"",
			)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)

			assertErrorResponse(t, response, testCase.wantStatus, testCase.wantErrorCode)
			event := recorder.lastEvent(t)
			if event.Result != audit.ResultFailed ||
				event.Parameters["error_code"] != testCase.wantErrorCode {
				t.Fatalf("audit event = %#v", event)
			}
		})
	}
}

func mustAuthenticator(t *testing.T, token string) AdminAuthenticator {
	t.Helper()
	secret, err := secrets.New(token)
	if err != nil {
		t.Fatalf("new token: %v", err)
	}
	authenticator, err := NewBearerTokenAuthenticator(secret)
	if err != nil {
		t.Fatalf("new authenticator: %v", err)
	}
	return authenticator
}

func assertAuditParametersAreBounded(t *testing.T, parameters map[string]any) {
	t.Helper()
	allowed := map[string]bool{
		"source_type": true,
		"error_code":  true,
		"template_id": true,
		"elapsed_ms":  true,
	}
	for key, value := range parameters {
		if !allowed[key] {
			t.Errorf("unexpected audit parameter %q = %#v", key, value)
		}
	}
}

func eventText(event audit.Event) string {
	return fmt.Sprintf("%+v", event)
}
