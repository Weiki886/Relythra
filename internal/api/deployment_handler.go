package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Weiki886/relythra/internal/domain"
	"github.com/Weiki886/relythra/internal/security/audit"
	"github.com/Weiki886/relythra/internal/store"
)

const maxDeploymentWebhookBodyBytes = 256 * 1024

type deploymentWriteRequest struct {
	ExternalDeploymentID string          `json:"external_deployment_id"`
	ProjectID            string          `json:"project_id"`
	ServiceID            string          `json:"service_id"`
	ReleaseID            string          `json:"release_id"`
	Environment          string          `json:"environment"`
	Status               string          `json:"status"`
	StartedAt            time.Time       `json:"started_at"`
	FinishedAt           *time.Time      `json:"finished_at"`
	Version              string          `json:"version"`
	CommitSHA            string          `json:"commit_sha"`
	ImageDigest          string          `json:"image_digest"`
	InstanceCount        int             `json:"instance_count"`
	SourceReference      string          `json:"source_reference"`
	SourceTimezone       string          `json:"source_timezone"`
	SourceTimeText       string          `json:"source_time_text"`
	Metadata             json.RawMessage `json:"metadata"`
}

type deploymentCorrectionRequest struct {
	ExpectedVersion int64      `json:"expected_version"`
	Reason          string     `json:"reason"`
	Status          string     `json:"status"`
	StartedAt       *time.Time `json:"started_at"`
	FinishedAt      *time.Time `json:"finished_at"`
	ReleaseID       string     `json:"release_id"`
	Version         string     `json:"version"`
	CommitSHA       string     `json:"commit_sha"`
	ImageDigest     string     `json:"image_digest"`
	InstanceCount   int        `json:"instance_count"`
	SourceReference string     `json:"source_reference"`
}

type deploymentCandidateResponse struct {
	Items      []store.DeploymentEvent `json:"items"`
	MatchState string                  `json:"match_state"`
}

func (handler *handler) receiveDeploymentWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, r, http.MethodPost)
		return
	}
	if handler.deployments == nil || handler.auditRecorder == nil || handler.deploymentWebhookSecret == "" || handler.deploymentWebhookSource == "" {
		writeError(w, r, http.StatusServiceUnavailable, "deployment_ingestion_unavailable", "deployment ingestion is unavailable")
		return
	}
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type"))), "application/json") {
		handler.recordDeploymentWebhookResult(w, r, audit.ResultDenied, "unsupported_media_type", "application/json is required", "unverified")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxDeploymentWebhookBodyBytes+1))
	if err != nil || len(body) > maxDeploymentWebhookBodyBytes {
		handler.recordDeploymentWebhookResult(w, r, audit.ResultDenied, "request_too_large", "deployment webhook body exceeded the limit", "unverified")
		return
	}
	if err := VerifyDeploymentSignature(
		[]byte(handler.deploymentWebhookSecret),
		r.Header.Get("X-Relythra-Timestamp"),
		r.Header.Get("X-Relythra-Signature"),
		body,
		time.Now().UTC(),
	); err != nil {
		handler.recordDeploymentWebhookResult(w, r, audit.ResultDenied, "invalid_signature", "deployment signature is invalid or expired", "unverified")
		return
	}
	deploymentRequest, err := parseDeploymentWriteRequest(body)
	if err != nil {
		handler.recordDeploymentWebhookResult(w, r, audit.ResultFailed, "invalid_request", "deployment request is invalid", "unverified")
		return
	}
	signatureDigest, err := DeploymentSignatureReplayKey(r.Header.Get("X-Relythra-Signature"))
	if err != nil {
		handler.recordDeploymentWebhookResult(w, r, audit.ResultDenied, "invalid_signature", "deployment signature is invalid or expired", "unverified")
		return
	}
	signatureExpiresAt, err := DeploymentSignatureExpiresAt(r.Header.Get("X-Relythra-Timestamp"))
	if err != nil {
		handler.recordDeploymentWebhookResult(w, r, audit.ResultDenied, "invalid_signature", "deployment signature is invalid or expired", "unverified")
		return
	}
	claimed, err := handler.deployments.ClaimDeploymentWebhookSignature(r.Context(), signatureDigest, signatureExpiresAt)
	if err != nil {
		handler.writeStoreError(w, r, err)
		return
	}
	if !claimed {
		handler.recordDeploymentWebhookResult(w, r, audit.ResultDenied, "replayed_signature", "deployment signature has already been used", "unverified")
		return
	}
	event, created, err := handler.ingestDeploymentRequest(r.Context(), deploymentRequest, handler.deploymentWebhookSource, handler.deploymentWebhookSource, body)
	if err != nil {
		handler.writeStoreError(w, r, err)
		return
	}
	result := audit.ResultSucceeded
	if _, err := handler.recordDeploymentWebhookResult(w, r, result, "", "", event.ID); err != nil {
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	handler.writeData(w, r, status, event)
}

