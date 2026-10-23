package api

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"
)

func validDeviceName(name string) bool {
	return validText(name, 1, 64) && !strings.ContainsFunc(name, func(value rune) bool { return value < 0x20 || value == 0x7f })
}

func validText(value string, min, max int) bool {
	length := utf8.RuneCountInString(strings.TrimSpace(value))
	return length >= min && length <= max
}

func validJSONObject(raw json.RawMessage) bool {
	var value any
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil {
		return false
	}
	_, ok := value.(map[string]any)
	return ok
}

func TestingValidDeviceRegistration(name, key, algorithm string, capabilities json.RawMessage) bool {
	return ValidDeviceRegistrationV2(name, key, algorithm, "", "", nil, capabilities)
}

func ValidDeviceRegistrationV2(name, key, algorithm, platform, endpointID string, protocolVersions, capabilities json.RawMessage) bool {
	decodedKey, err := decodeDeviceBase64(key)
	if !validText(name, 1, 100) || err != nil || len(decodedKey) != ed25519.PublicKeySize || (algorithm != "" && algorithm != "ed25519") || !validJSONObject(capabilities) || containsLocalPath(capabilities) || containsClipboardValue(capabilities) {
		return false
	}
	platform = normalizedDevicePlatform(platform)
	if platform == "" || (endpointID != "" && !p2pEndpointIDPattern.MatchString(endpointID)) {
		return false
	}
	if len(protocolVersions) == 0 {
		return endpointID == ""
	}
	var versions []string
	if json.Unmarshal(protocolVersions, &versions) != nil || len(versions) > 8 {
		return false
	}
	for _, version := range versions {
		if version != "misty-device/1" {
			return false
		}
	}
	return (endpointID == "") == (len(versions) == 0)
}

func normalizedDevicePlatform(platform string) string {
	platform = strings.ToLower(strings.TrimSpace(platform))
	if platform == "" {
		return "unknown"
	}
	for _, allowed := range []string{"macos", "windows", "linux", "unknown"} {
		if platform == allowed {
			return platform
		}
	}
	return ""
}

func decodeDeviceBase64(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if decoded, err := encoding.DecodeString(value); err == nil {
			return decoded, nil
		}
	}
	return nil, errors.New("invalid base64")
}

func containsLocalPath(raw json.RawMessage) bool {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return true
	}
	return containsPathValue(value)
}

func containsPathValue(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			normalized := strings.ToLower(strings.ReplaceAll(key, "_", ""))
			if normalized == "path" || strings.HasSuffix(normalized, "path") {
				return true
			}
			if containsPathValue(child) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if containsPathValue(child) {
				return true
			}
		}
	}
	return false
}
