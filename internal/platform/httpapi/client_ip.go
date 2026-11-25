package api

import (
	"net"
	"net/http"
	"strings"
	"sync"

	"github.com/misty-org/misty-server/internal/platform/config"
)

var defaultTrustedProxyCIDRs = []string{
	"127.0.0.0/8", "::1/128",
	"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16",
	"169.254.0.0/16", "fe80::/10", "fc00::/7",
}

var (
	trustedProxyOnce sync.Once
	trustedProxyNets []*net.IPNet
)

func trustedProxyNetworks() []*net.IPNet {
	trustedProxyOnce.Do(func() {
		entries := append([]string{}, defaultTrustedProxyCIDRs...)
		if configured := strings.TrimSpace(config.Getenv("TRUSTED_PROXY_CIDRS")); configured != "" {
			entries = append(entries, strings.Split(configured, ",")...)
		}
		for _, entry := range entries {
			entry = strings.TrimSpace(entry)
			if entry == "" {
				continue
			}
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

func ClientIPFromRequest(r *http.Request) string {
	remote := remoteHostFromRequest(r)
	if !trustProxyHeaders() {
		return remote
	}
	if !isTrustedProxyAddress(remote) {
		return remote
	}
	if cloudflare := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); net.ParseIP(cloudflare) != nil {
		return cloudflare
	}
	if forwarded := forwardedClientIP(r.Header.Get("X-Forwarded-For")); forwarded != "" {
		return forwarded
	}
	if realIP := strings.TrimSpace(r.Header.Get("X-Real-IP")); net.ParseIP(realIP) != nil {
		return realIP
	}
	return remote
}

func forwardedClientIP(header string) string {
	parts := strings.Split(header, ",")
	for index := len(parts) - 1; index >= 0; index-- {
		candidate := strings.TrimSpace(parts[index])
		if candidate == "" {
			continue
		}
		if host, _, err := net.SplitHostPort(candidate); err == nil {
			candidate = host
		}
		candidate = strings.Trim(candidate, "[]")
		if net.ParseIP(candidate) == nil {
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
	value := strings.ToLower(strings.TrimSpace(config.Getenv("TRUST_PROXY_HEADERS")))
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
