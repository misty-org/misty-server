package main

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/misty-org/misty-server/internal/platform/config"
)

// requiredInputs are the values the operator writes into the three files under
// ~/.misty/.env/server/. Every one is required: Misty has no optional integrations. Setup
// configures no mail provider, so a server it sets up sends no email.
var requiredInputs = []struct{ File, Name string }{
	{"misty.env", "MISTY_HOSTNAME"},
	{"cloudflare.env", "CLOUDFLARE_API_TOKEN"},
	{"cloudflare.env", "CLOUDFLARE_ACCOUNT_ID"},
	{"cloudflare.env", "CLOUDFLARE_ZONE_ID"},
	{"google.env", "GOOGLE_CLIENT_ID"},
	{"google.env", "GOOGLE_CLIENT_SECRET"},
}

// Inputs are the operator's values that setup itself uses.
type Inputs struct {
	Hostname  string
	Token     string
	AccountID string
	ZoneID    string
	// APIURL is the Cloudflare API base. Only tests point it elsewhere.
	APIURL string
	// Origins are extra web origins allowed to upload to the Library bucket.
	Origins []string
	// ExtraIngress are more hostnames the tunnel publishes, for services that
	// run beside the server (MISTY_TUNNEL_EXTRA_INGRESS). Usually empty.
	ExtraIngress []IngressRule
}

// IngressRule routes one public hostname to a service inside the Compose
// network.
type IngressRule struct {
	Host, Service string
}

var (
	cloudflareID = regexp.MustCompile(`^[0-9a-f]{32}$`)
	dnsLabel     = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
)

func readInputs(getenv func(string) string) (Inputs, error) {
	var missing []string
	for _, input := range requiredInputs {
		if strings.TrimSpace(getenv(input.Name)) == "" {
			missing = append(missing, fmt.Sprintf("%s in ~/.misty/.env/server/%s", input.Name, input.File))
		}
	}
	if len(missing) > 0 {
		return Inputs{}, fmt.Errorf("missing %s", strings.Join(missing, ", "))
	}
	inputs := Inputs{
		Hostname:  strings.ToLower(strings.TrimSpace(getenv("MISTY_HOSTNAME"))),
		Token:     strings.TrimSpace(getenv("CLOUDFLARE_API_TOKEN")),
		AccountID: strings.ToLower(strings.TrimSpace(getenv("CLOUDFLARE_ACCOUNT_ID"))),
		ZoneID:    strings.ToLower(strings.TrimSpace(getenv("CLOUDFLARE_ZONE_ID"))),
		APIURL:    strings.TrimRight(strings.TrimSpace(getenv("MISTY_CLOUDFLARE_API_URL")), "/"),
	}
	if inputs.APIURL == "" {
		inputs.APIURL = "https://api.cloudflare.com/client/v4"
	}
	if err := validHostname("MISTY_HOSTNAME", inputs.Hostname); err != nil {
		return Inputs{}, err
	}
	if !cloudflareID.MatchString(inputs.AccountID) || !cloudflareID.MatchString(inputs.ZoneID) {
		return Inputs{}, fmt.Errorf("CLOUDFLARE_ACCOUNT_ID and CLOUDFLARE_ZONE_ID are 32 hexadecimal characters")
	}
	if strings.ContainsAny(inputs.Token, " \t\r\n\"\\") {
		return Inputs{}, fmt.Errorf("CLOUDFLARE_API_TOKEN has an invalid format")
	}
	for _, origin := range strings.Split(getenv("MISTY_ALLOWED_ORIGINS"), ",") {
		if origin = strings.TrimSpace(origin); origin == "" {
			continue
		}
		if parsed, err := url.Parse(origin); err != nil || parsed.Scheme != "https" || parsed.Host == "" || strings.Trim(parsed.Path, "/") != "" || strings.Contains(origin, "*") {
			return Inputs{}, fmt.Errorf("MISTY_ALLOWED_ORIGINS must list https origins without paths: %q", origin)
		}
		inputs.Origins = append(inputs.Origins, strings.TrimRight(origin, "/"))
	}
	extra, err := parseExtraIngress(getenv("MISTY_TUNNEL_EXTRA_INGRESS"), inputs.Hostname)
	if err != nil {
		return Inputs{}, err
	}
	inputs.ExtraIngress = extra
	return inputs, nil
}

// parseExtraIngress reads a comma-separated list of host=service entries, such
// as billing.misty.example=http://billing:8091. Each host is a DNS name like
// MISTY_HOSTNAME, and each service an http or https URL with a host and no path.
func parseExtraIngress(value, hostname string) ([]IngressRule, error) {
	var rules []IngressRule
	seen := map[string]bool{hostname: true}
	for _, entry := range strings.Split(value, ",") {
		if entry = strings.TrimSpace(entry); entry == "" {
			continue
		}
		host, service, ok := strings.Cut(entry, "=")
		host, service = strings.ToLower(strings.TrimSpace(host)), strings.TrimSpace(service)
		if !ok || host == "" || service == "" {
			return nil, fmt.Errorf("MISTY_TUNNEL_EXTRA_INGRESS entries are host=service, such as billing.misty.example=http://billing:8091: %q", entry)
		}
		if err := validHostname("MISTY_TUNNEL_EXTRA_INGRESS host "+host, host); err != nil {
			return nil, err
		}
		if seen[host] {
			return nil, fmt.Errorf("MISTY_TUNNEL_EXTRA_INGRESS lists %s twice, or as MISTY_HOSTNAME", host)
		}
		seen[host] = true
		parsed, err := url.Parse(service)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" ||
			parsed.User != nil || strings.Trim(parsed.Path, "/") != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			return nil, fmt.Errorf("MISTY_TUNNEL_EXTRA_INGRESS service for %s must be an http or https URL without a path, such as http://billing:8091: %q", host, service)
		}
		rules = append(rules, IngressRule{Host: host, Service: strings.TrimRight(service, "/")})
	}
	return rules, nil
}

// validHostname checks that host is a DNS name; name says which setting it
// came from.
func validHostname(name, host string) error {
	if len(host) > 253 || !strings.Contains(host, ".") {
		return fmt.Errorf("%s must be a DNS name such as cloud.misty.example, without scheme or path", name)
	}
	for _, label := range strings.Split(host, ".") {
		if !dnsLabel.MatchString(label) {
			return fmt.Errorf("%s must be a DNS name such as cloud.misty.example, without scheme or path", name)
		}
	}
	return nil
}

// Names derives every Cloudflare resource name from the hostname, so a second
// run (or a rebuilt server with the same files) finds the same resources.
type Names struct {
	Tunnel, LibraryBucket, BackupBucket, LibraryToken, BackupToken, CollabWorker string
}

func resourceNames(hostname string) Names {
	slug := strings.ReplaceAll(hostname, ".", "-")
	// Worker and bucket names allow 63 characters; leave room for suffixes.
	if len(slug) > 40 {
		slug = strings.TrimRight(slug[:40], "-")
	}
	base := "misty-" + slug
	return Names{
		Tunnel:        base,
		LibraryBucket: base + "-library",
		BackupBucket:  base + "-backups",
		LibraryToken:  base + " library",
		BackupToken:   base + " backups",
		CollabWorker:  base + "-collab",
	}
}

// deriveConfig writes the values the API reads that follow from the hostname.
func deriveConfig(store *Store, inputs Inputs) {
	origin := "https://" + inputs.Hostname
	store.Set("MISTY_PUBLIC_API_URL", origin+"/v1")
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(config.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
