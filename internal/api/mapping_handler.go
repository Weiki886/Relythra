package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Weiki886/relythra/internal/connector/health"
	"github.com/Weiki886/relythra/internal/datasource"
	"github.com/Weiki886/relythra/internal/domain"
	"github.com/Weiki886/relythra/internal/security/audit"
	"github.com/Weiki886/relythra/internal/security/secrets"
	"github.com/Weiki886/relythra/internal/store"
)

const (
	maxManagementBodyBytes = 1 << 20
	defaultMappingPageSize = 20
	maxMappingPageSize     = 100
)

type MappingRepository interface {
	CreateDataSourceMapping(
		context.Context,
		store.CreateDataSourceMappingInput,
	) (store.DataSourceMapping, []datasource.ValidationIssue, error)
	GetDataSourceMapping(context.Context, string) (store.DataSourceMapping, error)
	ListDataSourceMappingsPage(
		context.Context,
		string,
		string,
		string,
		int,
		int,
	) ([]store.DataSourceMapping, error)
	UpdateDataSourceMapping(
		context.Context,
		store.UpdateDataSourceMappingInput,
	) (store.DataSourceMapping, []datasource.ValidationIssue, error)
	DeleteDataSourceMapping(context.Context, string, int) error
}

type ConnectionTester interface {
	Test(context.Context, health.Target) (health.Result, error)
}

type DeploymentRepository interface {
	ClaimDeploymentWebhookSignature(context.Context, []byte, time.Time) (bool, error)
	CreateDeploymentEvent(context.Context, store.CreateDeploymentEventInput) (store.DeploymentEvent, bool, error)
	UpdateDeploymentEvent(context.Context, string, store.UpdateDeploymentEventInput) (store.DeploymentEvent, error)
	FindDeploymentCandidates(context.Context, store.DeploymentCandidateQuery) ([]store.DeploymentEvent, error)
}

type Dependencies struct {
	Reader                  Reader
	Mappings                MappingRepository
	Deployments             DeploymentRepository
	AdminAuthenticator      AdminAuthenticator
	ConnectionTester        ConnectionTester
	AuditRecorder           audit.Recorder
	DeploymentWebhookSecret secrets.Secret
	DeploymentWebhookSource string
	GrafanaWebhookSecret    secrets.Secret
	GrafanaAlerts           store.GrafanaAlertRepository
	ManagementActor         string
}

type mappingWriteRequest struct {
	ProjectID      string                    `json:"project_id"`
	ServiceID      string                    `json:"service_id"`
	Environment    string                    `json:"environment"`
	SourceType     datasource.SourceType     `json:"source_type"`
	RuntimeType    datasource.RuntimeType    `json:"runtime_type,omitempty"`
	PublicSettings datasource.PublicSettings `json:"public_settings"`
	TemplateIDs    []string                  `json:"template_ids"`
	SecretRef      string                    `json:"secret_ref,omitempty"`
}

type mappingUpdateRequest struct {
	ExpectedVersion int `json:"expected_version"`
	mappingWriteRequest
}

type mappingQueryRequest struct {
	TemplateID string         `json:"template_id"`
	Parameters map[string]any `json:"parameters"`
}

type mappingView struct {
	ID             string                    `json:"id"`
	ProjectID      string                    `json:"project_id"`
	ServiceID      string                    `json:"service_id"`
	Environment    string                    `json:"environment"`
	SourceType     datasource.SourceType     `json:"source_type"`
	RuntimeType    datasource.RuntimeType    `json:"runtime_type,omitempty"`
	PublicSettings datasource.PublicSettings `json:"public_settings"`
	Templates      []templateView            `json:"templates"`
	SecretStatus   string                    `json:"secret_status"`
	Version        int                       `json:"version"`
	CreatedAt      time.Time                 `json:"created_at"`
	UpdatedAt      time.Time                 `json:"updated_at"`
}

type templateView struct {
	ID                string                `json:"id"`
	Version           int                   `json:"version"`
	Source            datasource.SourceType `json:"source"`
	Purpose           string                `json:"purpose"`
	ResultSchema      string                `json:"result_schema"`
	AllowedParameters []string              `json:"allowed_parameters"`
}

type mappingListView struct {
	Items      []mappingView `json:"items"`
	NextCursor string        `json:"next_cursor"`
}

