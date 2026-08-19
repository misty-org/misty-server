package mcp_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	mcp "github.com/kannachi323/misty/server/internal/integrations/mcp"
)

type resolverAnswer struct {
	addresses []net.IPAddr
	err       error
}

type sequenceResolver struct {
	mu      sync.Mutex
	answers []resolverAnswer
	calls   int
}

func (resolver *sequenceResolver) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) {
	resolver.mu.Lock()
	defer resolver.mu.Unlock()
	resolver.calls++
	if len(resolver.answers) == 0 {
		return nil, errors.New("unexpected lookup")
	}
	answer := resolver.answers[0]
	if len(resolver.answers) > 1 {
		resolver.answers = resolver.answers[1:]
	}
	return answer.addresses, answer.err
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func answer(addresses ...string) resolverAnswer {
	items := make([]net.IPAddr, 0, len(addresses))
	for _, address := range addresses {
		items = append(items, net.IPAddr{IP: net.ParseIP(address)})
	}
	return resolverAnswer{addresses: items}
}

func TestNewHTTPClientRejectsUnsafeEndpointsAndCredentials(t *testing.T) {
	public := &sequenceResolver{answers: []resolverAnswer{answer("8.8.8.8")}}
	for _, test := range []struct {
		name, endpoint, bearer string
	}{
		{name: "plaintext", endpoint: "http://mcp.example/mcp"},
		{name: "userinfo", endpoint: "https://user:secret@mcp.example/mcp"},
		{name: "query", endpoint: "https://mcp.example/mcp?forward=https://internal"},
		{name: "fragment", endpoint: "https://mcp.example/mcp#credentials"},
		{name: "header injection", endpoint: "https://mcp.example/mcp", bearer: "token\r\nX-Leak: yes"},
		{name: "scheme in bearer", endpoint: "https://mcp.example/mcp", bearer: "Bearer token"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := mcp.TestingNewHTTPClient(test.endpoint, test.bearer, mcp.Limits{}, public, nil); !errors.Is(err, mcp.ErrEndpointRejected) {
				t.Fatalf("expected endpoint rejection, got %v", err)
			}
		})
	}

	for _, resolver := range []*sequenceResolver{
		{answers: []resolverAnswer{answer("127.0.0.1")}},
		{answers: []resolverAnswer{answer("8.8.8.8", "169.254.169.254")}},
	} {
		if _, err := mcp.TestingNewHTTPClient("https://mcp.example/mcp", "", mcp.Limits{}, resolver, nil); !errors.Is(err, mcp.ErrEndpointRejected) {
			t.Fatalf("expected unsafe DNS answer rejection, got %v", err)
		}
	}
}

func TestHTTPClientRevalidatesDNSBeforeDial(t *testing.T) {
	resolver := &sequenceResolver{answers: []resolverAnswer{answer("8.8.8.8"), answer("10.0.0.8")}}
	dialed := false
	client, err := mcp.TestingNewHTTPClient("https://mcp.example/mcp", "token", mcp.Limits{RequestTimeout: time.Second}, resolver, func(context.Context, string, string) (net.Conn, error) {
		dialed = true
		return nil, errors.New("must not dial")
	})
	if err != nil {
		t.Fatal(err)
	}
	request, _ := http.NewRequest(http.MethodPost, "https://mcp.example/mcp", strings.NewReader(`{}`))
	_, err = client.Do(request)
	if !errors.Is(err, mcp.ErrEndpointRejected) || dialed || resolver.calls != 2 {
		t.Fatalf("DNS rebind was not rejected before dial: calls=%d dialed=%v err=%v", resolver.calls, dialed, err)
	}
}

