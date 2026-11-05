package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"time"

	"github.com/Weiki886/relythra/internal/domain"
	"github.com/Weiki886/relythra/internal/security/redact"
)

const requestIDHeader = "X-Request-ID"

type requestIDContextKey struct{}

var safeRequestID = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

func WithRequestID(next http.Handler, filters ...*redact.Filter) http.Handler {
	filter, err := redact.NewFilter()
	if err != nil {
		panic("initialize request ID redaction filter: " + err.Error())
	}
	if len(filters) > 0 && filters[0] != nil {
		filter = filters[0]
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := r.Header.Get(requestIDHeader)
		safeID, redactErr := filter.Text(r.Context(), requestID)
		if !safeRequestID.MatchString(requestID) || redactErr != nil || safeID != requestID {
			generated, err := domain.NewID()
			if err != nil {
				generated = fmt.Sprintf("request-%d", time.Now().UnixNano())
			}
			requestID = generated
		}

		w.Header().Set(requestIDHeader, requestID)
		ctx := context.WithValue(r.Context(), requestIDContextKey{}, requestID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func RequestIDFromContext(ctx context.Context) string {
	requestID, _ := ctx.Value(requestIDContextKey{}).(string)
	return requestID
}

type successEnvelope struct {
	Data      any    `json:"data"`
	RequestID string `json:"request_id"`
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

type errorEnvelope struct {
	Error     errorBody `json:"error"`
	RequestID string    `json:"request_id"`
}

func writeError(w http.ResponseWriter, r *http.Request, status int, code string, message string) {
	writeErrorDetails(w, r, status, code, message, nil)
}

func writeErrorDetails(
	w http.ResponseWriter,
	r *http.Request,
	status int,
	code string,
	message string,
	details any,
) {
	_ = writeJSON(w, status, errorEnvelope{
		Error: errorBody{
			Code:    code,
			Message: message,
			Details: details,
		},
		RequestID: RequestIDFromContext(r.Context()),
	})
}

func writeJSON(w http.ResponseWriter, status int, body any) error {
	var encoded bytes.Buffer
	if err := json.NewEncoder(&encoded).Encode(body); err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, err := w.Write(encoded.Bytes())
	return err
}