type queryPlanView struct {
	TemplateID  string        `json:"template_id"`
	Version     int           `json:"version"`
	Service     string        `json:"service"`
	Environment string        `json:"environment,omitempty"`
	QueryType   string        `json:"query_type,omitempty"`
	Start       time.Time     `json:"start"`
	End         time.Time     `json:"end"`
	Step        time.Duration `json:"step,omitempty"`
	Limit       int           `json:"limit"`
	Cursor      string        `json:"cursor"`
	MaxSeries   int           `json:"max_series"`
	MaxPoints   int           `json:"max_points,omitempty"`
}

type validationView struct {
	Valid  bool                         `json:"valid"`
	Issues []datasource.ValidationIssue `json:"issues"`
}

func (handler *handler) manageMappingCollection(w http.ResponseWriter, r *http.Request) {
	if !handler.authorizeManagement(w, r) {
		return
	}
	handler.withManagementAudit(w, r, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			handler.listMappings(w, r)
		case http.MethodPost:
			handler.createMapping(w, r)
		default:
			writeMethodNotAllowed(w, r, http.MethodGet, http.MethodPost)
		}
	})
}

func (handler *handler) manageMappingItem(w http.ResponseWriter, r *http.Request) {
	if !handler.authorizeManagement(w, r) {
		return
	}
	handler.withManagementAudit(w, r, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			handler.getMapping(w, r)
		case http.MethodPut:
			handler.updateMapping(w, r)
		case http.MethodDelete:
			handler.deleteMapping(w, r)
		default:
			writeMethodNotAllowed(w, r, http.MethodGet, http.MethodPut, http.MethodDelete)
		}
	})
}

func (handler *handler) manageMappingAction(w http.ResponseWriter, r *http.Request) {
	if !handler.authorizeManagement(w, r) {
		return
	}
	handler.withManagementAudit(w, r, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, r, http.MethodPost)
			return
		}
		switch r.PathValue("action") {
		case "validate":
			handler.validateMapping(w, r)
		case "queries":
			handler.planMappingQuery(w, r)
		case "connection-test":
			handler.testMappingConnection(w, r)
		default:
			handler.notFound(w, r)
		}
	})
}

func (handler *handler) authorizeManagement(w http.ResponseWriter, r *http.Request) bool {
	if handler.auditRecorder == nil {
		writeError(w, r, http.StatusServiceUnavailable, "management_unavailable", "management API is unavailable")
		return false
	}
	if handler.adminAuthenticator == nil {
		if !handler.recordDeniedManagementAttempt(w, r, "management_unavailable") {
			return false
		}
		writeError(w, r, http.StatusServiceUnavailable, "management_unavailable", "management API is unavailable")
		return false
	}
	err := handler.adminAuthenticator.Authenticate(r)
	switch {
	case err == nil:
		return true
	case errors.Is(err, ErrUnauthorized):
		if !handler.recordDeniedManagementAttempt(w, r, "unauthorized") {
			return false
		}
		w.Header().Set("WWW-Authenticate", "Bearer")
		writeError(w, r, http.StatusUnauthorized, "unauthorized", "management authorization is required")
	case errors.Is(err, ErrManagementUnavailable):
		if !handler.recordDeniedManagementAttempt(w, r, "management_unavailable") {
			return false
		}
		writeError(w, r, http.StatusServiceUnavailable, "management_unavailable", "management API is unavailable")
	default:
		if !handler.recordDeniedManagementAttempt(w, r, "management_unavailable") {
			return false
		}
		writeError(w, r, http.StatusServiceUnavailable, "management_unavailable", "management API is unavailable")
	}
	return false
}

func (handler *handler) listMappings(w http.ResponseWriter, r *http.Request) {
	limit, err := boundedQueryInt(r, "limit", defaultMappingPageSize, maxMappingPageSize)
	if err != nil {
		handler.writeBodyValidationError(w, r, "limit", err.Error())
		return
	}
	offset, err := decodeMappingCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		handler.writeBodyValidationError(w, r, "cursor", "cursor is invalid")
		return
	}
	mappings, err := handler.mappings.ListDataSourceMappingsPage(
		r.Context(),
		strings.TrimSpace(r.URL.Query().Get("project_id")),
		strings.TrimSpace(r.URL.Query().Get("service_id")),
		strings.TrimSpace(r.URL.Query().Get("environment")),
		limit,
		offset,
	)
	if err != nil {
		handler.writeMappingStoreError(w, r, err, nil)
		return
	}
	items := make([]mappingView, 0, len(mappings))
	for _, mapping := range mappings {
		items = append(items, newMappingView(mapping))
	}
	nextCursor := ""
	if len(mappings) == limit {
		nextCursor = encodeMappingCursor(offset + limit)
	}
	handler.writeData(w, r, http.StatusOK, mappingListView{
		Items:      items,
		NextCursor: nextCursor,
	})
}