func TestEndpointTransportContainsCredentialsAndBoundsBodies(t *testing.T) {
	calls := 0
	transport, err := mcp.TestingEndpointTransport(
		"https://mcp.example:8443/mcp", "secret-token",
		mcp.Limits{MaxRequestBytes: 8, MaxResponseBytes: 4},
		roundTripFunc(func(request *http.Request) (*http.Response, error) {
			calls++
			if request.Header.Get("Authorization") != "Bearer secret-token" || request.Header.Get("Cookie") != "" || request.Header.Get("Proxy-Authorization") != "" {
				t.Fatalf("transport leaked or failed to isolate credentials: %#v", request.Header)
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("12345")), Header: make(http.Header)}, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	request, _ := http.NewRequest(http.MethodPost, "https://mcp.example:8443/mcp", strings.NewReader(`{}`))
	request.Header.Set("Authorization", "Bearer attacker-controlled")
	request.Header.Set("Cookie", "session=leak")
	request.Header.Set("Proxy-Authorization", "Basic leak")
	response, err := transport.RoundTrip(request)
	if err != nil {
		t.Fatal(err)
	}
	payload, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if string(payload) != "1234" || !errors.Is(readErr, mcp.ErrResponseTooLarge) || calls != 1 {
		t.Fatalf("bounded response mismatch: payload=%q calls=%d err=%v", payload, calls, readErr)
	}
	for _, escaped := range []string{"https://other.example:8443/mcp", "https://mcp.example:8443/other", "https://mcp.example:8443/mcp?next=internal"} {
		escapedRequest, _ := http.NewRequest(http.MethodPost, escaped, strings.NewReader(`{}`))
		if _, err := transport.RoundTrip(escapedRequest); !errors.Is(err, mcp.ErrEndpointRejected) {
			t.Fatalf("escaped endpoint %q was not rejected: %v", escaped, err)
		}
	}
	oversized, _ := http.NewRequest(http.MethodPost, "https://mcp.example:8443/mcp", strings.NewReader("123456789"))
	if _, err := transport.RoundTrip(oversized); !errors.Is(err, mcp.ErrEndpointRejected) {
		t.Fatalf("oversized request was not rejected: %v", err)
	}
}

func TestMCPClientNeverFollowsRedirects(t *testing.T) {
	client, err := mcp.TestingNewHTTPClient("https://mcp.example/mcp", "token", mcp.Limits{}, &sequenceResolver{answers: []resolverAnswer{answer("8.8.8.8")}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	next, _ := http.NewRequest(http.MethodPost, "https://attacker.example/mcp", nil)
	previous, _ := http.NewRequest(http.MethodPost, "https://mcp.example/mcp", nil)
	if err := client.CheckRedirect(next, []*http.Request{previous}); !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatalf("expected redirect rejection, got %v", err)
	}
}

func TestPublicMCPIPPolicyAndLimitCeilings(t *testing.T) {
	for _, test := range []struct {
		ip      string
		allowed bool
	}{
		{ip: "8.8.8.8", allowed: true}, {ip: "2606:4700:4700::1111", allowed: true},
		{ip: "127.0.0.1"}, {ip: "10.0.0.1"}, {ip: "100.64.0.1"},
		{ip: "169.254.169.254"}, {ip: "192.0.2.1"}, {ip: "198.18.0.1"},
		{ip: "203.0.113.1"}, {ip: "::1"}, {ip: "fc00::1"},
		{ip: "fe80::1"}, {ip: "2001:db8::1"},
	} {
		if got := mcp.TestingIsPublicMCPIP(net.ParseIP(test.ip)); got != test.allowed {
			t.Errorf("public policy for %s=%v, want %v", test.ip, got, test.allowed)
		}
	}
	limits := mcp.TestingNormalizedLimits(mcp.Limits{
		ConnectTimeout: 24 * time.Hour, TLSHandshakeTimeout: 24 * time.Hour,
		RequestTimeout: 24 * time.Hour, MaxRequestBytes: 20 << 20,
		MaxResponseBytes: 20 << 20, MaxCatalogTools: 10_000, MaxToolNameBytes: 10_000,
	})
	if limits.ConnectTimeout > 10*time.Second || limits.TLSHandshakeTimeout > 10*time.Second || limits.RequestTimeout > time.Minute ||
		limits.MaxRequestBytes > 1<<20 || limits.MaxResponseBytes > 8<<20 || limits.MaxCatalogTools > 512 || limits.MaxToolNameBytes > 512 {
		t.Fatalf("limits exceeded security ceilings: %+v", limits)
	}
}
