package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/Weiki886/relythra/internal/datasource"
	"github.com/Weiki886/relythra/internal/domain"
	"github.com/Weiki886/relythra/internal/security/secrets"
	"github.com/jackc/pgx/v5/pgconn"
)

type CreateDataSourceMappingInput struct {
	ProjectID      string
	ServiceID      string
	Environment    string
	SourceType     datasource.SourceType
	RuntimeType    datasource.RuntimeType
	PublicSettings datasource.PublicSettings
	TemplateIDs    []string
	SecretRef      string
}

type UpdateDataSourceMappingInput struct {
	ID              string
	ExpectedVersion int
	ProjectID       string
	ServiceID       string
	Environment     string
	SourceType      datasource.SourceType
	RuntimeType     datasource.RuntimeType
	PublicSettings  datasource.PublicSettings
	TemplateIDs     []string
	SecretRef       string
}

type DataSourceMapping struct {
	ID             string                    `json:"id"`
	ProjectID      string                    `json:"project_id"`
	ServiceID      string                    `json:"service_id"`
	Environment    string                    `json:"environment"`
	SourceType     datasource.SourceType     `json:"source_type"`
	RuntimeType    datasource.RuntimeType    `json:"runtime_type,omitempty"`
	PublicSettings datasource.PublicSettings `json:"public_settings"`
	Templates      []datasource.Template     `json:"templates"`
	SecretRef      string                    `json:"-"`
	Version        int                       `json:"version"`
	CreatedAt      time.Time                 `json:"created_at"`
	UpdatedAt      time.Time                 `json:"updated_at"`
}

type queryTemplateDocument struct {
	Items []queryTemplateRecord `json:"items"`
}

type queryTemplateRecord struct {
	ID                string                `json:"id"`
	Version           int                   `json:"version"`
	Source            datasource.SourceType `json:"source"`
	Purpose           string                `json:"purpose"`
	ResultSchema      string                `json:"result_schema"`
	AllowedParameters []string              `json:"allowed_parameters"`
}

var environmentPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

const mappingColumns = `
	id::text,
	project_id::text,
	service_id::text,
	environment,
	source_type,
	COALESCE(runtime_type, ''),
	public_settings,
	query_templates,
	COALESCE(secret_ref, ''),
	version,
	created_at,
	updated_at
`

func (repository *Postgres) CreateDataSourceMapping(
	ctx context.Context,
	input CreateDataSourceMappingInput,
) (DataSourceMapping, []datasource.ValidationIssue, error) {
	normalized, issues, secretReference, err := validateCreateMappingInput(input)
	if err != nil {
		return DataSourceMapping{}, nil, err
	}
	templates, templateIssues := datasource.ResolveTemplates(normalized.SourceType, normalized.TemplateIDs)
	issues = append(issues, templateIssues...)
	if hasBlockingMappingIssue(issues) {
		return DataSourceMapping{}, issues, fmt.Errorf("%w: mapping configuration is invalid", ErrInvalidInput)
	}
	publicSettings, queryTemplates, err := encodeMappingConfiguration(
		normalized.PublicSettings,
		templates,
	)
	if err != nil {
		return DataSourceMapping{}, nil, fmt.Errorf("%w: mapping configuration could not be encoded", ErrInvalidInput)
	}

	mapping, err := scanDataSourceMapping(repository.db.QueryRowContext(ctx, `
		INSERT INTO data_source_mappings (
			project_id, service_id, environment, source_type, runtime_type,
			public_settings, query_templates, secret_ref
		) VALUES (
			$1, $2, $3, $4, NULLIF($5, ''), $6::jsonb, $7::jsonb, NULLIF($8, '')
		)
		RETURNING `+mappingColumns,
		normalized.ProjectID,
		normalized.ServiceID,
		normalized.Environment,
		normalized.SourceType,
		normalized.RuntimeType,
		string(publicSettings),
		string(queryTemplates),
		secretReferenceString(secretReference),
	))
	if err != nil {
		if isUniqueViolation(err) {
			return DataSourceMapping{}, issues, ErrConflict
		}
		if isForeignKeyViolation(err) {
			return DataSourceMapping{}, issues, fmt.Errorf(
				"%w: project and service do not identify the same owner",
				ErrInvalidInput,
			)
		}
		return DataSourceMapping{}, nil, fmt.Errorf("insert data source mapping: %w", err)
	}
	return mapping, issues, nil
}