func (handler *handler) createMapping(w http.ResponseWriter, r *http.Request) {
	var body mappingWriteRequest
	if !decodeStrictJSON(w, r, &body, false) {
		return
	}
	auditStateFromRequest(r).SourceType = string(body.SourceType)
	mapping, issues, err := handler.mappings.CreateDataSourceMapping(
		r.Context(),
		store.CreateDataSourceMappingInput{
			ProjectID:      body.ProjectID,
			ServiceID:      body.ServiceID,
			Environment:    body.Environment,
			SourceType:     body.SourceType,
			RuntimeType:    body.RuntimeType,
			PublicSettings: body.PublicSettings,
			TemplateIDs:    body.TemplateIDs,
			SecretRef:      body.SecretRef,
		},
	)
	if err != nil {
		handler.writeMappingStoreError(w, r, err, issues)
		return
	}
	auditStateFromRequest(r).ObjectID = mapping.ID
	handler.writeData(w, r, http.StatusCreated, newMappingView(mapping))
}

func (handler *handler) getMapping(w http.ResponseWriter, r *http.Request) {
	id, ok := requireID(w, r)
	if !ok {
		return
	}
	mapping, err := handler.mappings.GetDataSourceMapping(r.Context(), id)
	if err != nil {
		handler.writeMappingStoreError(w, r, err, nil)
		return
	}
	handler.writeData(w, r, http.StatusOK, newMappingView(mapping))
}

func (handler *handler) updateMapping(w http.ResponseWriter, r *http.Request) {
	id, ok := requireID(w, r)
	if !ok {
		return
	}
	var body mappingUpdateRequest
	if !decodeStrictJSON(w, r, &body, false) {
		return
	}
	if body.ExpectedVersion < 1 {
		handler.writeBodyValidationError(w, r, "expected_version", "expected_version must be positive")
		return
	}
	auditStateFromRequest(r).SourceType = string(body.SourceType)
	mapping, issues, err := handler.mappings.UpdateDataSourceMapping(
		r.Context(),
		store.UpdateDataSourceMappingInput{
			ID:              id,
			ExpectedVersion: body.ExpectedVersion,
			ProjectID:       body.ProjectID,
			ServiceID:       body.ServiceID,
			Environment:     body.Environment,
			SourceType:      body.SourceType,
			RuntimeType:     body.RuntimeType,
			PublicSettings:  body.PublicSettings,
			TemplateIDs:     body.TemplateIDs,
			SecretRef:       body.SecretRef,
		},
	)
	if err != nil {
		handler.writeMappingStoreError(w, r, err, issues)
		return
	}
	handler.writeData(w, r, http.StatusOK, newMappingView(mapping))
}

