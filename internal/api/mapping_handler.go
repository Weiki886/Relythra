package api

import (
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

type Dependencies struct {
	Reader             Reader
	Mappings           MappingRepository
	AdminAuthenticator AdminAuthenticator
	ConnectionTester   ConnectionTester
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
	TemplateID string    `json:"template_id"`
	Version    int       `json:"version"`
	Service    string    `json:"service"`
	Start      time.Time `json:"start"`
	End        time.Time `json:"end"`
	Limit      int       `json:"limit"`
	Cursor     string    `json:"cursor"`
	MaxSeries  int       `json:"max_series"`
}

type validationView struct {
	Valid  bool                         `json:"valid"`
	Issues []datasource.ValidationIssue `json:"issues"`
}

func (handler *handler) manageMappingCollection(w http.ResponseWriter, r *http.Request) {
	if !handler.authorizeManagement(w, r) {
		return
	}
	switch r.Method {
	case http.MethodGet:
		handler.listMappings(w, r)
	case http.MethodPost:
		handler.createMapping(w, r)
	default:
		writeMethodNotAllowed(w, r, http.MethodGet, http.MethodPost)
	}
}

func (handler *handler) manageMappingItem(w http.ResponseWriter, r *http.Request) {
	if !handler.authorizeManagement(w, r) {
		return
	}
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
}

func (handler *handler) manageMappingAction(w http.ResponseWriter, r *http.Request) {
	if !handler.authorizeManagement(w, r) {
		return
	}
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
}

func (handler *handler) authorizeManagement(w http.ResponseWriter, r *http.Request) bool {
	if handler.adminAuthenticator == nil {
		writeError(w, r, http.StatusServiceUnavailable, "management_unavailable", "management API is unavailable")
		return false
	}
	err := handler.adminAuthenticator.Authenticate(r)
	switch {
	case err == nil:
		return true
	case errors.Is(err, ErrUnauthorized):
		w.Header().Set("WWW-Authenticate", "Bearer")
		writeError(w, r, http.StatusUnauthorized, "unauthorized", "management authorization is required")
	case errors.Is(err, ErrManagementUnavailable):
		writeError(w, r, http.StatusServiceUnavailable, "management_unavailable", "management API is unavailable")
	default:
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
		TemplateID: normalized.TemplateID,
		Version:    normalized.Version,
		Service:    normalized.Service,
		Start:      normalized.Start,
		End:        normalized.End,
		Limit:      normalized.Limit,
		Cursor:     normalized.Cursor,
		MaxSeries:  normalized.MaxSeries,
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
	if mapping.SourceType == datasource.SourceRuntime &&
		mapping.RuntimeType == datasource.RuntimeKubernetes {
		writeError(w, r, http.StatusUnprocessableEntity, "unsupported_runtime", "Kubernetes runtime is not supported in v1")
		return
	}
	if issues := validateStoredMapping(mapping); len(issues) > 0 {
		handler.writeValidationIssues(w, r, issues)
		return
	}
	if handler.connectionTester == nil {
		writeError(w, r, http.StatusServiceUnavailable, "connection_test_unavailable", "connection testing is unavailable")
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
			return template, true
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
	parameters.Start, _ = queryString(values, "start", &issues)
	parameters.End, _ = queryString(values, "end", &issues)
	parameters.Cursor, _ = queryString(values, "cursor", &issues)
	parameters.Limit, _ = queryInteger(values, "limit", &issues)
	parameters.MaxSeries, _ = queryInteger(values, "max_series", &issues)
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
		writeError(w, r, http.StatusConflict, "stale_version", "mapping version is stale")
	case errors.Is(err, store.ErrInvalidInput):
		writeError(w, r, http.StatusUnprocessableEntity, "invalid_request", "mapping request is invalid")
	default:
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
		writeError(w, r, http.StatusBadGateway, "connection_test_failed", "connection test failed")
		return
	}
	switch testError.Category {
	case health.ErrorInvalidMapping:
		writeError(w, r, http.StatusUnprocessableEntity, "invalid_mapping", "mapping cannot be tested")
	case health.ErrorSecretUnavailable:
		writeError(w, r, http.StatusUnprocessableEntity, "secret_unavailable", "mapping secret is unavailable")
	case health.ErrorPolicyRejected:
		writeError(w, r, http.StatusUnprocessableEntity, "policy_rejected", "outbound target is not allowed")
	case health.ErrorTimeout:
		writeError(w, r, http.StatusGatewayTimeout, "connection_timeout", "data source connection timed out")
	case health.ErrorResponseTooLarge:
		writeError(w, r, http.StatusBadGateway, "response_too_large", "data source response exceeded the limit")
	default:
		writeError(w, r, http.StatusBadGateway, "connection_test_failed", "connection test failed")
	}
}

func writeMethodNotAllowed(w http.ResponseWriter, r *http.Request, methods ...string) {
	w.Header().Set("Allow", strings.Join(methods, ", "))
	writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method is not allowed")
}
