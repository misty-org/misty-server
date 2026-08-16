package mcp

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	defaultMaxRequestBytes  int64 = 256 << 10
	defaultMaxResponseBytes int64 = 2 << 20
	defaultMaxCatalogTools        = 128
	defaultMaxToolNameBytes       = 200
)

var (
	ErrEndpointRejected = errors.New("MCP endpoint rejected")
	ErrResponseTooLarge = errors.New("MCP response exceeded its configured limit")
	bearerTokenPattern  = regexp.MustCompile(`^[A-Za-z0-9\-._~+/]+=*$`)
	blockedNetworks     = mustBlockedNetworks()
)

// Limits bounds the remote MCP transport and the catalog material exposed to
// an Agent. Zero values select conservative defaults.
type Limits struct {
	ConnectTimeout      time.Duration
	TLSHandshakeTimeout time.Duration
	RequestTimeout      time.Duration
	MaxRequestBytes     int64
	MaxResponseBytes    int64
	MaxCatalogTools     int
	MaxToolNameBytes    int
}

func (limits Limits) normalized() Limits {
	if limits.ConnectTimeout <= 0 || limits.ConnectTimeout > 10*time.Second {
		limits.ConnectTimeout = 5 * time.Second
	}
	if limits.TLSHandshakeTimeout <= 0 || limits.TLSHandshakeTimeout > 10*time.Second {
		limits.TLSHandshakeTimeout = 5 * time.Second
	}
	if limits.RequestTimeout <= 0 || limits.RequestTimeout > 60*time.Second {
		limits.RequestTimeout = 30 * time.Second
	}
	if limits.MaxRequestBytes <= 0 || limits.MaxRequestBytes > 1<<20 {
		limits.MaxRequestBytes = defaultMaxRequestBytes
	}
	if limits.MaxResponseBytes <= 0 || limits.MaxResponseBytes > 8<<20 {
		limits.MaxResponseBytes = defaultMaxResponseBytes
	}
	if limits.MaxCatalogTools <= 0 || limits.MaxCatalogTools > 512 {
		limits.MaxCatalogTools = defaultMaxCatalogTools
	}
	if limits.MaxToolNameBytes <= 0 || limits.MaxToolNameBytes > 512 {
		limits.MaxToolNameBytes = defaultMaxToolNameBytes
	}
	return limits
}

type ipResolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}

type dialContextFunc func(context.Context, string, string) (net.Conn, error)

// NewHTTPClient creates a remote Streamable HTTP client bound to one exact MCP
// endpoint. It never follows redirects or uses environment proxies, and it
// resolves and validates the hostname again for every new connection.
func NewHTTPClient(endpoint, bearer string, limits Limits) (*http.Client, error) {
	return newHTTPClient(endpoint, bearer, limits, net.DefaultResolver, nil)
}

// TestingNewHTTPClient exposes resolver and dial injection to the external
// security contract suite without weakening the production constructor.
func TestingNewHTTPClient(endpoint, bearer string, limits Limits, resolver ipResolver, dial dialContextFunc) (*http.Client, error) {
	return newHTTPClient(endpoint, bearer, limits, resolver, dial)
}

// TestingEndpointTransport exposes the exact-endpoint credential boundary to
// the external security contract suite.
func TestingEndpointTransport(endpoint, bearer string, limits Limits, next http.RoundTripper) (http.RoundTripper, error) {
	limits = limits.normalized()
	target, err := parseEndpoint(endpoint)
	if err != nil {
		return nil, err
	}
	return &endpointRoundTripper{next: next, target: target, bearer: bearer, maxInput: limits.MaxRequestBytes, maxBody: limits.MaxResponseBytes}, nil
}

func TestingIsPublicMCPIP(ip net.IP) bool { return isPublicMCPIP(ip) }

func TestingNormalizedLimits(limits Limits) Limits { return limits.normalized() }