func (repository *Postgres) GetDataSourceMapping(
	ctx context.Context,
	id string,
) (DataSourceMapping, error) {
	if !domain.IsID(id) {
		return DataSourceMapping{}, fmt.Errorf("%w: invalid mapping UUID", ErrInvalidInput)
	}
	mapping, err := scanDataSourceMapping(repository.db.QueryRowContext(ctx, `
		SELECT `+mappingColumns+`
		FROM data_source_mappings
		WHERE id = $1 AND deleted_at IS NULL
	`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return DataSourceMapping{}, ErrNotFound
	}
	if err != nil {
		return DataSourceMapping{}, fmt.Errorf("read data source mapping: %w", err)
	}
	return mapping, nil
}

func (repository *Postgres) ListDataSourceMappings(
	ctx context.Context,
	projectID string,
	serviceID string,
	environment string,
) ([]DataSourceMapping, error) {
	query := `
		SELECT ` + mappingColumns + `
		FROM data_source_mappings
		WHERE deleted_at IS NULL
	`
	arguments := make([]any, 0, 3)
	if projectID != "" {
		if !domain.IsID(projectID) {
			return nil, fmt.Errorf("%w: invalid project UUID", ErrInvalidInput)
		}
		arguments = append(arguments, projectID)
		query += fmt.Sprintf(" AND project_id = $%d", len(arguments))
	}
	if serviceID != "" {
		if !domain.IsID(serviceID) {
			return nil, fmt.Errorf("%w: invalid service UUID", ErrInvalidInput)
		}
		arguments = append(arguments, serviceID)
		query += fmt.Sprintf(" AND service_id = $%d", len(arguments))
	}
	if environment != "" {
		if !environmentPattern.MatchString(environment) {
			return nil, fmt.Errorf("%w: invalid environment", ErrInvalidInput)
		}
		arguments = append(arguments, environment)
		query += fmt.Sprintf(" AND environment = $%d", len(arguments))
	}
	query += " ORDER BY updated_at DESC, id DESC LIMIT 100"

	rows, err := repository.db.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, fmt.Errorf("list data source mappings: %w", err)
	}
	defer rows.Close()

	mappings := make([]DataSourceMapping, 0)
	for rows.Next() {
		mapping, err := scanDataSourceMapping(rows)
		if err != nil {
			return nil, fmt.Errorf("scan data source mapping: %w", err)
		}
		mappings = append(mappings, mapping)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate data source mappings: %w", err)
	}
	return mappings, nil
}

