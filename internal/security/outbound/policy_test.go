package outbound

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestPolicyRejectsUnauthorizedTargets(t *testing.T) {
	tests := []struct {
		name      string
		config    PolicyConfig
		rawURL    string
		addresses []net.IPAddr
		wantErr   error
	}{
		{
			name:    "scheme",
			config:  testPolicyConfig(),
			rawURL:  "file:///etc/passwd",
			wantErr: ErrSchemeNotAllowed,
		},
		{
			name:      "host",
			config:    testPolicyConfig(),
			rawURL:    "https://other.example.com",
			addresses: []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}},
			wantErr:   ErrHostNotAllowed,
		},
		{
			name:      "port",
			config:    testPolicyConfig(),
			rawURL:    "https://metrics.example.com:8443",
			addresses: []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}},
			wantErr:   ErrPortNotAllowed,
		},
		{
			name:      "http disabled",
			config:    testPolicyConfig(),
			rawURL:    "http://metrics.example.com",
			addresses: []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}},
			wantErr:   ErrSchemeNotAllowed,
		},
		{
			name: "loopback even with private access",
			config: PolicyConfig{
				AllowedHosts: []string{"localhost"},
				AllowedPorts: []int{80},
				AllowHTTP:    true,
				AllowPrivate: true,
			},
			rawURL:    "http://localhost",
			addresses: []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}},
			wantErr:   ErrUnsafeAddress,
		},
		{
			name: "link local metadata",
			config: PolicyConfig{
				AllowedHosts: []string{"metadata.example.com"},
				AllowedPorts: []int{443},
				AllowPrivate: true,
			},
			rawURL:    "https://metadata.example.com",
			addresses: []net.IPAddr{{IP: net.ParseIP("169.254.169.254")}},
			wantErr:   ErrUnsafeAddress,
		},
		{
			name: "public cloud metadata",
			config: PolicyConfig{
				AllowedHosts: []string{"metadata.example.com"},
				AllowedPorts: []int{443},
				AllowPrivate: true,
			},
			rawURL:    "https://metadata.example.com",
			addresses: []net.IPAddr{{IP: net.ParseIP("100.100.100.200")}},
			wantErr:   ErrUnsafeAddress,
		},
		{
			name: "unspecified",
			config: PolicyConfig{
				AllowedHosts: []string{"metrics.example.com"},
				AllowedPorts: []int{443},
				AllowPrivate: true,
			},
			rawURL:    "https://metrics.example.com",
			addresses: []net.IPAddr{{IP: net.IPv4zero}},
			wantErr:   ErrUnsafeAddress,
		},
		{
			name: "multicast",
			config: PolicyConfig{
				AllowedHosts: []string{"metrics.example.com"},
				AllowedPorts: []int{443},
				AllowPrivate: true,
			},
			rawURL:    "https://metrics.example.com",
			addresses: []net.IPAddr{{IP: net.ParseIP("224.0.0.1")}},
			wantErr:   ErrUnsafeAddress,
		},
		{
			name:      "private disabled",
			config:    testPolicyConfig(),
			rawURL:    "https://metrics.example.com",
			addresses: []net.IPAddr{{IP: net.ParseIP("10.0.0.12")}},
			wantErr:   ErrUnsafeAddress,
		},
		{
			name: "mixed safe and unsafe",
			config: PolicyConfig{
				AllowedHosts: []string{"metrics.example.com"},
				AllowedPorts: []int{443},
				AllowPrivate: true,
			},
			rawURL: "https://metrics.example.com",
			addresses: []net.IPAddr{
				{IP: net.ParseIP("93.184.216.34")},
				{IP: net.ParseIP("127.0.0.1")},
			},
			wantErr: ErrUnsafeAddress,
		},
		{
			name: "IPv4-mapped loopback",
			config: PolicyConfig{
				AllowedHosts: []string{"metrics.example.com"},
				AllowedPorts: []int{443},
				AllowPrivate: true,
			},
			rawURL:    "https://metrics.example.com",
			addresses: []net.IPAddr{{IP: net.ParseIP("::ffff:127.0.0.1")}},
			wantErr:   ErrUnsafeAddress,
		},
		{
			name: "IPv4-mapped link local",
			config: PolicyConfig{
				AllowedHosts: []string{"metrics.example.com"},
				AllowedPorts: []int{443},
				AllowPrivate: true,
			},
			rawURL:    "https://metrics.example.com",
			addresses: []net.IPAddr{{IP: net.ParseIP("::ffff:169.254.169.254")}},
			wantErr:   ErrUnsafeAddress,
		},
		{
			name: "scoped DNS address",
			config: PolicyConfig{
				AllowedHosts: []string{"metrics.example.com"},
				AllowedPorts: []int{443},
				AllowPrivate: true,
			},
			rawURL:    "https://metrics.example.com",
			addresses: []net.IPAddr{{IP: net.ParseIP("fe80::1"), Zone: "eth0"}},
			wantErr:   ErrUnsafeAddress,
		},
		{
			name:      "userinfo",
			config:    testPolicyConfig(),
			rawURL:    "https://admin:secret@metrics.example.com",
			addresses: []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}},
			wantErr:   ErrInvalidTarget,
		},
		{
			name:      "path",
			config:    testPolicyConfig(),
			rawURL:    "https://metrics.example.com/admin",
			addresses: []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}},
			wantErr:   ErrInvalidTarget,
		},
		{
			name:      "empty explicit port",
			config:    testPolicyConfig(),
			rawURL:    "https://metrics.example.com:",
			addresses: []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}},
			wantErr:   ErrInvalidTarget,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			policy, err := newPolicyWithResolver(
				testCase.config,
				func(context.Context, string) ([]net.IPAddr, error) {
					return testCase.addresses, nil
				},
			)
			if err != nil {
				t.Fatalf("new policy: %v", err)
			}
			if _, err := policy.Validate(context.Background(), testCase.rawURL); !errors.Is(err, testCase.wantErr) {
				t.Fatalf("err = %v, want %v", err, testCase.wantErr)
			}
		})
	}
}