func (handler *handler) deleteMapping(w http.ResponseWriter, r *http.Request) {
	id, ok := requireID(w, r)
	if !ok {
		return
	}
	version, err := boundedQueryInt(r, "expected_version", 0, int(^uint(0)>>1))
	if err != nil || version < 1 {
		handler.writeBodyValidationError(
			w,
			r,
			"expected_version",
			"expected_version must be a positive integer",
		)
		return
	}
	if err := handler.mappings.DeleteDataSourceMapping(r.Context(), id, version); err != nil {
		handler.writeMappingStoreError(w, r, err, nil)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

func (handler *handler) validateMapping(w http.ResponseWriter, r *http.Request) {
	id, ok := requireID(w, r)
	if !ok {
		return
	}
	if !requireEmptyBody(w, r) {
		return
	}
	mapping, err := handler.mappings.GetDataSourceMapping(r.Context(), id)
	if err != nil {
		handler.writeMappingStoreError(w, r, err, nil)
		return
	}
	issues := validateStoredMapping(mapping)
	handler.writeData(w, r, http.StatusOK, validationView{
		Valid:  len(issues) == 0,
		Issues: issues,
	})
}

func (handler *handler) planMappingQuery(w http.ResponseWriter, r *http.Request) {
	id, ok := requireID(w, r)
	if !ok {
		return
	}
	var body mappingQueryRequest
	if !decodeStrictJSON(w, r, &body, false) {
		return
	}
	auditStateFromRequest(r).TemplateID = strings.TrimSpace(body.TemplateID)
	mapping, err := handler.mappings.GetDataSourceMapping(r.Context(), id)
	if err != nil {
		handler.writeMappingStoreError(w, r, err, nil)
		return
	}
	template, found := findTemplate(mapping.Templates, strings.TrimSpace(body.TemplateID))
	if !found {
		handler.writeValidationIssues(w, r, []datasource.ValidationIssue{{
			Field:   "template_id",
			Code:    "unsupported_template",
			Message: "query template is not configured for this mapping",
		}})
		return
	}
	parameters, issues := parseQueryParameters(body.Parameters)
	normalized, queryIssues := datasource.ValidateQuery(template, parameters)
	issues = append(issues, queryIssues...)
	if len(issues) > 0 {
		handler.writeValidationIssues(w, r, issues)
		return
	}
	handler.writeData(w, r, http.StatusOK, queryPlanView{
		TemplateID:  normalized.TemplateID,
		Version:     normalized.Version,
		Service:     normalized.Service,
		Environment: normalized.Environment,
		QueryType:   normalized.QueryType,
		Start:       normalized.Start,
		End:         normalized.End,
		Step:        normalized.Step,
		Limit:       normalized.Limit,
		Cursor:      normalized.Cursor,
		MaxSeries:   normalized.MaxSeries,
		MaxPoints:   normalized.MaxPoints,
	})
}

func (handler *handler) testMappingConnection(w http.ResponseWriter, r *http.Request) {
	id, ok := requireID(w, r)
	if !ok {
		return
	}
	if !requireEmptyBody(w, r) {
		return
	}
	mapping, err := handler.mappings.GetDataSourceMapping(r.Context(), id)
	if err != nil {
		handler.writeMappingStoreError(w, r, err, nil)
		return
	}
	auditStateFromRequest(r).SourceType = string(mapping.SourceType)
	if mapping.SourceType == datasource.SourceRuntime &&
		mapping.RuntimeType == datasource.RuntimeKubernetes {
		handler.setAuditError(r, "unsupported_runtime", audit.ResultDenied)
		writeError(w, r, http.StatusUnprocessableEntity, "unsupported_runtime", "Kubernetes runtime is not supported in v1")
		return
	}
	if issues := validateStoredMapping(mapping); len(issues) > 0 {
		handler.writeValidationIssues(w, r, issues)
		return
	}
	if handler.connectionTester == nil {
		handler.setAuditError(r, "connection_test_unavailable", audit.ResultFailed)
		writeError(w, r, http.StatusServiceUnavailable, "connection_test_unavailable", "connection testing is unavailable")
		return
	}
	if !handler.preflightConnectionTestAudit(w, r) {
		return
	}
	var reference *secrets.Reference
	if mapping.SecretRef != "" {
		parsed, parseErr := secrets.ParseReference(mapping.SecretRef)
		if parseErr != nil {
			handler.writeBodyValidationError(w, r, "secret_ref", "secret reference is invalid")
			return
		}
		reference = &parsed
	}
	result, err := handler.connectionTester.Test(r.Context(), health.Target{
		SourceType:      mapping.SourceType,
		RuntimeType:     mapping.RuntimeType,
		PublicSettings:  mapping.PublicSettings,
		SecretReference: reference,
	})
	if err != nil {
		handler.writeConnectionTestError(w, r, err)
		return
	}
	if result.Permission == health.PermissionDenied {
		state := auditStateFromRequest(r)
		state.Result = audit.ResultDenied
		state.ErrorCode = "permission_denied"
	}
	handler.writeData(w, r, http.StatusOK, result)
}

func validateStoredMapping(mapping store.DataSourceMapping) []datasource.ValidationIssue {
	var reference *secrets.Reference
	if mapping.SecretRef != "" {
		parsed, err := secrets.ParseReference(mapping.SecretRef)
		if err != nil {
			return []datasource.ValidationIssue{{
				Field:   "secret_ref",
				Code:    "invalid_reference",
				Message: "secret reference is invalid",
			}}
		}
		reference = &parsed
	}
	templateIDs := make([]string, 0, len(mapping.Templates))
	for _, template := range mapping.Templates {
		templateIDs = append(templateIDs, template.ID)
	}
	return datasource.ValidateMapping(
		mapping.SourceType,
		mapping.RuntimeType,
		mapping.PublicSettings,
		templateIDs,
		reference,
	)
}

func newMappingView(mapping store.DataSourceMapping) mappingView {
	templates := make([]templateView, 0, len(mapping.Templates))
	for _, template := range mapping.Templates {
		templates = append(templates, templateView{
			ID:                template.ID,
			Version:           template.Version,
			Source:            template.Source,
			Purpose:           template.Purpose,
			ResultSchema:      template.ResultSchema,
			AllowedParameters: append([]string(nil), template.AllowedParameters...),
		})
	}
	secretStatus := "not_configured"
	if mapping.SecretRef != "" {
		secretStatus = "configured"
	}
	return mappingView{
		ID:             mapping.ID,
		ProjectID:      mapping.ProjectID,
		ServiceID:      mapping.ServiceID,
		Environment:    mapping.Environment,
		SourceType:     mapping.SourceType,
		RuntimeType:    mapping.RuntimeType,
		PublicSettings: mapping.PublicSettings,
		Templates:      templates,
		SecretStatus:   secretStatus,
		Version:        mapping.Version,
		CreatedAt:      mapping.CreatedAt,
		UpdatedAt:      mapping.UpdatedAt,
	}
}

func findTemplate(templates []datasource.Template, id string) (datasource.Template, bool) {
	for _, template := range templates {
		if template.ID == id {
			registered, issues := datasource.ResolveTemplates(template.Source, []string{id})
			if len(issues) != 0 || len(registered) != 1 {
				return datasource.Template{}, false
			}
			return registered[0], true
		}
	}
	return datasource.Template{}, false
}

func parseQueryParameters(values map[string]any) (datasource.QueryParameters, []datasource.ValidationIssue) {
	parameters := datasource.QueryParameters{Extra: make(map[string]any, len(values))}
	for key, value := range values {
		parameters.Extra[key] = value
	}
	issues := make([]datasource.ValidationIssue, 0)
	parameters.Service, _ = queryString(values, "service", &issues)
	parameters.Environment, _ = queryString(values, "environment", &issues)
	parameters.QueryType, _ = queryString(values, "query_type", &issues)
	parameters.At, _ = queryString(values, "at", &issues)
	parameters.Start, _ = queryString(values, "start", &issues)
	parameters.End, _ = queryString(values, "end", &issues)
	parameters.Step, _ = queryString(values, "step", &issues)
	parameters.Cursor, _ = queryString(values, "cursor", &issues)
	parameters.Limit, _ = queryInteger(values, "limit", &issues)
	parameters.MaxSeries, _ = queryInteger(values, "max_series", &issues)
	parameters.MaxPoints, _ = queryInteger(values, "max_points", &issues)
	return parameters, issues
}

func queryString(
	values map[string]any,
	key string,
	issues *[]datasource.ValidationIssue,
) (string, bool) {
	raw, exists := values[key]
	if !exists {
		return "", false
	}
	value, ok := raw.(string)
	if !ok {
		*issues = append(*issues, datasource.ValidationIssue{
			Field:   "parameters." + key,
			Code:    "invalid_type",
			Message: "query parameter must be a string",
		})
		return "", false
	}
	return value, true
}

func queryInteger(
	values map[string]any,
	key string,
	issues *[]datasource.ValidationIssue,
) (int, bool) {
	raw, exists := values[key]
	if !exists {
		return 0, false
	}
	value, ok := integerValue(raw)
	if !ok {
		*issues = append(*issues, datasource.ValidationIssue{
			Field:   "parameters." + key,
			Code:    "invalid_type",
			Message: "query parameter must be an integer",
		})
		return 0, false
	}
	return value, true
}

func integerValue(value any) (int, bool) {
	switch typed := value.(type) {
	case json.Number:
		parsed, err := strconv.ParseInt(string(typed), 10, 32)
		return int(parsed), err == nil
	case float64:
		if typed != float64(int(typed)) {
			return 0, false
		}
		return int(typed), true
	case int:
		return typed, true
	default:
		return 0, false
	}
}

func decodeStrictJSON(w http.ResponseWriter, r *http.Request, destination any, allowEmpty bool) bool {
	if r.ContentLength == 0 && allowEmpty {
		return true
	}
	contentType := strings.TrimSpace(r.Header.Get("Content-Type"))
	if !strings.HasPrefix(strings.ToLower(contentType), "application/json") {
		writeError(w, r, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/json")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxManagementBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			writeError(w, r, http.StatusRequestEntityTooLarge, "payload_too_large", "request body is too large")
		} else {
			writeError(w, r, http.StatusUnprocessableEntity, "validation_failed", "request body is invalid")
		}
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		writeError(w, r, http.StatusUnprocessableEntity, "validation_failed", "request must contain one JSON document")
		return false
	}
	return true
}

func requireEmptyBody(w http.ResponseWriter, r *http.Request) bool {
	if r.ContentLength == 0 {
		return true
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 2))
	if err != nil || len(strings.TrimSpace(string(body))) != 0 {
		writeError(w, r, http.StatusUnprocessableEntity, "validation_failed", "request body must be empty")
		return false
	}
	return true
}

func boundedQueryInt(r *http.Request, key string, fallback int, maximum int) (int, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		if fallback < 1 {
			return 0, fmt.Errorf("%s is required", key)
		}
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 || value > maximum {
		return 0, fmt.Errorf("%s must be between 1 and %d", key, maximum)
	}
	return value, nil
}

func encodeMappingCursor(offset int) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(offset)))
}

