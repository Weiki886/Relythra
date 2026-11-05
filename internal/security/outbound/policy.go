// Package outbound enforces explicit host, port, protocol, and address
// policies for external data source connections.
package outbound

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var (
	ErrHostNotAllowed   = errors.New("outbound host is not allowed")
	ErrPortNotAllowed   = errors.New("outbound port is not allowed")
	ErrSchemeNotAllowed = errors.New("outbound scheme is not allowed")
	ErrUnsafeAddress    = errors.New("outbound address is unsafe")
	ErrRedirectBlocked  = errors.New("outbound redirect is blocked")
	ErrResolutionFailed = errors.New("outbound DNS resolution failed")
	ErrInvalidTarget    = errors.New("outbound target is invalid")
	ErrInvalidPolicy    = errors.New("outbound policy is invalid")
)

type PolicyConfig struct {
	AllowedHosts []string
	AllowedPorts []int
	AllowHTTP    bool
	AllowPrivate bool
}

type Target struct {
	URL       *url.URL
	Addresses []net.IPAddr
	Port      string
}

type resolveFunc func(context.Context, string) ([]net.IPAddr, error)

type Policy struct {
	allowedHosts map[string]struct{}
	allowedPorts map[int]struct{}
	allowHTTP    bool
	allowPrivate bool
	resolve      resolveFunc
}

func NewPolicy(config PolicyConfig) (*Policy, error) {
	return newPolicyWithResolver(config, net.DefaultResolver.LookupIPAddr)
}

func newPolicyWithResolver(config PolicyConfig, resolve resolveFunc) (*Policy, error) {
	if resolve == nil {
		return nil, fmt.Errorf("%w: resolver is required", ErrInvalidPolicy)
	}
	policy := &Policy{
		allowedHosts: make(map[string]struct{}, len(config.AllowedHosts)),
		allowedPorts: make(map[int]struct{}, len(config.AllowedPorts)),
		allowHTTP:    config.AllowHTTP,
		allowPrivate: config.AllowPrivate,
		resolve:      resolve,
	}
	for _, rawHost := range config.AllowedHosts {
		host, err := normalizeHost(rawHost)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid allowed host", ErrInvalidPolicy)
		}
		policy.allowedHosts[host] = struct{}{}
	}
	if len(policy.allowedHosts) == 0 {
		return nil, fmt.Errorf("%w: at least one allowed host is required", ErrInvalidPolicy)
	}
	for _, port := range config.AllowedPorts {
		if port < 1 || port > 65535 {
			return nil, fmt.Errorf("%w: allowed port is outside 1-65535", ErrInvalidPolicy)
		}
		policy.allowedPorts[port] = struct{}{}
	}
	if len(policy.allowedPorts) == 0 {
		return nil, fmt.Errorf("%w: at least one allowed port is required", ErrInvalidPolicy)
	}
	return policy, nil
}

func (policy *Policy) Validate(ctx context.Context, rawURL string) (Target, error) {
	if policy == nil {
		return Target{}, fmt.Errorf("%w: policy is nil", ErrInvalidPolicy)
	}
	return policy.validate(ctx, rawURL, true)
}

func (policy *Policy) validate(
	ctx context.Context,
	rawURL string,
	requireRootPath bool,
) (Target, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed == nil {
		return Target{}, fmt.Errorf("%w: URL cannot be parsed", ErrInvalidTarget)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return Target{}, fmt.Errorf("%w: only HTTP and HTTPS are supported", ErrSchemeNotAllowed)
	}
	if parsed.Scheme == "http" && !policy.allowHTTP {
		return Target{}, fmt.Errorf("%w: plain HTTP is disabled", ErrSchemeNotAllowed)
	}
	if parsed.User != nil || parsed.Fragment != "" {
		return Target{}, fmt.Errorf("%w: userinfo and fragments are prohibited", ErrInvalidTarget)
	}
	if requireRootPath && (parsed.RawQuery != "" ||
		(parsed.EscapedPath() != "" && parsed.EscapedPath() != "/")) {
		return Target{}, fmt.Errorf("%w: endpoint must be a base URL", ErrInvalidTarget)
	}

	host, err := normalizeHost(parsed.Hostname())
	if err != nil {
		return Target{}, fmt.Errorf("%w: host is invalid", ErrInvalidTarget)
	}
	if strings.HasSuffix(parsed.Host, ":") {
		return Target{}, fmt.Errorf("%w: port is invalid", ErrInvalidTarget)
	}
	if _, ok := policy.allowedHosts[host]; !ok {
		return Target{}, fmt.Errorf("%w: %s", ErrHostNotAllowed, host)
	}

	port := parsed.Port()
	if port == "" {
		if parsed.Scheme == "http" {
			port = "80"
		} else {
			port = "443"
		}
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return Target{}, fmt.Errorf("%w: port is invalid", ErrInvalidTarget)
	}
	if _, ok := policy.allowedPorts[portNumber]; !ok {
		return Target{}, fmt.Errorf("%w: %d", ErrPortNotAllowed, portNumber)
	}

	addresses, err := policy.resolve(ctx, host)
	if err != nil || len(addresses) == 0 {
		return Target{}, fmt.Errorf("%w: no addresses resolved", ErrResolutionFailed)
	}
	resolved := make([]net.IPAddr, 0, len(addresses))
	for _, address := range addresses {
		if address.Zone != "" {
			return Target{}, fmt.Errorf("%w: scoped DNS addresses are prohibited", ErrUnsafeAddress)
		}
		class := classifyAddress(address.IP)
		if class == addressUnsafe || class == addressPrivate && !policy.allowPrivate {
			return Target{}, fmt.Errorf("%w: resolved address is prohibited", ErrUnsafeAddress)
		}
		resolved = append(resolved, net.IPAddr{
			IP:   append(net.IP(nil), address.IP...),
			Zone: address.Zone,
		})
	}

	base := *parsed
	base.Path = ""
	base.RawPath = ""
	base.RawQuery = ""
	base.Fragment = ""
	return Target{URL: &base, Addresses: resolved, Port: port}, nil
}