func TestPolicyAllowsExplicitPublicAndPrivateTargets(t *testing.T) {
	tests := []struct {
		name      string
		config    PolicyConfig
		addresses []net.IPAddr
		wantPort  string
	}{
		{
			name:   "public default port",
			config: testPolicyConfig(),
			addresses: []net.IPAddr{
				{IP: net.ParseIP("93.184.216.34")},
				{IP: net.ParseIP("2606:2800:220:1:248:1893:25c8:1946")},
			},
			wantPort: "443",
		},
		{
			name: "private explicit opt in",
			config: PolicyConfig{
				AllowedHosts: []string{"metrics.internal"},
				AllowedPorts: []int{9090},
				AllowPrivate: true,
			},
			addresses: []net.IPAddr{{IP: net.ParseIP("10.0.0.12")}},
			wantPort:  "9090",
		},
		{
			name: "http explicit opt in",
			config: PolicyConfig{
				AllowedHosts: []string{"metrics.example.com"},
				AllowedPorts: []int{80},
				AllowHTTP:    true,
			},
			addresses: []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}},
			wantPort:  "80",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			policy, err := newPolicyWithResolver(
				testCase.config,
				func(context.Context, string) ([]net.IPAddr, error) {
					return testCase.addresses, nil
				},
			)
			if err != nil {
				t.Fatalf("new policy: %v", err)
			}
			target, err := policy.Validate(context.Background(), testCase.config.configURL(testCase.wantPort))
			if err != nil {
				t.Fatalf("validate: %v", err)
			}
			if target.Port != testCase.wantPort {
				t.Fatalf("port = %q, want %q", target.Port, testCase.wantPort)
			}
			if len(target.Addresses) != len(testCase.addresses) {
				t.Fatalf("addresses = %#v", target.Addresses)
			}
		})
	}
}

func TestPolicyWrapsResolutionFailure(t *testing.T) {
	policy, err := newPolicyWithResolver(
		testPolicyConfig(),
		func(context.Context, string) ([]net.IPAddr, error) {
			return nil, errors.New("resolver unavailable")
		},
	)
	if err != nil {
		t.Fatalf("new policy: %v", err)
	}
	_, err = policy.Validate(context.Background(), "https://metrics.example.com")
	if !errors.Is(err, ErrResolutionFailed) {
		t.Fatalf("err = %v, want ErrResolutionFailed", err)
	}
}

func TestPolicyRejectsEmptyResolutionResult(t *testing.T) {
	policy, err := newPolicyWithResolver(
		testPolicyConfig(),
		func(context.Context, string) ([]net.IPAddr, error) {
			return nil, nil
		},
	)
	if err != nil {
		t.Fatalf("new policy: %v", err)
	}
	_, err = policy.Validate(context.Background(), "https://metrics.example.com")
	if !errors.Is(err, ErrResolutionFailed) {
		t.Fatalf("err = %v, want ErrResolutionFailed", err)
	}
}

