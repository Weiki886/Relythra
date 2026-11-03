package health

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

const readinessTimeout = 2 * time.Second

type Checker interface {
	PingContext(context.Context) error
}

type response struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

func NewHandler(checker Checker, logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, response{Status: "ok"})
	})
	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), readinessTimeout)
		defer cancel()

		if err := checker.PingContext(ctx); err != nil {
			logger.ErrorContext(r.Context(), "readiness check failed", "error", err)
			writeJSON(w, http.StatusServiceUnavailable, response{
				Status: "not_ready",
				Error:  "dependency unavailable",
			})
			return
		}
		writeJSON(w, http.StatusOK, response{Status: "ready"})
	})
	return mux
}

func writeJSON(w http.ResponseWriter, status int, body response) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