func decodeMappingCursor(raw string) (int, error) {
	if raw == "" {
		return 0, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return 0, err
	}
	offset, err := strconv.Atoi(string(decoded))
	if err != nil || offset < 0 {
		return 0, errors.New("invalid cursor")
	}
	return offset, nil
}

func (handler *handler) writeBodyValidationError(
	w http.ResponseWriter,
	r *http.Request,
	field string,
	message string,
) {
	handler.setAuditError(r, "validation_failed", audit.ResultFailed)
	handler.writeValidationIssues(w, r, []datasource.ValidationIssue{{
		Field:   field,
		Code:    "invalid_format",
		Message: message,
	}})
}

func (handler *handler) writeValidationIssues(
	w http.ResponseWriter,
	r *http.Request,
	issues []datasource.ValidationIssue,
) {
	handler.setAuditError(r, "validation_failed", audit.ResultFailed)
	writeErrorDetails(
		w,
		r,
		http.StatusUnprocessableEntity,
		"validation_failed",
		"mapping configuration is invalid",
		issues,
	)
}

func (handler *handler) writeMappingStoreError(
	w http.ResponseWriter,
	r *http.Request,
	err error,
	issues []datasource.ValidationIssue,
) {
	switch {
	case len(issues) > 0 && errors.Is(err, store.ErrInvalidInput):
		handler.writeValidationIssues(w, r, issues)
	case errors.Is(err, store.ErrConflict):
		handler.setAuditError(r, "stale_version", audit.ResultFailed)
		writeError(w, r, http.StatusConflict, "stale_version", "mapping version is stale")
	case errors.Is(err, store.ErrInvalidInput):
		handler.setAuditError(r, "invalid_request", audit.ResultFailed)
		writeError(w, r, http.StatusUnprocessableEntity, "invalid_request", "mapping request is invalid")
	default:
		handler.setAuditError(r, "internal_error", audit.ResultFailed)
		handler.writeStoreError(w, r, err)
	}
}

