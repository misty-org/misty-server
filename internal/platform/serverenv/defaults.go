package serverenv

import (
	"fmt"
	"strings"
)

// Names derives every Cloudflare resource name from the hostname, so a second
// setup run (or a rebuilt server with the same files) finds the same
// resources, and the API knows its bucket without being told.
type Names struct {
	Tunnel, LibraryBucket, BackupBucket, LibraryToken, BackupToken, CollabWorker string
}

// ResourceNames returns the names for a hostname.
func ResourceNames(hostname string) Names {
	slug := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(hostname)), ".", "-")
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

// LiveKitAPIKey is the name LiveKit knows the server's key by; the secret is LIVEKIT_API_SECRET.
const LiveKitAPIKey = "misty"

// CallsOn says whether this stack runs LiveKit for calls (MISTY_CALLS, with the compose profile "calls").
func CallsOn(getenv func(string) string) bool {
	switch strings.ToLower(strings.TrimSpace(getenv("MISTY_CALLS"))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// CallsHostname is where LiveKit's signalling is published through the tunnel: a sibling of the API's
// hostname ("misty-calls.example.com" beside "misty.example.com"), so Cloudflare's universal
// certificate covers it; for an apex hostname, "calls.example.com".
func CallsHostname(hostname string) string {
	hostname = strings.ToLower(strings.TrimSpace(hostname))
	first, rest, found := strings.Cut(hostname, ".")
	if !found || !strings.Contains(rest, ".") {
		return "calls." + hostname
	}
	return first + "-calls." + rest
}

// PublicAPIURL is the API's public base for a hostname.
func PublicAPIURL(hostname string) string {
	return "https://" + strings.ToLower(strings.TrimSpace(hostname)) + "/v1"
}

// Defaults returns the values a server works out for itself, for every one
// the environment leaves unset: keys from the API secret, and in production
// the public API URL and the Library bucket from MISTY_HOSTNAME. A value that
// is set always wins, so a server with keys of its own (a development
// stack, or a stack set up before the root secret) keeps them.
func Defaults(getenv func(string) string) (map[string]string, error) {
	values := map[string]string{}
	unset := func(name string) bool { return strings.TrimSpace(getenv(name)) == "" }
	if encoded := strings.TrimSpace(getenv(APISecretName)); encoded != "" {
		secret, err := DecodeSecret(APISecretName, encoded)
		if err != nil {
			return nil, err
		}
		versions, err := ParseVersions(getenv(VersionsName))
		if err != nil {
			return nil, err
		}
		for _, key := range Keys {
			if key.Scope != FromAPISecret {
				continue
			}
			version := versions.Of(key.Purpose)
			if unset(key.Name) {
				if values[key.Name], err = key.Value(secret, version.Current); err != nil {
					return nil, fmt.Errorf("%s: %w", key.Name, err)
				}
			}
			if key.Previous != "" && version.Previous > 0 && unset(key.Previous) {
				if values[key.Previous], err = key.PreviousValue(secret, version.Previous); err != nil {
					return nil, fmt.Errorf("%s: %w", key.Previous, err)
				}
			}
		}
		if unset("LIVEKIT_API_KEY") {
			values["LIVEKIT_API_KEY"] = LiveKitAPIKey
		}
	}
	hostname := strings.TrimSpace(getenv("MISTY_HOSTNAME"))
	if hostname != "" && strings.EqualFold(strings.TrimSpace(getenv("MISTY_ENVIRONMENT")), "production") {
		if unset("MISTY_PUBLIC_API_URL") {
			values["MISTY_PUBLIC_API_URL"] = PublicAPIURL(hostname)
		}
		if unset("R2_BUCKET") {
			values["R2_BUCKET"] = ResourceNames(hostname).LibraryBucket
		}
		// Calls are offered only where the stack runs LiveKit, reached through the tunnel over TLS.
		if CallsOn(getenv) && unset("LIVEKIT_URL") {
			values["LIVEKIT_URL"] = "wss://" + CallsHostname(hostname)
		}
	}
	return values, nil
}
