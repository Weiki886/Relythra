// Package health performs bounded, read-only checks against configured data
// sources without returning credential material or unbounded remote content.
package health

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Weiki886/relythra/internal/datasource"
	"github.com/Weiki886/relythra/internal/security/outbound"
	"github.com/Weiki886/relythra/internal/security/secrets"
)

const (
	// DefaultTimeout bounds a complete connection test, including body reads.
	DefaultTimeout = 5 * time.Second
	// MaxResponseBytes bounds remote responses before parsing or reporting.
	MaxResponseBytes = 64 * 1024
)

type Connectivity string

const (
	ConnectivityUnknown     Connectivity = "UNKNOWN"
	ConnectivityConnected   Connectivity = "CONNECTED"
	ConnectivityUnreachable Connectivity = "UNREACHABLE"
)

type Permission string

const (
	PermissionUnknown Permission = "UNKNOWN"
	PermissionGranted Permission = "GRANTED"
	PermissionDenied  Permission = "DENIED"
)

type ErrorCategory string

const (
	ErrorInvalidMapping    ErrorCategory = "invalid_mapping"
	ErrorSecretUnavailable ErrorCategory = "secret_unavailable"
	ErrorPolicyRejected    ErrorCategory = "policy_rejected"
	ErrorTimeout           ErrorCategory = "timeout"
	ErrorTLS               ErrorCategory = "tls_error"
	ErrorNetwork           ErrorCategory = "network_error"
	ErrorRemoteUnavailable ErrorCategory = "remote_unavailable"
	ErrorInvalidResponse   ErrorCategory = "invalid_response"
	ErrorResponseTooLarge  ErrorCategory = "response_too_large"
)

// TestError exposes a stable category while keeping implementation details
// available only through errors.Unwrap.
type TestError struct {
	Category ErrorCategory
	Cause    error
}

func (err *TestError) Error() string {
	if err == nil {
		return "<nil>"
	}
	return "connection test failed: " + string(err.Category)
}

func (err *TestError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.Cause
}

type SecretResolver interface {
	Resolve(context.Context, secrets.Reference) (secrets.Secret, error)
}

// Target contains the non-secret mapping fields required for one connection
// test. Callers obtain the reference separately from persisted configuration.
type Target struct {
	SourceType      datasource.SourceType
	RuntimeType     datasource.RuntimeType
	PublicSettings  datasource.PublicSettings
	SecretReference *secrets.Reference
}

type Result struct {
	Connectivity Connectivity `json:"connectivity"`
	Permission   Permission   `json:"permission"`
	Version      string       `json:"version,omitempty"`
	Capabilities []string     `json:"capabilities"`
	CheckedAt    time.Time    `json:"checked_at"`
}

type optionConfig struct {
	client *http.Client
}

type Option func(*optionConfig) error

// WithHTTPClient injects a controlled client for tests or deployment-specific
// transport policy. Production callers should normally use the default client
// assembled from the outbound policy.
func WithHTTPClient(client *http.Client) Option {
	return func(config *optionConfig) error {
		if client == nil {
			return errors.New("HTTP client is required")
		}
		config.client = client
		return nil
	}
}

type Tester struct {
	resolver SecretResolver
	client   *http.Client
	now      func() time.Time
}

func NewTester(
	policy *outbound.Policy,
	resolver SecretResolver,
	options ...Option,
) (*Tester, error) {
	if policy == nil {
		return nil, errors.New("outbound policy is required")
	}
	if resolver == nil {
		return nil, errors.New("secret resolver is required")
	}
	config := optionConfig{}
	for _, option := range options {
		if option == nil {
			return nil, errors.New("health tester option is nil")
		}
		if err := option(&config); err != nil {
			return nil, fmt.Errorf("configure health tester: %w", err)
		}
	}
	if config.client == nil {
		client, err := outbound.NewHTTPClient(policy, DefaultTimeout)
		if err != nil {
			return nil, fmt.Errorf("create outbound client: %w", err)
		}
		config.client = client
	}
	return &Tester{
		resolver: resolver,
		client:   config.client,
		now:      time.Now,
	}, nil
}