func (handler *handler) writeConnectionTestError(
	w http.ResponseWriter,
	r *http.Request,
	err error,
) {
	var testError *health.TestError
	if !errors.As(err, &testError) {
		handler.setAuditError(r, "connection_test_failed", audit.ResultFailed)
		writeError(w, r, http.StatusBadGateway, "connection_test_failed", "connection test failed")
		return
	}
	switch testError.Category {
	case health.ErrorInvalidMapping:
		handler.setAuditError(r, "invalid_mapping", audit.ResultFailed)
		writeError(w, r, http.StatusUnprocessableEntity, "invalid_mapping", "mapping cannot be tested")
	case health.ErrorSecretUnavailable:
		handler.setAuditError(r, "secret_unavailable", audit.ResultFailed)
		writeError(w, r, http.StatusUnprocessableEntity, "secret_unavailable", "mapping secret is unavailable")
	case health.ErrorPolicyRejected:
		handler.setAuditError(r, "policy_rejected", audit.ResultDenied)
		writeError(w, r, http.StatusUnprocessableEntity, "policy_rejected", "outbound target is not allowed")
	case health.ErrorTimeout:
		handler.setAuditError(r, "connection_timeout", audit.ResultFailed)
		writeError(w, r, http.StatusGatewayTimeout, "connection_timeout", "data source connection timed out")
	case health.ErrorTLS:
		handler.setAuditError(r, "tls_error", audit.ResultFailed)
		writeError(w, r, http.StatusBadGateway, "tls_error", "data source TLS verification failed")
	case health.ErrorNetwork:
		handler.setAuditError(r, "network_error", audit.ResultFailed)
		writeError(w, r, http.StatusBadGateway, "network_error", "data source network connection failed")
	case health.ErrorRemoteUnavailable:
		handler.setAuditError(r, "remote_unavailable", audit.ResultFailed)
		writeError(w, r, http.StatusBadGateway, "remote_unavailable", "data source is unavailable")
	case health.ErrorInvalidResponse:
		handler.setAuditError(r, "invalid_response", audit.ResultFailed)
		writeError(w, r, http.StatusBadGateway, "invalid_response", "data source returned an invalid response")
	case health.ErrorResponseTooLarge:
		handler.setAuditError(r, "response_too_large", audit.ResultFailed)
		writeError(w, r, http.StatusBadGateway, "response_too_large", "data source response exceeded the limit")
	default:
		handler.setAuditError(r, "connection_test_failed", audit.ResultFailed)
		writeError(w, r, http.StatusBadGateway, "connection_test_failed", "connection test failed")
	}
}

