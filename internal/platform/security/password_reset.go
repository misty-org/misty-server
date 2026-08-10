package security

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"time"
)

const (
	// PasswordResetTokenTTL keeps reset links short-lived and limits replay risk.
	PasswordResetTokenTTL = 15 * time.Minute
	passwordResetTokenLen = 32
)

// GenerateSecureToken returns a URL-safe token backed by cryptographic randomness.
func GenerateSecureToken() (string, error) {
	buf := make([]byte, passwordResetTokenLen)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate secure token: %w", err)
	}

	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// HashToken stores only a SHA-256 digest so raw reset tokens never reach the database.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
