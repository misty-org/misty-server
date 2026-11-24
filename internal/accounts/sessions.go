package accounts

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	DefaultSessionName   = "Misty"
	MaxDeviceNameRunes   = 64
	appSessionNamePrefix = "Misty on "
)

func NormalizeDeviceName(name string) (string, bool) {
	name = strings.TrimSpace(name)
	if !utf8.ValidString(name) {
		return "", false
	}
	count := utf8.RuneCountInString(name)
	if count < 1 || count > MaxDeviceNameRunes {
		return "", false
	}
	for _, r := range name {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return "", false
		}
	}
	return name, true
}

func AppSessionName(host string) string {
	if name, ok := NormalizeDeviceName(host); ok {
		return appSessionNamePrefix + name
	}
	return DefaultSessionName
}

func DisplaySessionName(stored string) string {
	if strings.TrimSpace(stored) == "" {
		return DefaultSessionName
	}
	return stored
}