func (tester *Tester) Test(ctx context.Context, target Target) (Result, error) {
	result := Result{
		Connectivity: ConnectivityUnknown,
		Permission:   PermissionUnknown,
		CheckedAt:    time.Now().UTC(),
	}
	if tester == nil || tester.client == nil || tester.resolver == nil {
		return result, testError(ErrorInvalidMapping, errors.New("tester is not configured"))
	}
	if tester.now != nil {
		result.CheckedAt = tester.now().UTC()
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if target.SourceType == datasource.SourceRuntime &&
		target.RuntimeType == datasource.RuntimeKubernetes {
		result.Capabilities = []string{"runtime.kubernetes.unsupported"}
		return result, nil
	}

	capabilities, templateID := sourceCapabilities(target.SourceType)
	result.Capabilities = append([]string(nil), capabilities...)
	if issues := datasource.ValidateMapping(
		target.SourceType,
		target.RuntimeType,
		target.PublicSettings,
		[]string{templateID},
		target.SecretReference,
	); len(issues) != 0 {
		return result, testError(
			ErrorInvalidMapping,
			fmt.Errorf("mapping has %d validation issue(s)", len(issues)),
		)
	}

	request, err := tester.newRequest(ctx, target)
	if err != nil {
		return result, testError(ErrorInvalidMapping, err)
	}
	if target.SecretReference != nil {
		secret, err := tester.resolver.Resolve(ctx, *target.SecretReference)
		if err != nil {
			return result, testError(ErrorSecretUnavailable, err)
		}
		request.Header.Set("Authorization", "Bearer "+secret.Reveal())
	}

	response, err := tester.client.Do(request)
	if err != nil {
		result.Connectivity = ConnectivityUnreachable
		return result, classifyTransportError(err)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(response.Body, MaxResponseBytes+1))
	if err != nil {
		result.Connectivity = ConnectivityUnreachable
		return result, classifyTransportError(err)
	}
	if len(body) > MaxResponseBytes {
		result.Connectivity = ConnectivityConnected
		return result, testError(
			ErrorResponseTooLarge,
			fmt.Errorf("response exceeds %d bytes", MaxResponseBytes),
		)
	}

	switch {
	case response.StatusCode == http.StatusUnauthorized ||
		response.StatusCode == http.StatusForbidden:
		result.Connectivity = ConnectivityConnected
		result.Permission = PermissionDenied
		return result, nil
	case response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices:
		result.Connectivity = ConnectivityUnreachable
		return result, testError(
			ErrorRemoteUnavailable,
			fmt.Errorf("remote returned HTTP status %d", response.StatusCode),
		)
	}

	version, err := parseVersion(target.SourceType, body)
	if err != nil {
		result.Connectivity = ConnectivityConnected
		return result, testError(ErrorInvalidResponse, err)
	}
	result.Connectivity = ConnectivityConnected
	result.Permission = PermissionGranted
	result.Version = version
	return result, nil
}

func (tester *Tester) newRequest(ctx context.Context, target Target) (*http.Request, error) {
	endpoint, err := url.Parse(strings.TrimSpace(target.PublicSettings.Endpoint))
	if err != nil {
		return nil, errors.New("endpoint cannot be parsed")
	}
	if endpoint.Scheme != "http" && endpoint.Scheme != "https" ||
		endpoint.Hostname() == "" ||
		endpoint.User != nil ||
		endpoint.RawQuery != "" ||
		endpoint.Fragment != "" {
		return nil, errors.New("endpoint must be an HTTP or HTTPS base URL")
	}

	path, err := fixedPath(target)
	if err != nil {
		return nil, err
	}
	endpoint.Path = path
	endpoint.RawPath = ""
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, errors.New("fixed request cannot be constructed")
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "Relythra/1.0")
	if target.SourceType == datasource.SourceGitHub {
		request.Header.Set("Accept", "application/vnd.github+json")
		request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	}
	return request, nil
}