type managementAuditState struct {
	SourceType string
	TemplateID string
	ObjectID   string
	ObjectType string
	Action     audit.Action
	Result     audit.Result
	ErrorCode  string
	SkipFinal  bool
}

type managementAuditContextKey struct{}

type bufferedResponseWriter struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func newBufferedResponseWriter() *bufferedResponseWriter {
	return &bufferedResponseWriter{header: make(http.Header)}
}

func (writer *bufferedResponseWriter) Header() http.Header {
	return writer.header
}

func (writer *bufferedResponseWriter) WriteHeader(status int) {
	if writer.status != 0 {
		return
	}
	writer.status = status
}

func (writer *bufferedResponseWriter) Write(body []byte) (int, error) {
	if writer.status == 0 {
		writer.status = http.StatusOK
	}
	return writer.body.Write(body)
}

func (handler *handler) withManagementAudit(
	w http.ResponseWriter,
	r *http.Request,
	next http.HandlerFunc,
) {
	state := &managementAuditState{}
	r = r.WithContext(context.WithValue(r.Context(), managementAuditContextKey{}, state))
	peekManagementAuditMetadata(r, state)

	startedAt := time.Now()
	buffered := newBufferedResponseWriter()
	next(buffered, r)
	if buffered.status == 0 {
		buffered.status = http.StatusOK
	}
	if state.SkipFinal {
		copyHTTPHeader(w.Header(), buffered.header)
		w.WriteHeader(buffered.status)
		_, _ = w.Write(buffered.body.Bytes())
		return
	}

	errorCode := state.ErrorCode
	if errorCode == "" {
		errorCode = managementErrorCode(buffered.body.Bytes())
	}
	result := state.Result
	if result == "" {
		result = managementAuditResult(buffered.status, errorCode)
	}
	event := handler.newManagementAuditEvent(
		r,
		*state,
		managementAuditAction(r),
		result,
		errorCode,
		startedAt,
	)
	if _, err := handler.auditRecorder.Record(r.Context(), event); err != nil {
		handler.writeManagementAuditUnavailable(w, r)
		return
	}
	copyHTTPHeader(w.Header(), buffered.header)
	w.WriteHeader(buffered.status)
	_, _ = w.Write(buffered.body.Bytes())
}

func peekManagementAuditMetadata(r *http.Request, state *managementAuditState) {
	if r.Body == nil ||
		!strings.HasPrefix(strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type"))), "application/json") {
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxManagementBodyBytes+1))
	if err != nil {
		return
	}
	originalBody := r.Body
	r.Body = &multiReadCloser{
		Reader: io.MultiReader(bytes.NewReader(body), originalBody),
		Closer: originalBody,
	}
	var metadata struct {
		SourceType datasource.SourceType `json:"source_type"`
		TemplateID string                `json:"template_id"`
	}
	if json.Unmarshal(body, &metadata) != nil {
		return
	}
	if metadata.SourceType != "" {
		state.SourceType = string(metadata.SourceType)
	}
	state.TemplateID = strings.TrimSpace(metadata.TemplateID)
}

type multiReadCloser struct {
	io.Reader
	io.Closer
}