func newHTTPClient(endpoint, bearer string, limits Limits, resolver ipResolver, dial dialContextFunc) (*http.Client, error) {
	limits = limits.normalized()
	target, err := parseEndpoint(endpoint)
	if err != nil {
		return nil, err
	}
	if bearer != "" && (len(bearer) > 16<<10 || !bearerTokenPattern.MatchString(bearer)) {
		return nil, fmt.Errorf("%w: invalid bearer credential", ErrEndpointRejected)
	}
	if resolver == nil {
		return nil, fmt.Errorf("%w: DNS resolver is unavailable", ErrEndpointRejected)
	}
	lookupCtx, cancel := context.WithTimeout(context.Background(), limits.ConnectTimeout)
	defer cancel()
	if _, err := resolvePublicIPs(lookupCtx, resolver, target.Hostname()); err != nil {
		return nil, err
	}

	baseDialer := &net.Dialer{Timeout: limits.ConnectTimeout, KeepAlive: -1}
	if dial == nil {
		dial = baseDialer.DialContext
	}
	transport := &http.Transport{
		Proxy:               nil,
		DisableKeepAlives:   true,
		TLSHandshakeTimeout: limits.TLSHandshakeTimeout,
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			if network != "tcp" && network != "tcp4" && network != "tcp6" {
				return nil, fmt.Errorf("%w: unsupported network", ErrEndpointRejected)
			}
			host, port, splitErr := net.SplitHostPort(address)
			if splitErr != nil || !strings.EqualFold(strings.TrimSuffix(host, "."), strings.TrimSuffix(target.Hostname(), ".")) || port != endpointPort(target) {
				return nil, fmt.Errorf("%w: transport target changed", ErrEndpointRejected)
			}
			ips, resolveErr := resolvePublicIPs(ctx, resolver, host)
			if resolveErr != nil {
				return nil, resolveErr
			}
			var lastErr error
			for _, ip := range ips {
				connection, dialErr := dial(ctx, network, net.JoinHostPort(ip.String(), port))
				if dialErr == nil {
					return connection, nil
				}
				lastErr = dialErr
			}
			if lastErr == nil {
				lastErr = errors.New("no address was available")
			}
			return nil, lastErr
		},
	}
	bound := &endpointRoundTripper{
		next:     transport,
		target:   target,
		bearer:   bearer,
		maxInput: limits.MaxRequestBytes,
		maxBody:  limits.MaxResponseBytes,
	}
	return &http.Client{
		Transport: bound,
		Timeout:   limits.RequestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}, nil
}

func parseEndpoint(raw string) (*url.URL, error) {
	target, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || target.Scheme != "https" || target.Hostname() == "" || target.User != nil || target.Fragment != "" || target.RawQuery != "" {
		return nil, fmt.Errorf("%w: a fixed HTTPS URL without credentials, query, or fragment is required", ErrEndpointRejected)
	}
	if target.Path == "" {
		target.Path = "/"
	}
	if target.RawPath != "" && target.EscapedPath() != target.Path {
		return nil, fmt.Errorf("%w: ambiguous endpoint path", ErrEndpointRejected)
	}
	port := endpointPort(target)
	parsedPort, portErr := strconv.Atoi(port)
	if portErr != nil || parsedPort < 1 || parsedPort > 65535 {
		return nil, fmt.Errorf("%w: invalid endpoint port", ErrEndpointRejected)
	}
	return target, nil
}

func endpointPort(target *url.URL) string {
	if port := target.Port(); port != "" {
		return port
	}
	return "443"
}

func resolvePublicIPs(ctx context.Context, resolver ipResolver, host string) ([]net.IP, error) {
	addresses, err := resolver.LookupIPAddr(ctx, strings.TrimSuffix(host, "."))
	if err != nil || len(addresses) == 0 {
		return nil, fmt.Errorf("%w: endpoint DNS resolution failed", ErrEndpointRejected)
	}
	ips := make([]net.IP, 0, len(addresses))
	for _, address := range addresses {
		if !isPublicMCPIP(address.IP) {
			// Reject the whole answer rather than selecting only its public member;
			// mixed answers are a common DNS-rebinding technique.
			return nil, fmt.Errorf("%w: endpoint resolved to a private or reserved address", ErrEndpointRejected)
		}
		ips = append(ips, append(net.IP(nil), address.IP...))
	}
	return ips, nil
}

