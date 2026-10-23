package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/kannachi323/misty/server/internal/agent"
)

// addressWithoutHost turns an address whose host has no DNS record at all into a correction the
// agent can act on, before any tab opens. An agent that guessed jjs-house.com for jjshouse.com
// opened it twice, got pages that never loaded and called the site a blocker (Odysseys, October
// 20). Only a definite "no such host" counts: a slow or failing lookup leaves the page to load,
// and names that only a local network knows are not looked up here.
func addressWithoutHost(ctx context.Context, raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil
	}
	host := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	if host == "" || net.ParseIP(host) != nil || !strings.Contains(host, ".") || localNetworkHost(host) {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_, err = net.DefaultResolver.LookupHost(ctx, host)
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
		return agent.ErrInvalidRequest(fmt.Sprintf("%s does not exist, so the address is wrong and nothing was opened. Find the site's real address with web_search (search for its name), then open that. If the site itself is gone (renamed, merged into another, shut down), do not retry other spellings of the same address: search for where its coverage lives now, open that, and say so in your answer.", host))
	}
	return nil
}

func localNetworkHost(host string) bool {
	for _, suffix := range []string{".local", ".lan", ".home", ".internal", ".home.arpa", ".localhost", ".test"} {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	return false
}

// navigationAddressWithoutHost applies addressWithoutHost to browser.navigate's url.
func navigationAddressWithoutHost(ctx context.Context, tool agent.ToolRequest) error {
	if tool.Name != "browser.navigate" {
		return nil
	}
	var input struct {
		URL string `json:"url"`
	}
	if json.Unmarshal(tool.Arguments, &input) != nil {
		return nil
	}
	return addressWithoutHost(ctx, input.URL)
}
