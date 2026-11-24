package spaces

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	IntegrationAccessRoomsRead  = "rooms.read"
	IntegrationAccessRoomsWrite = "rooms.write"

	IntegrationTokenPrefix       = "misty_int_"
	IntegrationTokenSecretLength = 43
	MaxIntegrationNameRunes      = 64
	MaxIntegrationKeyPrefixBytes = 256
	MaxIntegrationAvatarBytes    = 4096
	integrationIDPrefix          = "int_"
	base62Alphabet               = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
)

var ErrInvalidIntegration = errors.New("invalid integration")

var integrationAccessOrder = []string{IntegrationAccessRoomsRead, IntegrationAccessRoomsWrite}

var integrationIDPattern = regexp.MustCompile(`^int_[a-z2-7]{26}$`)

var integrationTokenPattern = regexp.MustCompile(`^misty_int_[0-9A-Za-z]{43}$`)

var integrationIDEncoding = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

func IntegrationAccessChoices() []string { return append([]string(nil), integrationAccessOrder...) }

func NormalizeIntegrationName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if !utf8.ValidString(name) {
		return "", ErrInvalidIntegration
	}
	count := utf8.RuneCountInString(name)
	if count < 1 || count > MaxIntegrationNameRunes {
		return "", ErrInvalidIntegration
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", ErrInvalidIntegration
		}
	}
	return name, nil
}

func NormalizeIntegrationAccess(access []string) ([]string, error) {
	if len(access) == 0 {
		return nil, ErrInvalidIntegration
	}
	wanted := map[string]bool{}
	for _, value := range access {
		value = strings.TrimSpace(value)
		known := false
		for _, choice := range integrationAccessOrder {
			if value == choice {
				known = true
				break
			}
		}
		if !known {
			return nil, ErrInvalidIntegration
		}
		wanted[value] = true
	}
	out := []string{}
	for _, choice := range integrationAccessOrder {
		if wanted[choice] {
			out = append(out, choice)
		}
	}
	return out, nil
}

func NormalizeIntegrationKeyPrefix(prefix string) (string, error) {
	if len(prefix) > MaxIntegrationKeyPrefixBytes || !utf8.ValidString(prefix) {
		return "", ErrInvalidIntegration
	}
	for _, r := range prefix {
		if unicode.IsControl(r) {
			return "", ErrInvalidIntegration
		}
	}
	return prefix, nil
}

func NormalizeIntegrationAvatar(avatar json.RawMessage) (json.RawMessage, error) {
	trimmed := strings.TrimSpace(string(avatar))
	if trimmed == "" || trimmed == "null" {
		return json.RawMessage(`{}`), nil
	}
	if len(trimmed) > MaxIntegrationAvatarBytes {
		return nil, ErrInvalidIntegration
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &fields); err != nil || fields == nil {
		return nil, ErrInvalidIntegration
	}
	for _, value := range fields {
		inner := strings.TrimSpace(string(value))
		if strings.HasPrefix(inner, "{") || strings.HasPrefix(inner, "[") {
			return nil, ErrInvalidIntegration
		}
	}
	compact, err := json.Marshal(fields)
	if err != nil {
		return nil, ErrInvalidIntegration
	}
	return compact, nil
}

func NewIntegrationID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return integrationIDPrefix + integrationIDEncoding.EncodeToString(buf), nil
}

func ValidIntegrationID(id string) bool { return integrationIDPattern.MatchString(id) }

func NewIntegrationToken() (token, hash string, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", err
	}
	token = IntegrationTokenPrefix + encodeBase62(buf, IntegrationTokenSecretLength)
	return token, HashIntegrationToken(token), nil
}

func ValidIntegrationToken(token string) bool { return integrationTokenPattern.MatchString(token) }

func HashIntegrationToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func encodeBase62(raw []byte, width int) string {
	value := new(big.Int).SetBytes(raw)
	base := big.NewInt(62)
	remainder := new(big.Int)
	out := make([]byte, 0, width)
	for value.Sign() > 0 {
		value.QuoRem(value, base, remainder)
		out = append(out, base62Alphabet[remainder.Int64()])
	}
	for len(out) < width {
		out = append(out, base62Alphabet[0])
	}
	for left, right := 0, len(out)-1; left < right; left, right = left+1, right-1 {
		out[left], out[right] = out[right], out[left]
	}
	return string(out)
}
