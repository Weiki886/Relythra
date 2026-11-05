package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Weiki886/relythra/internal/security/secrets"
)

func TestBearerTokenAuthenticatorRequiresExactCredential(t *testing.T) {
	t.Parallel()

	token, err := secrets.New("management-token-canary")
	if err != nil {
		t.Fatalf("new token: %v", err)
	}
	authenticator, err := NewBearerTokenAuthenticator(token)
	if err != nil {
		t.Fatalf("new authenticator: %v", err)
	}

	tests := []struct {
		name       string
		header     string
		wantErr    error
		wantResult bool
	}{
		{
			name:       "matching bearer token",
			header:     "Bearer management-token-canary",
			wantResult: true,
		},
		{
			name:    "missing header",
			wantErr: ErrUnauthorized,
		},
		{
			name:    "wrong token",
			header:  "Bearer management-token-other",
			wantErr: ErrUnauthorized,
		},
		{
			name:    "wrong scheme",
			header:  "Basic management-token-canary",
			wantErr: ErrUnauthorized,
		},
		{
			name:    "extra whitespace",
			header:  "Bearer  management-token-canary",
			wantErr: ErrUnauthorized,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/api/v1/data-source-mappings", nil)
			if testCase.header != "" {
				request.Header.Set("Authorization", testCase.header)
			}
			err := authenticator.Authenticate(request)
			if testCase.wantResult {
				if err != nil {
					t.Fatalf("Authenticate() error = %v", err)
				}
				return
			}
			if !errors.Is(err, testCase.wantErr) {
				t.Fatalf("Authenticate() error = %v, want %v", err, testCase.wantErr)
			}
		})
	}
}

func TestManagementUnavailableAuthenticatorFailsClosed(t *testing.T) {
	t.Parallel()

	err := (ManagementUnavailableAuthenticator{}).Authenticate(
		httptest.NewRequest(http.MethodGet, "/api/v1/data-source-mappings", nil),
	)
	if !errors.Is(err, ErrManagementUnavailable) {
		t.Fatalf("Authenticate() error = %v, want ErrManagementUnavailable", err)
	}
}