func (handler *handler) manageDeploymentCollection(w http.ResponseWriter, r *http.Request) {
	if !handler.authorizeManagement(w, r) {
		return
	}
	handler.withManagementAudit(w, r, func(w http.ResponseWriter, r *http.Request) {
		setDeploymentManagementAudit(r, audit.ActionDeploymentEventChanged, "collection")
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, r, http.MethodPost)
			return
		}
		if handler.deployments == nil {
			writeError(w, r, http.StatusServiceUnavailable, "deployment_ingestion_unavailable", "deployment ingestion is unavailable")
			return
		}
		request, err := decodeDeploymentRequest(r.Body)
		if err != nil {
			writeError(w, r, http.StatusBadRequest, "invalid_request", "deployment request is invalid")
			return
		}
		rawBody, _ := json.Marshal(request)
		event, created, err := handler.ingestDeploymentRequest(r.Context(), request, "manual", handler.managementActor, rawBody)
		if err != nil {
			handler.writeStoreError(w, r, err)
			return
		}
		status := http.StatusOK
		if created {
			status = http.StatusCreated
		}
		handler.writeData(w, r, status, event)
	})
}

func (handler *handler) manageDeploymentItem(w http.ResponseWriter, r *http.Request) {
	if !handler.authorizeManagement(w, r) {
		return
	}
	handler.withManagementAudit(w, r, func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		setDeploymentManagementAudit(r, audit.ActionDeploymentEventChanged, id)
		if r.Method != http.MethodPatch {
			writeMethodNotAllowed(w, r, http.MethodPatch)
			return
		}
		if handler.deployments == nil {
			writeError(w, r, http.StatusServiceUnavailable, "deployment_ingestion_unavailable", "deployment ingestion is unavailable")
			return
		}
		if !domain.IsID(id) {
			writeError(w, r, http.StatusBadRequest, "invalid_request", "deployment ID is invalid")
			return
		}
		var request deploymentCorrectionRequest
		if !decodeStrictJSON(w, r, &request, false) {
			return
		}
		if request.ExpectedVersion < 1 || strings.TrimSpace(request.Reason) == "" {
			writeError(w, r, http.StatusBadRequest, "invalid_request", "expected_version and reason are required")
			return
		}
		if request.SourceReference != "" {
			safeReference, redactErr := handler.filter.Text(r.Context(), request.SourceReference)
			if redactErr != nil || safeReference != request.SourceReference {
				writeError(w, r, http.StatusBadRequest, "invalid_request", "source_reference contains unsafe data")
				return
			}
		}
		event, err := handler.deployments.UpdateDeploymentEvent(r.Context(), id, store.UpdateDeploymentEventInput{
			ExpectedVersion: request.ExpectedVersion,
			Actor:           handler.managementActor,
			RequestID:       RequestIDFromContext(r.Context()),
			Reason:          request.Reason,
			Status:          request.Status,
			FinishedAt:      request.FinishedAt,
			ReleaseID:       request.ReleaseID,
			Version:         request.Version,
			CommitSHA:       request.CommitSHA,
			ImageDigest:     request.ImageDigest,
			InstanceCount:   request.InstanceCount,
			StartedAt:       request.StartedAt,
			SourceReference: request.SourceReference,
		})
		if err != nil {
			handler.writeStoreError(w, r, err)
			return
		}
		handler.writeData(w, r, http.StatusOK, event)
	})
}

