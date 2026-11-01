package main

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/kannachi323/misty/server/internal/platform/config"
)

// requiredInputs are the values the operator writes into the four files under
// server/.env/. Every one is required: Misty has no optional integrations. The
// server sends no email, so there is no mail provider to configure.
var requiredInputs = []struct{ File, Name string }{
	{"misty.env", "MISTY_HOSTNAME"},
	{"cloudflare.env", "CLOUDFLARE_API_TOKEN"},
	{"cloudflare.env", "CLOUDFLARE_ACCOUNT_ID"},
	{"cloudflare.env", "CLOUDFLARE_ZONE_ID"},
	{"composio.env", "MISTY_COMPOSIO_API_KEY"},
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
}

var (
	cloudflareID = regexp.MustCompile(`^[0-9a-f]{32}$`)
	dnsLabel     = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
)

func readInputs(getenv func(string) string) (Inputs, error) {
	var missing []string
	for _, input := range requiredInputs {
		if strings.TrimSpace(getenv(input.Name)) == "" {
			missing = append(missing, fmt.Sprintf("%s in server/.env/%s", input.Name, input.File))
		}
	}
	if len(missing) > 0 {
		return Inputs{}, fmt.Errorf("missing %s (docs/self-hosting.md lists every value)", strings.Join(missing, ", "))
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
	if err := validHostname(inputs.Hostname); err != nil {
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
	return inputs, nil
}

func validHostname(host string) error {
	if len(host) > 253 || !strings.Contains(host, ".") {
		return fmt.Errorf("MISTY_HOSTNAME must be a DNS name such as cloud.misty.example, without scheme or path")
	}
	for _, label := range strings.Split(host, ".") {
		if !dnsLabel.MatchString(label) {
			return fmt.Errorf("MISTY_HOSTNAME must be a DNS name such as cloud.misty.example, without scheme or path")
		}
	}
	return nil
}

// Names derives every Cloudflare resource name from the hostname, so a second
// run (or a rebuilt server with the same files) finds the same resources.
type Names struct {
	Tunnel, LibraryBucket, BackupBucket, LibraryToken, BackupToken, CollabWorker, FetchWorker string
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
		FetchWorker:   base + "-web-fetch",
	}
}

// deriveConfig writes the values the API reads that follow from the hostname.
func deriveConfig(store *Store, inputs Inputs) {
	origin := "https://" + inputs.Hostname
	store.Set("MISTY_PUBLIC_API_URL", origin+"/v1")
	store.Set("AUTH_HANDOFF_START_URL", origin+"/v1/auth/handoff/start")
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(config.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
