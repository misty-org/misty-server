package api

import (
	"net"
	"net/http"
	"strings"
	"sync"

	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
)

// Client IP resolution for rate limiting and abuse blocking.
//
// Getting this wrong defeats every limit in the server at once, so the rules
// are deliberately conservative:
//
//  1. Forwarded headers are ignored unless TRUST_PROXY_HEADERS is set.
//  2. Even then, they are honoured only when the request actually arrived from
//     a trusted proxy address — otherwise a direct caller could forge them.
//  3. The chain is read right-to-left, skipping trusted hops. The standard
//     nginx recipe ($proxy_add_x_forwarded_for) *appends* the real peer, so
//     anything a client sends sits to the left of the truth. Reading the
//     left-most entry — the previous behaviour — took the attacker's value.

// defaultTrustedProxyCIDRs covers loopback and the private ranges a reverse
// proxy realistically sits in. Public proxy ranges (Cloudflare, a load
// balancer) must be added explicitly through TRUSTED_PROXY_CIDRS.
var defaultTrustedProxyCIDRs = []string{
	"127.0.0.0/8", "::1/128",
	"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16",
	"169.254.0.0/16", "fe80::/10", "fc00::/7",
}

var (
	trustedProxyOnce sync.Once
	trustedProxyNets []*net.IPNet
)

// trustedProxyNetworks parses the trusted set once per process.
func trustedProxyNetworks() []*net.IPNet {
	trustedProxyOnce.Do(func() {
		entries := append([]string{}, defaultTrustedProxyCIDRs...)
		if configured := strings.TrimSpace(envconfig.Getenv("TRUSTED_PROXY_CIDRS")); configured != "" {
			entries = append(entries, strings.Split(configured, ",")...)
		}
		for _, entry := range entries {
			entry = strings.TrimSpace(entry)
			if entry == "" {
				continue
			}
			// A bare address is accepted as a single-host network so operators
			// can pin one proxy without writing a mask.
			if !strings.Contains(entry, "/") {
				if ip := net.ParseIP(entry); ip != nil {
					bits := 32
					if ip.To4() == nil {
						bits = 128
					}
					entry += "/" + itoa(bits)
				}
			}
			if _, network, err := net.ParseCIDR(entry); err == nil {
				trustedProxyNets = append(trustedProxyNets, network)
			}
		}
	})
	return trustedProxyNets
}

// ResetTrustedProxyCacheForTest re-reads TRUSTED_PROXY_CIDRS. Tests change the
// environment between cases; production parses once and never calls this.
func ResetTrustedProxyCacheForTest() {
	trustedProxyOnce = sync.Once{}
	trustedProxyNets = nil
}

func isTrustedProxyAddress(address string) bool {
	ip := net.ParseIP(strings.TrimSpace(address))
	if ip == nil {
		return false
	}
	for _, network := range trustedProxyNetworks() {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

// clientIPFromRequest returns the address rate limits are keyed on.
func TestingClientIPFromRequest(r *http.Request) string {
	remote := remoteHostFromRequest(r)
	if !trustProxyHeaders() {
		return remote
	}
	// Forged headers from a direct connection must not be believed. Only a
	// request that genuinely came from a trusted proxy may carry a chain.
	if !isTrustedProxyAddress(remote) {
		return remote
	}
	if forwarded := forwardedClientIP(r.Header.Get("X-Forwarded-For")); forwarded != "" {
		return forwarded
	}
	// X-Real-IP is a single value set by the proxy itself, so it is not
	// attacker-extendable in the way a chain is.
	if realIP := strings.TrimSpace(r.Header.Get("X-Real-IP")); net.ParseIP(realIP) != nil {
		return realIP
	}
	return remote
}

// forwardedClientIP walks the chain right-to-left and returns the first address
// that is not itself a trusted proxy.
func forwardedClientIP(header string) string {
	parts := strings.Split(header, ",")
	for index := len(parts) - 1; index >= 0; index-- {
		candidate := strings.TrimSpace(parts[index])
		if candidate == "" {
			continue
		}
		// Some proxies emit "ip:port" or a bracketed IPv6 literal.
		if host, _, err := net.SplitHostPort(candidate); err == nil {
			candidate = host
		}
		candidate = strings.Trim(candidate, "[]")
		if net.ParseIP(candidate) == nil {
			// A malformed hop means the chain cannot be trusted further left.
			return ""
		}
		if isTrustedProxyAddress(candidate) {
			continue
		}
		return candidate
	}
	return ""
}

func remoteHostFromRequest(r *http.Request) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil && host != "" {
		return host
	}
	if remote := strings.TrimSpace(r.RemoteAddr); remote != "" {
		return remote
	}
	return "unknown"
}

func trustProxyHeaders() bool {
	value := strings.ToLower(strings.TrimSpace(envconfig.Getenv("TRUST_PROXY_HEADERS")))
	return value == "1" || value == "true" || value == "yes"
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := ""
	for value > 0 {
		digits = string(rune('0'+value%10)) + digits
		value /= 10
	}
	return digits
}