type addressClass uint8

const (
	addressPublic addressClass = iota
	addressPrivate
	addressUnsafe
)

var prohibitedMetadataAddresses = map[string]struct{}{
	"100.100.100.200": {},
	"168.63.129.16":   {},
	"169.254.169.254": {},
	"fd00:ec2::254":   {},
	"fe80::a9fe:a9fe": {},
}

func classifyAddress(ip net.IP) addressClass {
	if ip == nil {
		return addressUnsafe
	}
	canonical := ip.String()
	if _, prohibited := prohibitedMetadataAddresses[canonical]; prohibited {
		return addressUnsafe
	}
	if ip.IsUnspecified() ||
		ip.IsLoopback() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() ||
		ip.IsMulticast() ||
		ip.Equal(net.IPv4bcast) {
		return addressUnsafe
	}
	if ip.IsPrivate() ||
		ipInPrefix(ip, "100.64.0.0/10") ||
		ipInPrefix(ip, "198.18.0.0/15") {
		return addressPrivate
	}
	return addressPublic
}

func ipInPrefix(ip net.IP, rawPrefix string) bool {
	_, network, err := net.ParseCIDR(rawPrefix)
	return err == nil && network.Contains(ip)
}

func normalizeHost(raw string) (string, error) {
	host := strings.ToLower(strings.TrimSpace(raw))
	host = strings.TrimSuffix(host, ".")
	if host == "" || len(host) > 253 {
		return "", errors.New("host is empty or too long")
	}
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	}
	if ip := net.ParseIP(host); ip != nil {
		if strings.Contains(host, "%") {
			return "", errors.New("scoped IP address is not allowed")
		}
		return ip.String(), nil
	}
	if strings.ContainsAny(host, "/\\@?#: \t\r\n") {
		return "", errors.New("host contains prohibited characters")
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) < 1 || len(label) > 63 ||
			label[0] == '-' || label[len(label)-1] == '-' {
			return "", errors.New("host label is invalid")
		}
		for _, character := range label {
			if character >= 'a' && character <= 'z' ||
				character >= '0' && character <= '9' ||
				character == '-' {
				continue
			}
			return "", errors.New("host label contains an invalid character")
		}
	}
	return host, nil
}

type dialContextFunc func(context.Context, string, []net.IPAddr, string) (net.Conn, error)

type targetContextKey struct{}

type policyTransport struct {
	policy      *Policy
	transport   *http.Transport
	dialContext dialContextFunc
}

func NewHTTPClient(policy *Policy, timeout time.Duration) (*http.Client, error) {
	if policy == nil || timeout <= 0 {
		return nil, fmt.Errorf("%w: policy and positive timeout are required", ErrInvalidPolicy)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.ForceAttemptHTTP2 = true
	policyTransport := &policyTransport{
		policy:    policy,
		transport: transport,
	}
	policyTransport.dialContext = policyTransport.dialValidatedAddress
	transport.DialContext = policyTransport.dialFromRequestContext
	return &http.Client{
		Transport: policyTransport,
		Timeout:   timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return ErrRedirectBlocked
		},
	}, nil
}

func (transport *policyTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request == nil || request.URL == nil {
		return nil, fmt.Errorf("%w: request URL is required", ErrInvalidTarget)
	}
	target, err := transport.policy.validate(request.Context(), request.URL.String(), false)
	if err != nil {
		return nil, err
	}
	ctx := context.WithValue(request.Context(), targetContextKey{}, target)
	return transport.transport.RoundTrip(request.Clone(ctx))
}

func (transport *policyTransport) dialFromRequestContext(
	ctx context.Context,
	network string,
	_ string,
) (net.Conn, error) {
	target, ok := ctx.Value(targetContextKey{}).(Target)
	if !ok {
		return nil, fmt.Errorf("%w: validated target is missing", ErrInvalidTarget)
	}
	return transport.dialContext(ctx, network, target.Addresses, target.Port)
}

func (transport *policyTransport) dialValidatedAddress(
	ctx context.Context,
	network string,
	addresses []net.IPAddr,
	port string,
) (net.Conn, error) {
	dialer := &net.Dialer{
		Timeout:   5 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	var lastError error
	for _, address := range addresses {
		host := address.IP.String()
		if address.Zone != "" {
			host += "%" + address.Zone
		}
		connection, err := dialer.DialContext(ctx, network, net.JoinHostPort(host, port))
		if err == nil {
			return connection, nil
		}
		lastError = err
	}
	if lastError == nil {
		lastError = ErrResolutionFailed
	}
	return nil, fmt.Errorf("%w: validated address could not be reached", lastError)
}