func (repository *Postgres) UpdateDataSourceMapping(
	ctx context.Context,
	input UpdateDataSourceMappingInput,
) (DataSourceMapping, []datasource.ValidationIssue, error) {
	if !domain.IsID(input.ID) || input.ExpectedVersion < 1 {
		return DataSourceMapping{}, nil, fmt.Errorf("%w: mapping ID and version are required", ErrInvalidInput)
	}
	createInput := CreateDataSourceMappingInput{
		ProjectID:      input.ProjectID,
		ServiceID:      input.ServiceID,
		Environment:    input.Environment,
		SourceType:     input.SourceType,
		RuntimeType:    input.RuntimeType,
		PublicSettings: input.PublicSettings,
		TemplateIDs:    input.TemplateIDs,
		SecretRef:      input.SecretRef,
	}
	normalized, issues, secretReference, err := validateCreateMappingInput(createInput)
	if err != nil {
		return DataSourceMapping{}, nil, err
	}
	templates, templateIssues := datasource.ResolveTemplates(normalized.SourceType, normalized.TemplateIDs)
	issues = append(issues, templateIssues...)
	if hasBlockingMappingIssue(issues) {
		return DataSourceMapping{}, issues, fmt.Errorf("%w: mapping configuration is invalid", ErrInvalidInput)
	}
	publicSettings, queryTemplates, err := encodeMappingConfiguration(
		normalized.PublicSettings,
		templates,
	)
	if err != nil {
		return DataSourceMapping{}, nil, fmt.Errorf("%w: mapping configuration could not be encoded", ErrInvalidInput)
	}

	mapping, err := scanDataSourceMapping(repository.db.QueryRowContext(ctx, `
		UPDATE data_source_mappings
		SET project_id = $1,
		    service_id = $2,
		    environment = $3,
		    source_type = $4,
		    runtime_type = NULLIF($5, ''),
		    public_settings = $6::jsonb,
		    query_templates = $7::jsonb,
		    secret_ref = NULLIF($8, ''),
		    version = version + 1
		WHERE id = $9 AND version = $10 AND deleted_at IS NULL
		RETURNING `+mappingColumns,
		normalized.ProjectID,
		normalized.ServiceID,
		normalized.Environment,
		normalized.SourceType,
		normalized.RuntimeType,
		string(publicSettings),
		string(queryTemplates),
		secretReferenceString(secretReference),
		input.ID,
		input.ExpectedVersion,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return DataSourceMapping{}, issues, repository.mappingWriteMiss(ctx, input.ID)
	}
	if err != nil {
		if isUniqueViolation(err) {
			return DataSourceMapping{}, issues, ErrConflict
		}
		if isForeignKeyViolation(err) {
			return DataSourceMapping{}, issues, fmt.Errorf(
				"%w: project and service do not identify the same owner",
				ErrInvalidInput,
			)
		}
		return DataSourceMapping{}, nil, fmt.Errorf("update data source mapping: %w", err)
	}
	return mapping, issues, nil
}

func (repository *Postgres) DeleteDataSourceMapping(
	ctx context.Context,
	id string,
	expectedVersion int,
) error {
	if !domain.IsID(id) || expectedVersion < 1 {
		return fmt.Errorf("%w: mapping ID and version are required", ErrInvalidInput)
	}
	result, err := repository.db.ExecContext(ctx, `
		UPDATE data_source_mappings
		SET deleted_at = now(), version = version + 1
		WHERE id = $1 AND version = $2 AND deleted_at IS NULL
	`, id, expectedVersion)
	if err != nil {
		return fmt.Errorf("delete data source mapping: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read deleted mapping count: %w", err)
	}
	if affected == 0 {
		return repository.mappingWriteMiss(ctx, id)
	}
	return nil
}

func validateCreateMappingInput(
	input CreateDataSourceMappingInput,
) (
	CreateDataSourceMappingInput,
	[]datasource.ValidationIssue,
	*secrets.Reference,
	error,
) {
	for _, id := range []string{input.ProjectID, input.ServiceID} {
		if !domain.IsID(id) {
			return CreateDataSourceMappingInput{}, nil, nil, fmt.Errorf(
				"%w: project and service UUIDs are required",
				ErrInvalidInput,
			)
		}
	}
	input.Environment = strings.TrimSpace(input.Environment)
	if !environmentPattern.MatchString(input.Environment) {
		return CreateDataSourceMappingInput{}, nil, nil, fmt.Errorf(
			"%w: invalid environment",
			ErrInvalidInput,
		)
	}
	input.PublicSettings.Endpoint = strings.TrimSpace(input.PublicSettings.Endpoint)
	input.PublicSettings.Repository = strings.TrimSpace(input.PublicSettings.Repository)
	input.PublicSettings.RuntimeLabels = normalizedStringMap(input.PublicSettings.RuntimeLabels)

	var reference *secrets.Reference
	rawSecretReference := strings.TrimSpace(input.SecretRef)
	if rawSecretReference != "" {
		parsed, err := secrets.ParseReference(rawSecretReference)
		if err != nil {
			return CreateDataSourceMappingInput{}, nil, nil, fmt.Errorf(
				"%w: secret reference is invalid",
				ErrInvalidInput,
			)
		}
		reference = &parsed
		input.SecretRef = parsed.String()
	}
	issues := datasource.ValidateMapping(
		input.SourceType,
		input.RuntimeType,
		input.PublicSettings,
		input.TemplateIDs,
		reference,
	)
	return input, issues, reference, nil
}

func hasBlockingMappingIssue(issues []datasource.ValidationIssue) bool {
	for _, issue := range issues {
		switch {
		case issue.Code == "required" &&
			strings.HasPrefix(issue.Field, "runtime_labels."):
			continue
		case issue.Code == "required" && issue.Field == "template_ids":
			continue
		case issue.Code == "unsupported_runtime" && issue.Field == "runtime_type":
			continue
		default:
			return true
		}
	}
	return false
}

func encodeMappingConfiguration(
	settings datasource.PublicSettings,
	templates []datasource.Template,
) (json.RawMessage, json.RawMessage, error) {
	publicSettings, err := json.Marshal(settings)
	if err != nil {
		return nil, nil, err
	}
	document := queryTemplateDocument{Items: make([]queryTemplateRecord, 0, len(templates))}
	for _, template := range templates {
		document.Items = append(document.Items, queryTemplateRecord{
			ID:                template.ID,
			Version:           template.Version,
			Source:            template.Source,
			Purpose:           template.Purpose,
			ResultSchema:      template.ResultSchema,
			AllowedParameters: append([]string(nil), template.AllowedParameters...),
		})
	}
	queryTemplates, err := json.Marshal(document)
	if err != nil {
		return nil, nil, err
	}
	return publicSettings, queryTemplates, nil
}

func scanDataSourceMapping(scanner interface {
	Scan(...any) error
}) (DataSourceMapping, error) {
	var mapping DataSourceMapping
	var publicSettingsJSON json.RawMessage
	var queryTemplatesJSON json.RawMessage
	if err := scanner.Scan(
		&mapping.ID,
		&mapping.ProjectID,
		&mapping.ServiceID,
		&mapping.Environment,
		&mapping.SourceType,
		&mapping.RuntimeType,
		&publicSettingsJSON,
		&queryTemplatesJSON,
		&mapping.SecretRef,
		&mapping.Version,
		&mapping.CreatedAt,
		&mapping.UpdatedAt,
	); err != nil {
		return DataSourceMapping{}, err
	}
	if err := json.Unmarshal(publicSettingsJSON, &mapping.PublicSettings); err != nil {
		return DataSourceMapping{}, fmt.Errorf("decode public settings: %w", err)
	}
	var templates queryTemplateDocument
	if err := json.Unmarshal(queryTemplatesJSON, &templates); err != nil {
		return DataSourceMapping{}, fmt.Errorf("decode query templates: %w", err)
	}
	mapping.Templates = make([]datasource.Template, 0, len(templates.Items))
	for _, template := range templates.Items {
		mapping.Templates = append(mapping.Templates, datasource.Template{
			ID:                template.ID,
			Version:           template.Version,
			Source:            template.Source,
			Purpose:           template.Purpose,
			ResultSchema:      template.ResultSchema,
			AllowedParameters: append([]string(nil), template.AllowedParameters...),
		})
	}
	return mapping, nil
}

func (repository *Postgres) mappingWriteMiss(ctx context.Context, id string) error {
	var exists bool
	err := repository.db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM data_source_mappings
			WHERE id = $1 AND deleted_at IS NULL
		)
	`, id).Scan(&exists)
	if err != nil {
		return fmt.Errorf("check data source mapping existence: %w", err)
	}
	if !exists {
		return ErrNotFound
	}
	return ErrConflict
}

func secretReferenceString(reference *secrets.Reference) string {
	if reference == nil {
		return ""
	}
	return reference.String()
}

func normalizedStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	normalized := make(map[string]string, len(values))
	for key, value := range values {
		normalized[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return normalized
}

func isUniqueViolation(err error) bool {
	return hasPostgresSQLState(err, "23505")
}

func isForeignKeyViolation(err error) bool {
	return hasPostgresSQLState(err, "23503")
}

func hasPostgresSQLState(err error, code string) bool {
	var pgError *pgconn.PgError
	return errors.As(err, &pgError) && pgError.Code == code
}
