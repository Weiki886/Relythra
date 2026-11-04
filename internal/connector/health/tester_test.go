package health

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Weiki886/relythra/internal/datasource"
	"github.com/Weiki886/relythra/internal/security/outbound"
	"github.com/Weiki886/relythra/internal/security/secrets"
)

func TestTesterReturnsVersionAndPermissionWithoutSecret(t *testing.T) {
	const (
		canary    = "health-canary-84a2"
		fixedTime = "2026-11-04T08:00:00Z"
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", request.Method)
		}
		if request.URL.Path != "/api/v1/status/buildinfo" {
			t.Errorf("path = %q, want Prometheus build info", request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer "+canary {
			t.Errorf("authorization header was not injected")
		}
		if request.Header.Get("Accept") != "application/json" {
			t.Errorf("accept = %q", request.Header.Get("Accept"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"success","data":{"version":"3.5.0"}}`)
	}))
	defer server.Close()

	reference := mustReference(t, "env:PROMETHEUS_TOKEN")
	tester := newTestTester(t, server.Client(), resolverFunc(func(_ context.Context, resolved secrets.Reference) (secrets.Secret, error) {
		if resolved.String() != reference.String() {
			t.Errorf("resolved reference = %q, want %q", resolved.String(), reference.String())
		}
		return mustSecret(t, canary), nil
	}))
	checkedAt := mustTime(t, fixedTime)
	tester.now = func() time.Time { return checkedAt }

	result, err := tester.Test(context.Background(), Target{
		SourceType:      datasource.SourcePrometheus,
		PublicSettings:  datasource.PublicSettings{Endpoint: server.URL},
		SecretReference: &reference,
	})
	if err != nil {
		t.Fatalf("test connection: %v", err)
	}
	if result.Connectivity != ConnectivityConnected {
		t.Fatalf("connectivity = %q, want %q", result.Connectivity, ConnectivityConnected)
	}
	if result.Permission != PermissionGranted {
		t.Fatalf("permission = %q, want %q", result.Permission, PermissionGranted)
	}
	if result.Version != "3.5.0" {
		t.Fatalf("version = %q, want 3.5.0", result.Version)
	}
	if len(result.Capabilities) != 1 || result.Capabilities[0] != "prometheus.query.read" {
		t.Fatalf("capabilities = %#v", result.Capabilities)
	}
	if !result.CheckedAt.Equal(checkedAt) {
		t.Fatalf("checked at = %s, want %s", result.CheckedAt, checkedAt)
	}
	if strings.Contains(fmt.Sprintf("%#v", result), canary) {
		t.Fatal("result exposed the secret")
	}
}

func TestTesterUsesSourceSpecificFixedPaths(t *testing.T) {
	tests := []struct {
		name       string
		target     Target
		response   string
		wantPath   string
		capability string
	}{
		{
			name: "GitHub",
			target: Target{
				SourceType: datasource.SourceGitHub,
				PublicSettings: datasource.PublicSettings{
					Endpoint:   "https://github.example",
					Repository: "acme/relythra",
				},
			},
			response:   `{"default_branch":"main"}`,
			wantPath:   "/repos/acme/relythra",
			capability: "github.repository.read",
		},
		{
			name: "Grafana",
			target: Target{
				SourceType:     datasource.SourceGrafana,
				PublicSettings: datasource.PublicSettings{Endpoint: "https://grafana.example"},
			},
			response:   `{"version":"11.3.0"}`,
			wantPath:   "/api/health",
			capability: "grafana.health.read",
		},
		{
			name: "Prometheus",
			target: Target{
				SourceType:     datasource.SourcePrometheus,
				PublicSettings: datasource.PublicSettings{Endpoint: "https://prometheus.example"},
			},
			response:   `{"status":"success","data":{"version":"3.5.0"}}`,
			wantPath:   "/api/v1/status/buildinfo",
			capability: "prometheus.query.read",
		},
		{
			name: "Loki",
			target: Target{
				SourceType:     datasource.SourceLoki,
				PublicSettings: datasource.PublicSettings{Endpoint: "https://loki.example"},
			},
			response:   `{"status":"success","data":{"version":"3.6.0"}}`,
			wantPath:   "/loki/api/v1/status/buildinfo",
			capability: "loki.logs.read",
		},
		{
			name: "Runtime",
			target: Target{
				SourceType:  datasource.SourceRuntime,
				RuntimeType: datasource.RuntimeDockerCompose,
				PublicSettings: datasource.PublicSettings{
					Endpoint: "https://runtime.example",
					RuntimeLabels: map[string]string{
						"project":      "relythra",
						"service":      "checkout",
						"env":          "prod",
						"host_id":      "host-1",
						"container_id": "container-1",
					},
				},
			},
			response:   `{"ready":true}`,
			wantPath:   "/-/ready",
			capability: "runtime.containers.read",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				if request.URL.Path != testCase.wantPath {
					t.Errorf("path = %q, want %q", request.URL.Path, testCase.wantPath)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, testCase.response)
			}))
			defer server.Close()

			target := testCase.target
			target.PublicSettings.Endpoint = server.URL
			result, err := newTestTester(t, server.Client(), unusedResolver).Test(context.Background(), target)
			if err != nil {
				t.Fatalf("test connection: %v", err)
			}
			if !contains(result.Capabilities, testCase.capability) {
				t.Fatalf("capabilities = %#v, want %q", result.Capabilities, testCase.capability)
			}
		})
	}
}

func TestTesterReportsDeniedPermissions(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
			}))
			defer server.Close()

			result, err := newTestTester(t, server.Client(), unusedResolver).Test(
				context.Background(),
				prometheusTarget(server.URL),
			)
			if err != nil {
				t.Fatalf("test connection: %v", err)
			}
			if result.Connectivity != ConnectivityConnected {
				t.Fatalf("connectivity = %q", result.Connectivity)
			}
			if result.Permission != PermissionDenied {
				t.Fatalf("permission = %q, want %q", result.Permission, PermissionDenied)
			}
			if !contains(result.Capabilities, "prometheus.query.read") {
				t.Fatalf("capabilities = %#v", result.Capabilities)
			}
		})
	}
}

func TestTesterReportsKubernetesUnsupported(t *testing.T) {
	result, err := newTestTester(t, http.DefaultClient, unusedResolver).Test(
		context.Background(),
		Target{
			SourceType:     datasource.SourceRuntime,
			RuntimeType:    datasource.RuntimeKubernetes,
			PublicSettings: datasource.PublicSettings{Endpoint: "https://runtime.example"},
		},
	)
	if err != nil {
		t.Fatalf("test connection: %v", err)
	}
	if result.Permission != PermissionUnknown {
		t.Fatalf("permission = %q, want unknown", result.Permission)
	}
	if len(result.Capabilities) != 1 || result.Capabilities[0] != "runtime.kubernetes.unsupported" {
		t.Fatalf("capabilities = %#v", result.Capabilities)
	}
}

func TestTesterReturnsUnknownPermissionForMalformedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"data":`)
	}))
	defer server.Close()

	result, err := newTestTester(t, server.Client(), unusedResolver).Test(
		context.Background(),
		prometheusTarget(server.URL),
	)
	requireCategory(t, err, ErrorInvalidResponse)
	if result.Connectivity != ConnectivityConnected {
		t.Fatalf("connectivity = %q, want connected", result.Connectivity)
	}
	if result.Permission != PermissionUnknown {
		t.Fatalf("permission = %q, want unknown", result.Permission)
	}
	if !contains(result.Capabilities, "prometheus.query.read") {
		t.Fatalf("capabilities = %#v", result.Capabilities)
	}
}

func TestTesterRejectsNullGitHubResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `null`)
	}))
	defer server.Close()

	_, err := newTestTester(t, server.Client(), unusedResolver).Test(
		context.Background(),
		Target{
			SourceType: datasource.SourceGitHub,
			PublicSettings: datasource.PublicSettings{
				Endpoint:   server.URL,
				Repository: "acme/relythra",
			},
		},
	)
	requireCategory(t, err, ErrorInvalidResponse)
}

func TestTesterRejectsOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat("x", MaxResponseBytes+1))
	}))
	defer server.Close()

	_, err := newTestTester(t, server.Client(), unusedResolver).Test(
		context.Background(),
		prometheusTarget(server.URL),
	)
	requireCategory(t, err, ErrorResponseTooLarge)
}

func TestTesterClassifiesRemoteUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	result, err := newTestTester(t, server.Client(), unusedResolver).Test(
		context.Background(),
		prometheusTarget(server.URL),
	)
	requireCategory(t, err, ErrorRemoteUnavailable)
	if result.Connectivity != ConnectivityUnreachable {
		t.Fatalf("connectivity = %q, want unreachable", result.Connectivity)
	}
}

func TestTesterClassifiesTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		<-request.Context().Done()
	}))
	defer server.Close()

	client := server.Client()
	client.Timeout = 50 * time.Millisecond
	_, err := newTestTester(t, client, unusedResolver).Test(
		context.Background(),
		prometheusTarget(server.URL),
	)
	requireCategory(t, err, ErrorTimeout)
}

func TestTesterClassifiesBodyReadTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-request.Context().Done()
	}))
	defer server.Close()

	client := server.Client()
	client.Timeout = 50 * time.Millisecond
	_, err := newTestTester(t, client, unusedResolver).Test(
		context.Background(),
		prometheusTarget(server.URL),
	)
	requireCategory(t, err, ErrorTimeout)
}

func TestTesterClassifiesTLSError(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	_, err := newTestTester(t, &http.Client{}, unusedResolver).Test(
		context.Background(),
		prometheusTarget(server.URL),
	)
	requireCategory(t, err, ErrorTLS)
}

func TestTesterDoesNotCallRemoteWhenSecretIsUnavailable(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	reference := mustReference(t, "env:MISSING_PROMETHEUS_TOKEN")
	tester := newTestTester(t, server.Client(), resolverFunc(func(context.Context, secrets.Reference) (secrets.Secret, error) {
		return secrets.Secret{}, secrets.ErrUnavailable
	}))
	target := prometheusTarget(server.URL)
	target.SecretReference = &reference
	_, err := tester.Test(context.Background(), target)
	requireCategory(t, err, ErrorSecretUnavailable)
	if calls != 0 {
		t.Fatalf("remote calls = %d, want 0", calls)
	}
}

func TestNewTesterUsesFiveSecondDefaultTimeout(t *testing.T) {
	policy := testPolicy(t)
	tester, err := NewTester(policy, unusedResolver)
	if err != nil {
		t.Fatalf("new tester: %v", err)
	}
	if tester.client.Timeout != 5*time.Second {
		t.Fatalf("timeout = %s, want 5s", tester.client.Timeout)
	}
}

func TestNewTesterRejectsInvalidDependencies(t *testing.T) {
	if _, err := NewTester(nil, unusedResolver); err == nil {
		t.Fatal("nil policy was accepted")
	}
	if _, err := NewTester(testPolicy(t), nil); err == nil {
		t.Fatal("nil secret resolver was accepted")
	}
	if _, err := NewTester(testPolicy(t), unusedResolver, WithHTTPClient(nil)); err == nil {
		t.Fatal("nil HTTP client was accepted")
	}
}

func newTestTester(
	t *testing.T,
	client *http.Client,
	resolver SecretResolver,
) *Tester {
	t.Helper()
	tester, err := NewTester(testPolicy(t), resolver, WithHTTPClient(client))
	if err != nil {
		t.Fatalf("new tester: %v", err)
	}
	return tester
}

func testPolicy(t *testing.T) *outbound.Policy {
	t.Helper()
	policy, err := outbound.NewPolicy(outbound.PolicyConfig{
		AllowedHosts: []string{"metrics.example.com"},
		AllowedPorts: []int{80, 443},
		AllowHTTP:    true,
	})
	if err != nil {
		t.Fatalf("new policy: %v", err)
	}
	return policy
}

func prometheusTarget(endpoint string) Target {
	return Target{
		SourceType:     datasource.SourcePrometheus,
		PublicSettings: datasource.PublicSettings{Endpoint: endpoint},
	}
}

type resolverFunc func(context.Context, secrets.Reference) (secrets.Secret, error)

func (resolve resolverFunc) Resolve(
	ctx context.Context,
	reference secrets.Reference,
) (secrets.Secret, error) {
	return resolve(ctx, reference)
}

var unusedResolver resolverFunc = func(context.Context, secrets.Reference) (secrets.Secret, error) {
	return secrets.Secret{}, errors.New("secret resolver must not be called")
}

func mustReference(t *testing.T, raw string) secrets.Reference {
	t.Helper()
	reference, err := secrets.ParseReference(raw)
	if err != nil {
		t.Fatalf("parse reference: %v", err)
	}
	return reference
}

func mustSecret(t *testing.T, value string) secrets.Secret {
	t.Helper()
	secret, err := secrets.New(value)
	if err != nil {
		t.Fatalf("new secret: %v", err)
	}
	return secret
}

func mustTime(t *testing.T, raw string) time.Time {
	t.Helper()
	value, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		t.Fatalf("parse time: %v", err)
	}
	return value
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func requireCategory(t *testing.T, err error, want ErrorCategory) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want category %q", want)
	}
	var testError *TestError
	if !errors.As(err, &testError) {
		t.Fatalf("error = %T %v, want *TestError", err, err)
	}
	if testError.Category != want {
		t.Fatalf("category = %q, want %q: %v", testError.Category, want, err)
	}
}
