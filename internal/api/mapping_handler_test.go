package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Weiki886/relythra/internal/connector/health"
	"github.com/Weiki886/relythra/internal/datasource"
	"github.com/Weiki886/relythra/internal/domain"
	"github.com/Weiki886/relythra/internal/security/audit"
	"github.com/Weiki886/relythra/internal/security/secrets"
	"github.com/Weiki886/relythra/internal/store"
)

const mappingID = "55555555-5555-4555-8555-555555555555"

type fakeMappingStore struct {
	mapping       store.DataSourceMapping
	list          []store.DataSourceMapping
	createInput   store.CreateDataSourceMappingInput
	updateInput   store.UpdateDataSourceMappingInput
	deleteID      string
	deleteVersion int
	listLimit     int
	listOffset    int
	err           error
	createIssues  []datasource.ValidationIssue
	updateIssues  []datasource.ValidationIssue
	calls         int
}

func (repository *fakeMappingStore) CreateDataSourceMapping(
	_ context.Context,
	input store.CreateDataSourceMappingInput,
) (store.DataSourceMapping, []datasource.ValidationIssue, error) {
	repository.calls++
	repository.createInput = input
	return repository.mapping, repository.createIssues, repository.err
}

func (repository *fakeMappingStore) GetDataSourceMapping(
	context.Context,
	string,
) (store.DataSourceMapping, error) {
	repository.calls++
	return repository.mapping, repository.err
}

func (repository *fakeMappingStore) ListDataSourceMappingsPage(
	_ context.Context,
	_ string,
	_ string,
	_ string,
	limit int,
	offset int,
) ([]store.DataSourceMapping, error) {
	repository.calls++
	repository.listLimit = limit
	repository.listOffset = offset
	return repository.list, repository.err
}

func (repository *fakeMappingStore) UpdateDataSourceMapping(
	_ context.Context,
	input store.UpdateDataSourceMappingInput,
) (store.DataSourceMapping, []datasource.ValidationIssue, error) {
	repository.calls++
	repository.updateInput = input
	return repository.mapping, repository.updateIssues, repository.err
}

func (repository *fakeMappingStore) DeleteDataSourceMapping(
	_ context.Context,
	id string,
	version int,
) error {
	repository.calls++
	repository.deleteID = id
	repository.deleteVersion = version
	return repository.err
}

type fakeConnectionTester struct {
	result health.Result
	err    error
	target health.Target
	calls  int
	block  <-chan struct{}
}

func (tester *fakeConnectionTester) Test(
	ctx context.Context,
	target health.Target,
) (health.Result, error) {
	tester.calls++
	tester.target = target
	if tester.block != nil {
		select {
		case <-tester.block:
		case <-ctx.Done():
			return health.Result{}, ctx.Err()
		}
	}
	return tester.result, tester.err
}

func TestManagementEndpointsRequireBearerToken(t *testing.T) {
	t.Parallel()

	handler := newMappingTestHandler(
		t,
		&fakeMappingStore{},
		nil,
		"management-token-canary",
	)

	t.Run("missing token", func(t *testing.T) {
		response := serve(handler, http.MethodGet, "/api/v1/data-source-mappings")
		assertErrorResponse(t, response, http.StatusUnauthorized, "unauthorized")
	})

	t.Run("wrong token", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/data-source-mappings", nil)
		request.Header.Set("Authorization", "Bearer wrong-token")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		assertErrorResponse(t, response, http.StatusUnauthorized, "unauthorized")
	})
}

func TestManagementEndpointsAreUnavailableWithoutConfiguredToken(t *testing.T) {
	t.Parallel()

	handler := NewHandlerWithDependencies(
		Dependencies{
			Reader:             &fakeReader{},
			Mappings:           &fakeMappingStore{},
			AdminAuthenticator: ManagementUnavailableAuthenticator{},
		},
		discardLogger(),
	)
	response := serve(handler, http.MethodGet, "/api/v1/data-source-mappings")
	assertErrorResponse(t, response, http.StatusServiceUnavailable, "management_unavailable")
}

