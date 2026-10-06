package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net"
	"net/netip"
)

// networkKey makes a device's public network comparable without revealing it:
// devices behind the same router share a key. IPv6 compares the /64 prefix.
func (h *deviceHub) networkKey(remote string) string {
	address, err := netip.ParseAddr(remote)
	if err != nil {
		return ""
	}
	address = address.Unmap()
	label := "v4:" + address.String()
	if address.Is6() {
		prefix, prefixErr := address.Prefix(64)
		if prefixErr != nil {
			return ""
		}
		label = "v6:" + prefix.String()
	}
	mac := hmac.New(sha256.New, h.secret)
	mac.Write([]byte("misty.device.network.v1\n" + label))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil)[:12])
}

// lanCandidate accepts only "ip:port" addresses in LAN ranges: private,
// link-local, shared overlay space (e.g. Tailscale) and IPv6 unique-local.
func lanCandidate(value string) (string, bool, bool) {
	address, err := netip.ParseAddrPort(value)
	if err != nil || address.Port() == 0 {
		return "", false, false
	}
	ip := address.Addr().Unmap()
	overlay := ip.Is4() && ip.As4()[0] == 100 && ip.As4()[1]&0xc0 == 64
	lan := ip.IsPrivate() || ip.IsLinkLocalUnicast() || overlay
	if !lan || ip.IsLoopback() || ip.IsUnspecified() || ip.IsMulticast() {
		return "", false, false
	}
	return netip.AddrPortFrom(ip, address.Port()).String(), overlay, true
}

// remoteIP strips the port from a connection's remote address.
func remoteIP(address string) string {
	if host, _, err := net.SplitHostPort(address); err == nil {
		return host
	}
	return address
}