func fixedPath(target Target) (string, error) {
	switch target.SourceType {
	case datasource.SourceGitHub:
		owner, repository, ok := strings.Cut(target.PublicSettings.Repository, "/")
		if !ok || owner == "" || repository == "" || strings.Contains(repository, "/") {
			return "", errors.New("GitHub repository is invalid")
		}
		return "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repository), nil
	case datasource.SourceGrafana:
		return "/api/health", nil
	case datasource.SourcePrometheus:
		return "/api/v1/status/buildinfo", nil
	case datasource.SourceLoki:
		return "/loki/api/v1/status/buildinfo", nil
	case datasource.SourceRuntime:
		if target.RuntimeType != datasource.RuntimeDockerCompose {
			return "", errors.New("runtime type is unsupported")
		}
		return "/-/ready", nil
	default:
		return "", errors.New("source type is unsupported")
	}
}

func sourceCapabilities(source datasource.SourceType) ([]string, string) {
	switch source {
	case datasource.SourceGitHub:
		return []string{"github.repository.read"}, "github.commits.v1"
	case datasource.SourceGrafana:
		return []string{"grafana.health.read"}, "grafana.health.v1"
	case datasource.SourcePrometheus:
		return []string{"prometheus.query.read"}, "prometheus.service.latency.v1"
	case datasource.SourceLoki:
		return []string{"loki.logs.read"}, "loki.service.errors.v1"
	case datasource.SourceRuntime:
		return []string{"runtime.containers.read", "runtime.host.read"}, "runtime.containers.v1"
	default:
		return nil, ""
	}
}

func parseVersion(source datasource.SourceType, body []byte) (string, error) {
	switch source {
	case datasource.SourceGitHub, datasource.SourceRuntime:
		var document map[string]any
		if len(strings.TrimSpace(string(body))) == 0 {
			return "", nil
		}
		if err := json.Unmarshal(body, &document); err != nil {
			return "", errors.New("response is not a valid JSON object")
		}
		if document == nil {
			return "", errors.New("response is not a valid JSON object")
		}
		return "", nil
	case datasource.SourceGrafana:
		var document struct {
			Version string `json:"version"`
		}
		if err := json.Unmarshal(body, &document); err != nil {
			return "", errors.New("response is not valid Grafana health JSON")
		}
		return boundedVersion(document.Version)
	case datasource.SourcePrometheus, datasource.SourceLoki:
		var document struct {
			Data struct {
				Version string `json:"version"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &document); err != nil {
			return "", errors.New("response is not valid build info JSON")
		}
		return boundedVersion(document.Data.Version)
	default:
		return "", errors.New("source type is unsupported")
	}
}

func boundedVersion(raw string) (string, error) {
	version := strings.TrimSpace(raw)
	if len(version) > 128 || strings.ContainsAny(version, "\x00\r\n") {
		return "", errors.New("remote version is invalid")
	}
	return version, nil
}

func classifyTransportError(err error) *TestError {
	if errors.Is(err, context.DeadlineExceeded) || isTimeout(err) {
		return testError(ErrorTimeout, err)
	}
	for _, sentinel := range []error{
		outbound.ErrHostNotAllowed,
		outbound.ErrPortNotAllowed,
		outbound.ErrSchemeNotAllowed,
		outbound.ErrUnsafeAddress,
		outbound.ErrRedirectBlocked,
		outbound.ErrResolutionFailed,
		outbound.ErrInvalidTarget,
		outbound.ErrInvalidPolicy,
	} {
		if errors.Is(err, sentinel) {
			return testError(ErrorPolicyRejected, err)
		}
	}
	var certificateError *tls.CertificateVerificationError
	var unknownAuthority x509.UnknownAuthorityError
	var hostnameError x509.HostnameError
	var recordHeaderError tls.RecordHeaderError
	if errors.As(err, &certificateError) ||
		errors.As(err, &unknownAuthority) ||
		errors.As(err, &hostnameError) ||
		errors.As(err, &recordHeaderError) {
		return testError(ErrorTLS, err)
	}
	return testError(ErrorNetwork, err)
}

func isTimeout(err error) bool {
	var netError net.Error
	return errors.As(err, &netError) && netError.Timeout()
}

func testError(category ErrorCategory, cause error) *TestError {
	return &TestError{Category: category, Cause: cause}
}