func TestCreateMappingAcceptsStrictJSONAndDoesNotEchoSecretReference(t *testing.T) {
	t.Parallel()

	const canary = "env:MAPPING_SECRET_CANARY"
	repository := &fakeMappingStore{
		mapping: validPrometheusMapping(),
	}
	handler := newMappingTestHandler(t, repository, nil, "management-token-canary")
	request := authenticatedJSONRequest(
		http.MethodPost,
		"/api/v1/data-source-mappings",
		`{
			"project_id":"66666666-6666-4666-8666-666666666666",
			"service_id":"77777777-7777-4777-8777-777777777777",
			"environment":"prod",
			"source_type":"PROMETHEUS",
			"public_settings":{"endpoint":"https://metrics.example.test"},
			"template_ids":["prometheus.service.latency.v1"],
			"secret_ref":"`+canary+`"
		}`,
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", response.Code, response.Body.String())
	}
	if repository.createInput.SecretRef != canary {
		t.Fatalf("stored secret reference = %q", repository.createInput.SecretRef)
	}
	if strings.Contains(response.Body.String(), canary) ||
		strings.Contains(response.Body.String(), "secret_ref") {
		t.Fatalf("response exposed secret reference: %s", response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"secret_status":"configured"`) {
		t.Fatalf("response missing configured status: %s", response.Body.String())
	}
}

func TestMappingWritesRejectUnknownFieldsAndMultipleDocuments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
	}{
		{
			name: "unknown field",
			body: `{
				"project_id":"66666666-6666-4666-8666-666666666666",
				"service_id":"77777777-7777-4777-8777-777777777777",
				"environment":"prod",
				"source_type":"PROMETHEUS",
				"public_settings":{"endpoint":"https://metrics.example.test"},
				"template_ids":["prometheus.service.latency.v1"],
				"promql":"up"
			}`,
		},
		{
			name: "multiple documents",
			body: `{"source_type":"PROMETHEUS"}{"source_type":"LOKI"}`,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			repository := &fakeMappingStore{mapping: validPrometheusMapping()}
			handler := newMappingTestHandler(t, repository, nil, "management-token-canary")
			request := authenticatedJSONRequest(
				http.MethodPost,
				"/api/v1/data-source-mappings",
				testCase.body,
			)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)

			assertErrorResponse(t, response, http.StatusUnprocessableEntity, "validation_failed")
			if repository.calls != 0 {
				t.Fatalf("store calls = %d, want 0", repository.calls)
			}
		})
	}
}

func TestQueryEndpointRejectsRawPromQL(t *testing.T) {
	t.Parallel()

	repository := &fakeMappingStore{mapping: validPrometheusMapping()}
	handler := newMappingTestHandler(t, repository, nil, "management-token-canary")
	request := authenticatedJSONRequest(
		http.MethodPost,
		"/api/v1/data-source-mappings/"+mappingID+"/queries",
		`{
			"template_id":"prometheus.service.latency.v1",
			"parameters":{
				"service":"checkout-api",
				"environment":"prod",
				"start":"2026-11-04T05:00:00Z",
				"end":"2026-11-04T06:00:00Z",
				"promql":"up"
			}
		}`,
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	assertErrorResponse(t, response, http.StatusUnprocessableEntity, "validation_failed")
	var envelope struct {
		Error struct {
			Details []datasource.ValidationIssue `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !hasValidationIssue(envelope.Error.Details, "parameters.promql", "unsupported_parameter") {
		t.Fatalf("details = %#v", envelope.Error.Details)
	}
}

func TestQueryEndpointReturnsNormalizedFixedTemplatePlan(t *testing.T) {
	t.Parallel()

	repository := &fakeMappingStore{mapping: validPrometheusMapping()}
	handler := newMappingTestHandler(t, repository, nil, "management-token-canary")
	request := authenticatedJSONRequest(
		http.MethodPost,
		"/api/v1/data-source-mappings/"+mappingID+"/queries",
		`{
			"template_id":"prometheus.service.latency.v1",
			"parameters":{
				"service":"checkout-api",
				"environment":"prod",
				"start":"2026-11-04T05:00:00Z",
				"end":"2026-11-04T06:00:00Z",
				"max_series":40,
				"max_points":200
			}
		}`,
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", response.Code, response.Body.String())
	}
	for _, expected := range []string{
		`"template_id":"prometheus.service.latency.v1"`,
		`"max_series":40`,
		`"environment":"prod"`,
		`"query_type":"range"`,
		`"step":30000000000`,
		`"max_points":200`,
	} {
		if !strings.Contains(response.Body.String(), expected) {
			t.Errorf("response missing %s: %s", expected, response.Body.String())
		}
	}
}

func TestQueryEndpointReturnsNormalizedLokiPlan(t *testing.T) {
	t.Parallel()

	mapping := validPrometheusMapping()
	mapping.SourceType = datasource.SourceLoki
	mapping.PublicSettings.Endpoint = "https://logs.example.test"
	mapping.Templates = []datasource.Template{{
		ID: "loki.service.errors.v1", Version: 1, Source: datasource.SourceLoki,
		Purpose: "Read bounded error log entries.", ResultSchema: "log.entry_list.v1",
		AllowedParameters: []string{"cursor", "direction", "end", "environment", "limit", "service", "severity", "start"},
	}}
	handler := newMappingTestHandler(t, &fakeMappingStore{mapping: mapping}, nil, "management-token-canary")
	request := authenticatedJSONRequest(
		http.MethodPost,
		"/api/v1/data-source-mappings/"+mappingID+"/queries",
		`{
			"template_id":"loki.service.errors.v1",
			"parameters":{
				"service":"checkout-api",
				"environment":"prod",
				"direction":" FORWARD ",
				"severity":" ERROR ",
				"start":"2026-11-04T05:00:00+08:00",
				"end":"2026-11-04T06:00:00+08:00",
				"limit":50
			}
		}`,
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", response.Code, response.Body.String())
	}
	for _, expected := range []string{
		`"direction":"forward"`,
		`"severity":"error"`,
		`"environment":"prod"`,
		`"start":"2026-11-03T21:00:00Z"`,
		`"end":"2026-11-03T22:00:00Z"`,
		`"limit":50`,
	} {
		if !strings.Contains(response.Body.String(), expected) {
			t.Errorf("response missing %s: %s", expected, response.Body.String())
		}
	}
}

func TestQueryEndpointReturnsNormalizedInstantPlan(t *testing.T) {
	t.Parallel()

	mapping := validPrometheusMapping()
	mapping.Templates[0].AllowedParameters = []string{"end", "max_series", "service", "start"}
	repository := &fakeMappingStore{mapping: mapping}
	handler := newMappingTestHandler(t, repository, nil, "management-token-canary")
	request := authenticatedJSONRequest(
		http.MethodPost,
		"/api/v1/data-source-mappings/"+mappingID+"/queries",
		`{
			"template_id":"prometheus.service.latency.v1",
			"parameters":{
				"service":"checkout-api",
				"environment":"prod",
				"query_type":"instant",
				"at":"2026-11-04T05:30:00Z"
			}
		}`,
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", response.Code, response.Body.String())
	}
	for _, expected := range []string{
		`"query_type":"instant"`,
		`"start":"2026-11-04T05:30:00Z"`,
		`"end":"2026-11-04T05:30:00Z"`,
		`"max_points":1`,
	} {
		if !strings.Contains(response.Body.String(), expected) {
			t.Errorf("response missing %s: %s", expected, response.Body.String())
		}
	}
}

func TestValidateEndpointReturnsStructuredRuntimeLabelIssues(t *testing.T) {
	t.Parallel()

	mapping := validPrometheusMapping()
	mapping.SourceType = datasource.SourceRuntime
	mapping.RuntimeType = datasource.RuntimeDockerCompose
	mapping.PublicSettings = datasource.PublicSettings{
		Endpoint:      "https://runtime.example.test",
		RuntimeLabels: map[string]string{"project": "relythra"},
	}
	mapping.Templates = []datasource.Template{{
		ID: "runtime.containers.v1", Version: 1, Source: datasource.SourceRuntime,
		Purpose: "Read containers", ResultSchema: "runtime.container_list.v1",
		AllowedParameters: []string{"cursor", "end", "limit", "max_series", "service", "start"},
	}}
	handler := newMappingTestHandler(
		t,
		&fakeMappingStore{mapping: mapping},
		nil,
		"management-token-canary",
	)
	request := authenticatedJSONRequest(
		http.MethodPost,
		"/api/v1/data-source-mappings/"+mappingID+"/validate",
		``,
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"valid":false`) ||
		!strings.Contains(response.Body.String(), `"runtime_labels.container_id"`) {
		t.Fatalf("validation response = %s", response.Body.String())
	}
}

func TestConnectionTestRejectsKubernetesAndDoesNotCallTester(t *testing.T) {
	t.Parallel()

	mapping := validPrometheusMapping()
	mapping.SourceType = datasource.SourceRuntime
	mapping.RuntimeType = datasource.RuntimeKubernetes
	mapping.Templates = []datasource.Template{{
		ID: "runtime.containers.v1", Version: 1, Source: datasource.SourceRuntime,
	}}
	tester := &fakeConnectionTester{}
	handler := newMappingTestHandler(
		t,
		&fakeMappingStore{mapping: mapping},
		tester,
		"management-token-canary",
	)
	request := authenticatedJSONRequest(
		http.MethodPost,
		"/api/v1/data-source-mappings/"+mappingID+"/connection-test",
		``,
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	assertErrorResponse(t, response, http.StatusUnprocessableEntity, "unsupported_runtime")
	if tester.calls != 0 {
		t.Fatalf("tester calls = %d, want 0", tester.calls)
	}
}

func TestConnectionTestReturnsBoundedResultWithoutSecret(t *testing.T) {
	t.Parallel()

	const canary = "connection-secret-canary"
	mapping := validPrometheusMapping()
	mapping.SecretRef = "env:" + strings.ToUpper(strings.ReplaceAll(canary, "-", "_"))
	tester := &fakeConnectionTester{result: health.Result{
		Connectivity: health.ConnectivityConnected,
		Permission:   health.PermissionGranted,
		Version:      "3.5.0",
		Capabilities: []string{"prometheus.query.read"},
		CheckedAt:    time.Date(2026, 11, 4, 6, 0, 0, 0, time.UTC),
	}}
	handler := newMappingTestHandler(
		t,
		&fakeMappingStore{mapping: mapping},
		tester,
		"management-token-canary",
	)
	request := authenticatedJSONRequest(
		http.MethodPost,
		"/api/v1/data-source-mappings/"+mappingID+"/connection-test",
		``,
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"version":"3.5.0"`) {
		t.Fatalf("result = %s", response.Body.String())
	}
	if strings.Contains(response.Body.String(), canary) ||
		strings.Contains(response.Body.String(), mapping.SecretRef) {
		t.Fatal("connection test response exposed secret material")
	}
}

func TestMappingStoreConflictsReturnStableStaleVersion(t *testing.T) {
	t.Parallel()

	repository := &fakeMappingStore{
		mapping: validPrometheusMapping(),
		err:     store.ErrConflict,
	}
	handler := newMappingTestHandler(t, repository, nil, "management-token-canary")
	request := authenticatedJSONRequest(
		http.MethodPut,
		"/api/v1/data-source-mappings/"+mappingID,
		validUpdateBody(),
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	assertErrorResponse(t, response, http.StatusConflict, "stale_version")
}

func TestMappingListUsesBoundedCursorPagination(t *testing.T) {
	t.Parallel()

	repository := &fakeMappingStore{
		list: []store.DataSourceMapping{validPrometheusMapping()},
	}
	handler := newMappingTestHandler(t, repository, nil, "management-token-canary")
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/data-source-mappings?limit=1&cursor=Mg",
		nil,
	)
	request.Header.Set("Authorization", "Bearer management-token-canary")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", response.Code, response.Body.String())
	}
	if repository.listLimit != 1 || repository.listOffset != 2 {
		t.Fatalf("limit/offset = %d/%d, want 1/2", repository.listLimit, repository.listOffset)
	}
	if !strings.Contains(response.Body.String(), `"next_cursor":"Mw"`) {
		t.Fatalf("response missing next cursor: %s", response.Body.String())
	}
}

func TestMappingDependencyErrorsAreRedacted(t *testing.T) {
	t.Parallel()

	const canary = "mapping-dependency-canary"
	repository := &fakeMappingStore{
		err: errors.New("postgres://admin:" + canary + "@database/relythra"),
	}
	handler := newMappingTestHandler(t, repository, nil, "management-token-canary")
	request := httptest.NewRequest(http.MethodGet, "/api/v1/data-source-mappings/"+mappingID, nil)
	request.Header.Set("Authorization", "Bearer management-token-canary")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	assertErrorResponse(t, response, http.StatusInternalServerError, "internal_error")
	if strings.Contains(response.Body.String(), canary) {
		t.Fatal("response exposed dependency error")
	}
}

func newMappingTestHandler(
	t *testing.T,
	repository MappingRepository,
	tester ConnectionTester,
	token string,
) http.Handler {
	t.Helper()
	secret, err := secrets.New(token)
	if err != nil {
		t.Fatalf("new management token: %v", err)
	}
	authenticator, err := NewBearerTokenAuthenticator(secret)
	if err != nil {
		t.Fatalf("new authenticator: %v", err)
	}
	return newMappingTestHandlerWithDependencies(
		t,
		repository,
		tester,
		authenticator,
		&capturingAuditRecorder{},
	)
}

func newMappingTestHandlerWithDependencies(
	t *testing.T,
	repository MappingRepository,
	tester ConnectionTester,
	authenticator AdminAuthenticator,
	recorder audit.Recorder,
) http.Handler {
	t.Helper()
	return NewHandlerWithDependencies(
		Dependencies{
			Reader:             &fakeReader{},
			Mappings:           repository,
			AdminAuthenticator: authenticator,
			ConnectionTester:   tester,
			AuditRecorder:      recorder,
		},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
}

func authenticatedJSONRequest(method, path, body string) *http.Request {
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	request.Header.Set("Authorization", "Bearer management-token-canary")
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	return request
}

func validPrometheusMapping() store.DataSourceMapping {
	now := time.Date(2026, 11, 4, 6, 0, 0, 0, time.UTC)
	return store.DataSourceMapping{
		ID:          mappingID,
		ProjectID:   "66666666-6666-4666-8666-666666666666",
		ServiceID:   "77777777-7777-4777-8777-777777777777",
		Environment: "prod",
		SourceType:  datasource.SourcePrometheus,
		PublicSettings: datasource.PublicSettings{
			Endpoint: "https://metrics.example.test",
		},
		Templates: []datasource.Template{{
			ID:                "prometheus.service.latency.v1",
			Version:           1,
			Source:            datasource.SourcePrometheus,
			Purpose:           "Read latency",
			ResultSchema:      "metric.series.v1",
			AllowedParameters: []string{"at", "end", "environment", "max_points", "max_series", "query_type", "service", "start", "step"},
		}},
		SecretRef: "env:MAPPING_SECRET_CANARY",
		Version:   3,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func validUpdateBody() string {
	return `{
		"expected_version":3,
		"project_id":"66666666-6666-4666-8666-666666666666",
		"service_id":"77777777-7777-4777-8777-777777777777",
		"environment":"prod",
		"source_type":"PROMETHEUS",
		"public_settings":{"endpoint":"https://metrics.example.test"},
		"template_ids":["prometheus.service.latency.v1"],
		"secret_ref":"env:MAPPING_SECRET_CANARY"
	}`
}

func hasValidationIssue(issues []datasource.ValidationIssue, field, code string) bool {
	for _, issue := range issues {
		if issue.Field == field && issue.Code == code {
			return true
		}
	}
	return false
}

var _ = domain.IsID