func TestNewPolicyRejectsInvalidConfiguration(t *testing.T) {
	tests := []PolicyConfig{
		{},
		{AllowedHosts: []string{""}, AllowedPorts: []int{443}},
		{AllowedHosts: []string{"https://metrics.example.com"}, AllowedPorts: []int{443}},
		{AllowedHosts: []string{"*.example.com"}, AllowedPorts: []int{443}},
		{AllowedHosts: []string{"metrics.example.com"}, AllowedPorts: []int{0}},
		{AllowedHosts: []string{"metrics.example.com"}, AllowedPorts: []int{65536}},
	}
	for index, config := range tests {
		t.Run(fmt.Sprintf("case-%d", index), func(t *testing.T) {
			if _, err := NewPolicy(config); err == nil {
				t.Fatalf("invalid config was accepted: %#v", config)
			}
		})
	}
}

func TestClientUsesValidatedAddressForDial(t *testing.T) {
	var dialedAddress string
	policy, err := newPolicyWithResolver(
		PolicyConfig{
			AllowedHosts: []string{"metrics.example.com"},
			AllowedPorts: []int{443},
		},
		func(context.Context, string) ([]net.IPAddr, error) {
			return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
		},
	)
	if err != nil {
		t.Fatalf("new policy: %v", err)
	}
	client, err := NewHTTPClient(policy, 2*time.Second)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	transport, ok := client.Transport.(*policyTransport)
	if !ok {
		t.Fatalf("transport = %T, want *policyTransport", client.Transport)
	}
	transport.dialContext = func(_ context.Context, _ string, addresses []net.IPAddr, port string) (net.Conn, error) {
		dialedAddress = net.JoinHostPort(addresses[0].IP.String(), port)
		return nil, errors.New("stop after address capture")
	}

	request, err := http.NewRequest(http.MethodGet, "https://metrics.example.com/health", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	_, err = client.Do(request)
	if err == nil || !strings.Contains(err.Error(), "stop after address capture") {
		t.Fatalf("client error = %v", err)
	}
	if dialedAddress != "93.184.216.34:443" {
		t.Fatalf("dial address = %q, want validated address", dialedAddress)
	}
}

func TestClientRevalidatesDNSForEachRequest(t *testing.T) {
	var calls int
	policy, err := newPolicyWithResolver(
		testPolicyConfig(),
		func(context.Context, string) ([]net.IPAddr, error) {
			calls++
			if calls == 1 {
				return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
			}
			return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
		},
	)
	if err != nil {
		t.Fatalf("new policy: %v", err)
	}
	client, err := NewHTTPClient(policy, 2*time.Second)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	transport := client.Transport.(*policyTransport)
	transport.dialContext = func(context.Context, string, []net.IPAddr, string) (net.Conn, error) {
		return nil, errors.New("stop after validation")
	}

	for attempt := 1; attempt <= 2; attempt++ {
		request, err := http.NewRequest(http.MethodGet, "https://metrics.example.com/health", nil)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		_, err = client.Do(request)
		if attempt == 1 && (err == nil || !strings.Contains(err.Error(), "stop after validation")) {
			t.Fatalf("first request error = %v", err)
		}
		if attempt == 2 && !errors.Is(err, ErrUnsafeAddress) {
			t.Fatalf("second request error = %v, want ErrUnsafeAddress", err)
		}
	}
	if calls != 2 {
		t.Fatalf("resolver calls = %d, want 2", calls)
	}
}

func TestClientDoesNotFollowRedirect(t *testing.T) {
	var destinationCalls int
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		destinationCalls++
		w.WriteHeader(http.StatusNoContent)
	}))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Redirect(w, &http.Request{}, destination.URL, http.StatusFound)
	}))
	defer source.Close()

	sourceURL, err := url.Parse(source.URL)
	if err != nil {
		t.Fatalf("parse source URL: %v", err)
	}
	port := sourceURL.Port()
	policy, err := newPolicyWithResolver(
		PolicyConfig{
			AllowedHosts: []string{"metrics.example.com"},
			AllowedPorts: []int{mustPort(t, port)},
			AllowHTTP:    true,
			AllowPrivate: true,
		},
		func(context.Context, string) ([]net.IPAddr, error) {
			return []net.IPAddr{{IP: net.ParseIP("10.0.0.12")}}, nil
		},
	)
	if err != nil {
		t.Fatalf("new policy: %v", err)
	}
	client, err := NewHTTPClient(policy, 2*time.Second)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	transport := client.Transport.(*policyTransport)
	transport.dialContext = func(ctx context.Context, network string, _ []net.IPAddr, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, source.Listener.Addr().String())
	}
	endpoint := "http://metrics.example.com:" + port + "/health"
	response, err := client.Get(endpoint)
	if response != nil {
		_ = response.Body.Close()
	}
	if !errors.Is(err, ErrRedirectBlocked) {
		t.Fatalf("err = %v, want ErrRedirectBlocked", err)
	}
	if destinationCalls != 0 {
		t.Fatalf("redirect destination calls = %d, want 0", destinationCalls)
	}
}

func TestClientReturnsBoundedResponseBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	}))
	defer server.Close()
	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	policy, err := newPolicyWithResolver(
		PolicyConfig{
			AllowedHosts: []string{"metrics.example.com"},
			AllowedPorts: []int{mustPort(t, serverURL.Port())},
			AllowHTTP:    true,
			AllowPrivate: true,
		},
		func(context.Context, string) ([]net.IPAddr, error) {
			return []net.IPAddr{{IP: net.ParseIP("10.0.0.12")}}, nil
		},
	)
	if err != nil {
		t.Fatalf("new policy: %v", err)
	}
	client, err := NewHTTPClient(policy, 500*time.Millisecond)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	client.Transport.(*policyTransport).dialContext = func(
		ctx context.Context,
		network string,
		_ []net.IPAddr,
		_ string,
	) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	response, err := client.Get("http://metrics.example.com:" + serverURL.Port() + "/health")
	if err != nil {
		t.Fatalf("get health: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatalf("close body: %v", err)
	}
	if string(body) != "ok" {
		t.Fatalf("body = %q", body)
	}
}

func TestClientTimesOutHangingRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		<-request.Context().Done()
	}))
	defer server.Close()
	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	policy, err := newPolicyWithResolver(
		PolicyConfig{
			AllowedHosts: []string{"metrics.example.com"},
			AllowedPorts: []int{mustPort(t, serverURL.Port())},
			AllowHTTP:    true,
			AllowPrivate: true,
		},
		func(context.Context, string) ([]net.IPAddr, error) {
			return []net.IPAddr{{IP: net.ParseIP("10.0.0.12")}}, nil
		},
	)
	if err != nil {
		t.Fatalf("new policy: %v", err)
	}
	client, err := NewHTTPClient(policy, 100*time.Millisecond)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	client.Transport.(*policyTransport).dialContext = func(
		ctx context.Context,
		network string,
		_ []net.IPAddr,
		_ string,
	) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}

	_, err = client.Get("http://metrics.example.com:" + serverURL.Port() + "/health")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context deadline exceeded", err)
	}
}

func TestNewHTTPClientRejectsInvalidArguments(t *testing.T) {
	if _, err := NewHTTPClient(nil, time.Second); err == nil {
		t.Fatal("nil policy was accepted")
	}
	policy, err := NewPolicy(testPolicyConfig())
	if err != nil {
		t.Fatalf("new policy: %v", err)
	}
	for _, timeout := range []time.Duration{0, -time.Second} {
		if _, err := NewHTTPClient(policy, timeout); err == nil {
			t.Fatalf("timeout %s was accepted", timeout)
		}
	}
}

func testPolicyConfig() PolicyConfig {
	return PolicyConfig{
		AllowedHosts: []string{"metrics.example.com"},
		AllowedPorts: []int{443},
	}
}

func (config PolicyConfig) configURL(port string) string {
	scheme := "https"
	if config.AllowHTTP && len(config.AllowedPorts) == 1 && config.AllowedPorts[0] == 80 {
		scheme = "http"
	}
	host := config.AllowedHosts[0]
	if port == "80" && scheme == "http" || port == "443" && scheme == "https" {
		return scheme + "://" + host
	}
	return scheme + "://" + net.JoinHostPort(host, port)
}

func mustPort(t *testing.T, raw string) int {
	t.Helper()
	port, err := net.LookupPort("tcp", raw)
	if err != nil {
		t.Fatalf("parse port %q: %v", raw, err)
	}
	return port
}
