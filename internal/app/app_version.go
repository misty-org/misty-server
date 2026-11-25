package app

import (
	"cmp"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/misty-org/misty-server/internal/platform/config"
)

const appVersionHeader = "X-Misty-App-Version"

type appVersion struct {
	core       [3]uint64
	prerelease []string
}

func parseAppVersion(raw string) (appVersion, bool) {
	raw = strings.TrimPrefix(strings.TrimSpace(raw), "v")
	raw, _, _ = strings.Cut(raw, "+")
	core, prerelease, hasPrerelease := strings.Cut(raw, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 || (hasPrerelease && prerelease == "") {
		return appVersion{}, false
	}
	var version appVersion
	for index, part := range parts {
		number, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return appVersion{}, false
		}
		version.core[index] = number
	}
	if hasPrerelease {
		version.prerelease = strings.Split(prerelease, ".")
		for _, identifier := range version.prerelease {
			if identifier == "" {
				return appVersion{}, false
			}
		}
	}
	return version, true
}

func compareAppVersions(a, b appVersion) int {
	for index := range a.core {
		if order := cmp.Compare(a.core[index], b.core[index]); order != 0 {
			return order
		}
	}
	switch {
	case len(a.prerelease) == 0 && len(b.prerelease) == 0:
		return 0
	case len(a.prerelease) == 0:
		return 1
	case len(b.prerelease) == 0:
		return -1
	}
	for index := 0; index < len(a.prerelease) && index < len(b.prerelease); index++ {
		if order := comparePrereleaseIdentifiers(a.prerelease[index], b.prerelease[index]); order != 0 {
			return order
		}
	}
	return cmp.Compare(len(a.prerelease), len(b.prerelease))
}

func comparePrereleaseIdentifiers(a, b string) int {
	aNumber, aErr := strconv.ParseUint(a, 10, 64)
	bNumber, bErr := strconv.ParseUint(b, 10, 64)
	switch {
	case aErr == nil && bErr == nil:
		return cmp.Compare(aNumber, bNumber)
	case aErr == nil:
		return -1
	case bErr == nil:
		return 1
	}
	return strings.Compare(a, b)
}

func minimumAppVersionMiddleware() (func(http.Handler) http.Handler, error) {
	raw := strings.TrimSpace(config.Getenv("MISTY_MIN_APP_VERSION"))
	if raw == "" {
		return func(next http.Handler) http.Handler { return next }, nil
	}
	minimum, ok := parseAppVersion(raw)
	if !ok {
		return nil, fmt.Errorf("MISTY_MIN_APP_VERSION must be a version such as 0.1.0 or 0.1.0-beta.2, not %q", raw)
	}
	return requireAppVersion(minimum, raw), nil
}

func requireAppVersion(minimum appVersion, label string) func(http.Handler) http.Handler {
	body, _ := json.Marshal(map[string]string{
		"code":    "app_update_required",
		"message": "This server needs Misty " + label + " or later. Update Misty to keep using it.",
		"minimum": label,
	})
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			version, ok := parseAppVersion(r.Header.Get(appVersionHeader))
			if !ok || compareAppVersions(version, minimum) >= 0 {
				next.ServeHTTP(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusUpgradeRequired)
			_, _ = w.Write(body)
		})
	}
}