func isPublicMCPIP(ip net.IP) bool {
	if ip == nil || !ip.IsGlobalUnicast() || ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		return false
	}
	for _, network := range blockedNetworks {
		if network.Contains(ip) {
			return false
		}
	}
	return true
}

func mustBlockedNetworks() []*net.IPNet {
	cidrs := []string{
		"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24",
		"198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4",
		"::/128", "::1/128", "64:ff9b::/96", "100::/64", "2001:db8::/32",
		"2001:10::/28", "fc00::/7", "fe80::/10", "ff00::/8",
	}
	result := make([]*net.IPNet, 0, len(cidrs))
	for _, cidr := range cidrs {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			panic(err)
		}
		result = append(result, network)
	}
	return result
}

type endpointRoundTripper struct {
	next     http.RoundTripper
	target   *url.URL
	bearer   string
	maxInput int64
	maxBody  int64
}

func (transport *endpointRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	if transport == nil || transport.next == nil || request == nil || request.URL == nil || !sameEndpoint(transport.target, request.URL) {
		return nil, fmt.Errorf("%w: request escaped its configured endpoint", ErrEndpointRejected)
	}
	clone := request.Clone(request.Context())
	clone.Header = request.Header.Clone()
	if request.Body != nil {
		payload, readErr := io.ReadAll(io.LimitReader(request.Body, transport.maxInput+1))
		_ = request.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("%w: request body could not be read", ErrEndpointRejected)
		}
		if int64(len(payload)) > transport.maxInput {
			return nil, fmt.Errorf("%w: request exceeded its configured limit", ErrEndpointRejected)
		}
		clone.Body = io.NopCloser(bytes.NewReader(payload))
		clone.ContentLength = int64(len(payload))
		clone.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(payload)), nil
		}
	}
	clone.Header.Del("Authorization")
	clone.Header.Del("Cookie")
	clone.Header.Del("Proxy-Authorization")
	if transport.bearer != "" {
		clone.Header.Set("Authorization", "Bearer "+transport.bearer)
	}
	response, err := transport.next.RoundTrip(clone)
	if err != nil {
		return nil, err
	}
	if response == nil || response.Body == nil {
		return response, nil
	}
	response.Body = &boundedReadCloser{reader: response.Body, closer: response.Body, remaining: transport.maxBody}
	return response, nil
}

func sameEndpoint(expected, actual *url.URL) bool {
	return expected != nil && actual != nil && actual.Scheme == "https" &&
		strings.EqualFold(expected.Hostname(), actual.Hostname()) && endpointPort(expected) == endpointPort(actual) &&
		expected.EscapedPath() == actual.EscapedPath() && actual.RawQuery == "" && actual.Fragment == "" && actual.User == nil
}

type boundedReadCloser struct {
	reader    io.Reader
	closer    io.Closer
	remaining int64
	exceeded  bool
}

func (body *boundedReadCloser) Read(buffer []byte) (int, error) {
	if body.exceeded {
		return 0, ErrResponseTooLarge
	}
	if body.remaining > 0 {
		if int64(len(buffer)) > body.remaining {
			buffer = buffer[:body.remaining]
		}
		n, err := body.reader.Read(buffer)
		body.remaining -= int64(n)
		return n, err
	}
	var probe [1]byte
	n, err := body.reader.Read(probe[:])
	if n > 0 {
		body.exceeded = true
		return 0, ErrResponseTooLarge
	}
	return 0, err
}

func (body *boundedReadCloser) Close() error {
	return body.closer.Close()
}
