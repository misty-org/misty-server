package accounts

import (
	"crypto/rand"
	"regexp"
	"strings"
	"time"

	"github.com/misty-org/misty-server/internal/platform/security"
)

const (
	DeviceSignInTTL           = 10 * time.Minute
	DeviceSignInMaxWrongCodes = 5
	UserCodeAlphabet          = "BCDFGHJKLMNPQRSTVWXZ"
	UserCodeLength            = 8
	UserCodePrefixLength      = 4
	DeviceVerificationURL     = "misty://device?code="
	deviceSignInIDPrefix      = "dsi_"
)

var deviceSignInIDPattern = regexp.MustCompile(`^dsi_[A-Za-z0-9_-]{43}$`)

func NewUserCode() (string, error) {
	out := make([]byte, 0, UserCodeLength)
	buf := make([]byte, UserCodeLength*2)
	limit := byte(256 - 256%len(UserCodeAlphabet))
	for len(out) < UserCodeLength {
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		for _, b := range buf {
			if b >= limit {
				continue
			}
			out = append(out, UserCodeAlphabet[int(b)%len(UserCodeAlphabet)])
			if len(out) == UserCodeLength {
				break
			}
		}
	}
	return string(out), nil
}

func FormatUserCode(code string) string {
	if len(code) != UserCodeLength {
		return code
	}
	return code[:UserCodePrefixLength] + "-" + code[UserCodePrefixLength:]
}

func NormalizeUserCode(input string) (string, bool) {
	var b strings.Builder
	for _, r := range strings.ToUpper(input) {
		switch {
		case r == '-' || r == ' ':
			continue
		case strings.ContainsRune(UserCodeAlphabet, r):
			b.WriteRune(r)
		default:
			return "", false
		}
		if b.Len() > UserCodeLength {
			return "", false
		}
	}
	code := b.String()
	return code, len(code) == UserCodeLength
}

func UserCodePrefix(code string) string {
	if len(code) < UserCodePrefixLength {
		return code
	}
	return code[:UserCodePrefixLength]
}

func HashUserCode(code string) string { return security.HashToken("misty-device-code:" + code) }

func NewDeviceSignInID() (string, error) {
	token, err := security.GenerateSecureToken()
	if err != nil {
		return "", err
	}
	return deviceSignInIDPrefix + token, nil
}

func ValidDeviceSignInID(id string) bool { return deviceSignInIDPattern.MatchString(id) }

func HashDeviceSignInID(id string) string { return security.HashToken("misty-device-sign-in:" + id) }

func DeviceVerificationLink(code string) string { return DeviceVerificationURL + FormatUserCode(code) }
