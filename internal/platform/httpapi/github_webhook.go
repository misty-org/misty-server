package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

func TestingValidGitHubWebhookSignature(secret string, payload []byte, signature string) bool {
	prefix, encoded, found := strings.Cut(strings.TrimSpace(signature), "=")
	if !found || prefix != "sha256" {
		return false
	}
	provided, err := hex.DecodeString(encoded)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(payload)
	return hmac.Equal(provided, mac.Sum(nil))
}