func managementAuditAction(r *http.Request) audit.Action {
	if state := managementAuditStateFromContext(r); state != nil && state.Action != "" {
		return state.Action
	}
	switch r.PathValue("action") {
	case "queries":
		return audit.ActionToolCalled
	case "connection-test":
		return audit.ActionConnectionTested
	default:
		return audit.ActionConfigChanged
	}
}

func managementObjectID(r *http.Request) string {
	id := r.PathValue("id")
	if id == "" {
		return "collection"
	}
	if !domain.IsID(id) {
		return "invalid_id"
	}
	return id
}

func managementAuditResult(status int, errorCode string) audit.Result {
	if status >= http.StatusOK && status < http.StatusMultipleChoices {
		return audit.ResultSucceeded
	}
	switch errorCode {
	case "unauthorized", "management_unavailable", "policy_rejected", "unsupported_runtime":
		return audit.ResultDenied
	default:
		return audit.ResultFailed
	}
}

func managementErrorCode(body []byte) string {
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return ""
	}
	return envelope.Error.Code
}

func (handler *handler) newManagementAuditEvent(
	r *http.Request,
	state managementAuditState,
	action audit.Action,
	result audit.Result,
	errorCode string,
	startedAt time.Time,
) audit.Event {
	parameters := map[string]any{
		"elapsed_ms": max(time.Since(startedAt).Milliseconds(), 0),
	}
	if state.SourceType != "" {
		parameters["source_type"] = state.SourceType
	}
	if state.TemplateID != "" {
		parameters["template_id"] = state.TemplateID
	}
	if errorCode != "" {
		parameters["error_code"] = errorCode
	}
	objectID := state.ObjectID
	if objectID == "" {
		objectID = managementObjectID(r)
	}
	objectType := state.ObjectType
	if objectType == "" {
		objectType = "data_source_mapping"
	}
	return audit.Event{
		Actor:      "management-token",
		Action:     action,
		ObjectType: objectType,
		ObjectID:   objectID,
		Result:     result,
		RequestID:  RequestIDFromContext(r.Context()),
		Parameters: parameters,
		OccurredAt: time.Now().UTC(),
	}
}

func managementAuditStateFromContext(r *http.Request) *managementAuditState {
	state, _ := r.Context().Value(managementAuditContextKey{}).(*managementAuditState)
	return state
}

func (handler *handler) recordDeniedManagementAttempt(
	w http.ResponseWriter,
	r *http.Request,
	errorCode string,
) bool {
	startedAt := time.Now()
	event := handler.newManagementAuditEvent(
		r,
		managementAuditState{},
		managementAuditAction(r),
		audit.ResultDenied,
		errorCode,
		startedAt,
	)
	if _, err := handler.auditRecorder.Record(r.Context(), event); err != nil {
		handler.writeManagementAuditUnavailable(w, r)
		return false
	}
	return true
}

func (handler *handler) preflightConnectionTestAudit(w http.ResponseWriter, r *http.Request) bool {
	startedAt := time.Now()
	state := auditStateFromRequest(r)
	event := handler.newManagementAuditEvent(
		r,
		*state,
		audit.ActionConnectionTested,
		audit.ResultFailed,
		"connection_test_started",
		startedAt,
	)
	if _, err := handler.auditRecorder.Record(r.Context(), event); err != nil {
		state.SkipFinal = true
		state.ErrorCode = "internal_error"
		handler.writeManagementAuditUnavailable(w, r)
		return false
	}
	return true
}

func (handler *handler) setAuditError(r *http.Request, errorCode string, result audit.Result) {
	state := auditStateFromRequest(r)
	state.ErrorCode = errorCode
	state.Result = result
}

func auditStateFromRequest(r *http.Request) *managementAuditState {
	state, ok := r.Context().Value(managementAuditContextKey{}).(*managementAuditState)
	if ok && state != nil {
		return state
	}
	return &managementAuditState{}
}

func (handler *handler) writeManagementAuditUnavailable(w http.ResponseWriter, r *http.Request) {
	handler.logger.Error(
		"management audit unavailable",
		"request_id", RequestIDFromContext(r.Context()),
	)
	writeError(w, r, http.StatusInternalServerError, "internal_error", "internal server error")
}

func copyHTTPHeader(destination http.Header, source http.Header) {
	for key, values := range source {
		for _, value := range values {
			destination.Add(key, value)
		}
	}
}

func writeMethodNotAllowed(w http.ResponseWriter, r *http.Request, methods ...string) {
	w.Header().Set("Allow", strings.Join(methods, ", "))
	writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method is not allowed")
}
