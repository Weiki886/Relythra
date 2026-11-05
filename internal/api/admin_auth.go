package api

import (
	"crypto/subtle"
	"errors"
	"net/http"

	"github.com/Weiki886/relythra/internal/security/secrets"
)

var (
	ErrUnauthorized          = errors.New("management authorization failed")
	ErrManagementUnavailable = errors.New("management API is unavailable")
)

// AdminAuthenticator validates access to privileged management endpoints.
// Implementations must fail closed when configuration is incomplete.
type AdminAuthenticator interface {
	Authenticate(*http.Request) error
}

// BearerTokenAuthenticator implements the single-administrator token boundary
// used by v1. It is intentionally not a role-based authorization system.
type BearerTokenAuthenticator struct {
	token secrets.Secret
}

func NewBearerTokenAuthenticator(token secrets.Secret) (*BearerTokenAuthenticator, error) {
	if token.Reveal() == "" {
		return nil, ErrManagementUnavailable
	}
	return &BearerTokenAuthenticator{token: token}, nil
}

func (authenticator *BearerTokenAuthenticator) Authenticate(request *http.Request) error {
	if authenticator == nil || authenticator.token.Reveal() == "" || request == nil {
		return ErrManagementUnavailable
	}
	expected := []byte("Bearer " + authenticator.token.Reveal())
	provided := []byte(request.Header.Get("Authorization"))
	if len(expected) != len(provided) ||
		subtle.ConstantTimeCompare(expected, provided) != 1 {
		return ErrUnauthorized
	}
	return nil
}

// ManagementUnavailableAuthenticator keeps management routes mounted while
// returning a stable unavailable error when no administrator token is set.
type ManagementUnavailableAuthenticator struct{}

func (ManagementUnavailableAuthenticator) Authenticate(*http.Request) error {
	return ErrManagementUnavailable
}