func (handler *handler) getDeploymentCandidates(w http.ResponseWriter, r *http.Request) {
	if !handler.authorizeManagement(w, r) {
		return
	}
	handler.withManagementAudit(w, r, func(w http.ResponseWriter, r *http.Request) {
		setDeploymentManagementAudit(r, audit.ActionDeploymentEventChanged, "candidates")
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, r, http.MethodGet)
			return
		}
		if handler.deployments == nil {
			writeError(w, r, http.StatusServiceUnavailable, "deployment_ingestion_unavailable", "deployment ingestion is unavailable")
			return
		}
		start, end, err := parseDeploymentWindow(r.URL.Query().Get("start"), r.URL.Query().Get("end"))
		if err != nil {
			writeError(w, r, http.StatusBadRequest, "invalid_request", "deployment time window is invalid")
			return
		}
		limit, err := boundedQueryInt(r, "limit", 20, 100)
		if err != nil {
			writeError(w, r, http.StatusBadRequest, "invalid_request", "deployment candidate limit is invalid")
			return
		}
		items, err := handler.deployments.FindDeploymentCandidates(r.Context(), store.DeploymentCandidateQuery{
			ProjectID: r.URL.Query().Get("project_id"), ServiceID: r.URL.Query().Get("service_id"),
			Environment: r.URL.Query().Get("environment"), Start: start, End: end, Limit: limit,
		})
		if err != nil {
			handler.writeStoreError(w, r, err)
			return
		}
		matchState := "MATCHED"
		if len(items) == 0 {
			matchState = "NO_DEPLOYMENT_CONTEXT"
		} else if len(items) > 1 {
			matchState = "AMBIGUOUS"
		}
		handler.writeData(w, r, http.StatusOK, deploymentCandidateResponse{Items: items, MatchState: matchState})
	})
}

func (handler *handler) ingestDeploymentRequest(
	ctx context.Context,
	request deploymentWriteRequest,
	source string,
	actor string,
	rawBody []byte,
) (store.DeploymentEvent, bool, error) {
	if handler.deployments == nil {
		return store.DeploymentEvent{}, false, store.ErrNotFound
	}
	metadata, err := handler.filter.JSON(ctx, json.RawMessage(rawBody))
	if err != nil {
		return store.DeploymentEvent{}, false, fmt.Errorf("redact deployment payload")
	}
	metadataJSON, err := json.Marshal(map[string]any{"payload": metadata})
	if err != nil {
		return store.DeploymentEvent{}, false, fmt.Errorf("encode deployment payload")
	}
	sourceReference, err := handler.filter.Text(ctx, request.SourceReference)
	if err != nil {
		return store.DeploymentEvent{}, false, store.ErrInvalidInput
	}
	input := store.CreateDeploymentEventInput{
		ProjectID: request.ProjectID, ServiceID: request.ServiceID, ReleaseID: request.ReleaseID,
		Environment: request.Environment, SourceSystem: source,
		IdempotencyKey: source + ":" + request.ExternalDeploymentID,
		RequestID:      RequestIDFromContext(ctx), Status: request.Status,
		StartedAt: request.StartedAt, FinishedAt: request.FinishedAt,
		Version: request.Version, CommitSHA: strings.ToLower(request.CommitSHA), ImageDigest: request.ImageDigest,
		InstanceCount: request.InstanceCount, SourceReference: sourceReference,
		SourceTimezone: request.SourceTimezone, SourceTimeText: request.SourceTimeText,
		Metadata: metadataJSON,
	}
	event, created, err := handler.deployments.CreateDeploymentEvent(ctx, input)
	if err != nil {
		return store.DeploymentEvent{}, false, err
	}
	if !created && (event.Status != request.Status || !sameOptionalTime(event.FinishedAt, request.FinishedAt)) {
		event, err = handler.deployments.UpdateDeploymentEvent(ctx, event.ID, store.UpdateDeploymentEventInput{
			ExpectedVersion: event.EventVersion,
			Actor:           actor, RequestID: RequestIDFromContext(ctx),
			Reason: "deployment webhook status update", Status: request.Status, FinishedAt: request.FinishedAt,
		})
		if err != nil {
			return store.DeploymentEvent{}, false, err
		}
	}
	return event, created, nil
}

func parseDeploymentWriteRequest(body []byte) (deploymentWriteRequest, error) {
	return decodeDeploymentRequest(bytes.NewReader(body))
}

