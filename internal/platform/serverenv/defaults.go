package serverenv

import (
	"fmt"
	"strings"
)

type Names struct {
	Tunnel, LibraryBucket, BackupBucket, LibraryToken, BackupToken string
}

func ResourceNames(hostname string) Names {
	slug := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(hostname)), ".", "-")
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
	}
}

const LiveKitAPIKey = "misty"

func CallsOn(getenv func(string) string) bool {
	switch strings.ToLower(strings.TrimSpace(getenv("MISTY_CALLS"))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func CallsHostname(hostname string) string {
	hostname = strings.ToLower(strings.TrimSpace(hostname))
	first, rest, found := strings.Cut(hostname, ".")
	if !found || !strings.Contains(rest, ".") {
		return "calls." + hostname
	}
	return first + "-calls." + rest
}

func PublicAPIURL(hostname string) string {
	return "https://" + strings.ToLower(strings.TrimSpace(hostname)) + "/v1"
}

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
		if CallsOn(getenv) && unset("LIVEKIT_URL") {
			values["LIVEKIT_URL"] = "wss://" + CallsHostname(hostname)
		}
	}
	return values, nil
}
