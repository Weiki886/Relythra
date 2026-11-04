package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/Weiki886/relythra/internal/domain"
	"github.com/Weiki886/relythra/internal/store"
)

type Reader interface {
	GetReport(context.Context, string) (store.Report, error)
	GetEvidence(context.Context, string) (store.Evidence, error)
	GetDeploymentEvent(context.Context, string) (store.DeploymentEvent, error)
}

type handler struct {
	reader Reader
	logger *slog.Logger
}

func NewHandler(reader Reader, logger *slog.Logger) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	apiHandler := &handler{reader: reader, logger: logger}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/reports/{id}", apiHandler.getReport)
	mux.HandleFunc("/api/v1/evidence/{id}", apiHandler.getEvidence)
	mux.HandleFunc("/api/v1/deployments/{id}", apiHandler.getDeployment)
	mux.HandleFunc("/api/v1/", apiHandler.notFound)
	return WithRequestID(mux)
}

func (handler *handler) getReport(w http.ResponseWriter, r *http.Request) {
	if !requireGET(w, r) {
		return
	}
	id, ok := requireID(w, r)
	if !ok {
		return
	}
	report, err := handler.reader.GetReport(r.Context(), id)
	if err != nil {
		handler.writeStoreError(w, r, err)
		return
	}
	writeData(w, r, http.StatusOK, report)
}

func (handler *handler) getEvidence(w http.ResponseWriter, r *http.Request) {
	if !requireGET(w, r) {
		return
	}
	id, ok := requireID(w, r)
	if !ok {
		return
	}
	evidence, err := handler.reader.GetEvidence(r.Context(), id)
	if err != nil {
		handler.writeStoreError(w, r, err)
		return
	}
	writeData(w, r, http.StatusOK, evidence)
}

func (handler *handler) getDeployment(w http.ResponseWriter, r *http.Request) {
	if !requireGET(w, r) {
		return
	}
	id, ok := requireID(w, r)
	if !ok {
		return
	}
	deployment, err := handler.reader.GetDeploymentEvent(r.Context(), id)
	if err != nil {
		handler.writeStoreError(w, r, err)
		return
	}
	writeData(w, r, http.StatusOK, deployment)
}

func (handler *handler) notFound(w http.ResponseWriter, r *http.Request) {
	writeError(w, r, http.StatusNotFound, "not_found", "resource was not found")
}

func (handler *handler) writeStoreError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, store.ErrInvalidInput):
		writeError(w, r, http.StatusBadRequest, "invalid_request", "request is invalid")
	case errors.Is(err, store.ErrNotFound):
		writeError(w, r, http.StatusNotFound, "not_found", "resource was not found")
	case errors.Is(err, store.ErrConflict):
		writeError(w, r, http.StatusConflict, "conflict", "resource changed; retry the request")
	default:
		handler.logger.ErrorContext(
			r.Context(),
			"domain read failed",
			"request_id", RequestIDFromContext(r.Context()),
			"path", r.URL.Path,
			"error", err,
		)
		writeError(
			w,
			r,
			http.StatusInternalServerError,
			"internal_error",
			"an internal error occurred",
		)
	}
}

func requireGET(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodGet {
		return true
	}
	w.Header().Set("Allow", http.MethodGet)
	writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method is not allowed")
	return false
}

func requireID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if !domain.IsID(id) {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "resource ID is invalid")
		return "", false
	}
	return id, true
}