func decodeDeploymentRequest(reader io.Reader) (deploymentWriteRequest, error) {
	data, err := io.ReadAll(io.LimitReader(reader, maxDeploymentWebhookBodyBytes+1))
	if err != nil || len(data) > maxDeploymentWebhookBodyBytes {
		return deploymentWriteRequest{}, store.ErrInvalidInput
	}
	var request deploymentWriteRequest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return deploymentWriteRequest{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return deploymentWriteRequest{}, errors.New("deployment request contains trailing JSON")
	}
	if strings.TrimSpace(request.ExternalDeploymentID) == "" || len(request.ExternalDeploymentID) > 256 ||
		!domain.IsID(request.ProjectID) || !domain.IsID(request.ServiceID) || !domain.IsID(request.ReleaseID) ||
		!validDeploymentEnvironment(request.Environment) || !domain.DeploymentStatus(request.Status).Valid() ||
		strings.TrimSpace(request.Version) == "" || len(request.Version) > 256 || request.InstanceCount < 1 ||
		domain.ValidateTimeRange(request.StartedAt, request.StartedAt) != nil ||
		(request.FinishedAt != nil && request.FinishedAt.Before(request.StartedAt)) {
		return deploymentWriteRequest{}, store.ErrInvalidInput
	}
	if isTerminalDeploymentStatus(request.Status) && request.FinishedAt == nil {
		return deploymentWriteRequest{}, store.ErrInvalidInput
	}
	if len(request.CommitSHA) != 40 && len(request.CommitSHA) != 64 {
		return deploymentWriteRequest{}, store.ErrInvalidInput
	}
	for _, value := range request.CommitSHA {
		if !(value >= '0' && value <= '9' || value >= 'a' && value <= 'f' || value >= 'A' && value <= 'F') {
			return deploymentWriteRequest{}, store.ErrInvalidInput
		}
	}
	if request.ImageDigest != "" && (len(request.ImageDigest) != 71 || !strings.HasPrefix(request.ImageDigest, "sha256:")) {
		return deploymentWriteRequest{}, store.ErrInvalidInput
	}
	if len(request.ImageDigest) == 71 {
		for _, value := range request.ImageDigest[7:] {
			if !(value >= '0' && value <= '9' || value >= 'a' && value <= 'f') {
				return deploymentWriteRequest{}, store.ErrInvalidInput
			}
		}
	}
	if len(request.Metadata) > 0 && !json.Valid(request.Metadata) {
		return deploymentWriteRequest{}, store.ErrInvalidInput
	}
	if request.SourceReference != "" {
		parsed, err := url.ParseRequestURI(request.SourceReference)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
			return deploymentWriteRequest{}, store.ErrInvalidInput
		}
	}
	return request, nil
}

func (handler *handler) recordDeploymentWebhookResult(
	w http.ResponseWriter,
	r *http.Request,
	result audit.Result,
	errorCode string,
	message string,
	objectID string,
) (int64, error) {
	if handler.auditRecorder == nil {
		writeError(w, r, http.StatusServiceUnavailable, "deployment_ingestion_unavailable", "deployment ingestion audit is unavailable")
		return 0, errors.New("deployment ingestion audit unavailable")
	}
	parameters := map[string]any{"source": handler.deploymentWebhookSource}
	if errorCode != "" {
		parameters["error_code"] = errorCode
	}
	id, err := handler.auditRecorder.Record(r.Context(), audit.Event{
		Actor: handler.deploymentWebhookSource, Action: audit.ActionWebhookReceived,
		ObjectType: "deployment_event", ObjectID: objectID, Result: result,
		RequestID: RequestIDFromContext(r.Context()), Parameters: parameters, OccurredAt: time.Now().UTC(),
	})
	if err != nil {
		writeError(w, r, http.StatusServiceUnavailable, "deployment_ingestion_unavailable", "deployment ingestion audit is unavailable")
		return 0, err
	}
	if errorCode != "" {
		status := http.StatusBadRequest
		if errorCode == "invalid_signature" {
			status = http.StatusUnauthorized
		} else if errorCode == "replayed_signature" {
			status = http.StatusConflict
		}
		writeError(w, r, status, errorCode, message)
	}
	return id, nil
}

func setDeploymentManagementAudit(r *http.Request, action audit.Action, objectID string) {
	if state := managementAuditStateFromContext(r); state != nil {
		state.Action = action
		state.ObjectType = "deployment_event"
		state.ObjectID = objectID
	}
}

func parseDeploymentWindow(startText, endText string) (time.Time, time.Time, error) {
	start, startErr := time.Parse(time.RFC3339Nano, startText)
	end, endErr := time.Parse(time.RFC3339Nano, endText)
	if startErr != nil || endErr != nil || domain.ValidateTimeRange(start, end) != nil || end.Sub(start) > 24*time.Hour {
		return time.Time{}, time.Time{}, store.ErrInvalidInput
	}
	return start.UTC(), end.UTC(), nil
}

func validDeploymentEnvironment(value string) bool {
	if value == "" || len(value) > 32 || !(value[0] >= 'a' && value[0] <= 'z' || value[0] >= '0' && value[0] <= '9') {
		return false
	}
	for _, character := range value {
		if !(character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '-') {
			return false
		}
	}
	return true
}

func isTerminalDeploymentStatus(status string) bool {
	switch status {
	case string(domain.DeploymentSucceeded), string(domain.DeploymentFailed), string(domain.DeploymentRolledBack):
		return true
	default:
		return false
	}
}

func sameOptionalTime(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}
