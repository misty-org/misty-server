package entitlement

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	Issuer        = "misty-hosted"
	Audience      = "misty-self-hosted"
	SchemaVersion = 1
	MaxLifetime   = 7 * 24 * time.Hour
)

var (
	ErrInvalid = errors.New("invalid self-host entitlement")
	ErrExpired = errors.New("self-host entitlement expired")
)

// BundledPublicKeys is intentionally versioned. New releases append keys; old
// keys can remain long enough for already-issued seven-day proofs to expire.
var BundledPublicKeys = map[string]ed25519.PublicKey{
	"misty-2026-01": mustPublicKey("d67rDV3YPzm1bhUKc5tmoML7qGAaVBEYi6GgfMoJQCA="),
}

type Claims struct {
	Subject       string `json:"sub"`
	Status        string `json:"status"`
	IssuedAt      int64  `json:"iat"`
	ExpiresAt     int64  `json:"exp"`
	TokenID       string `json:"jti"`
	SchemaVersion int    `json:"schema_version"`
	Issuer        string `json:"iss"`
	Audience      string `json:"aud"`
}

type Signer struct {
	PrivateKey ed25519.PrivateKey
	KeyID      string
}

func (s Signer) Sign(claims Claims) (string, error) {
	if len(s.PrivateKey) != ed25519.PrivateKeySize || strings.TrimSpace(s.KeyID) == "" {
		return "", fmt.Errorf("%w: signing key is not configured", ErrInvalid)
	}
	header, err := json.Marshal(map[string]string{"alg": "EdDSA", "kid": s.KeyID, "typ": "JWT"})
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	input := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	signature := ed25519.Sign(s.PrivateKey, []byte(input))
	return input + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func Verify(token string, keys map[string]ed25519.PublicKey, now time.Time) (Claims, error) {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 3 {
		return Claims{}, ErrInvalid
	}
	var header struct {
		Algorithm string `json:"alg"`
		KeyID     string `json:"kid"`
	}
	if err := decodePart(parts[0], &header); err != nil || header.Algorithm != "EdDSA" {
		return Claims{}, ErrInvalid
	}
	publicKey, ok := keys[header.KeyID]
	if !ok || len(publicKey) != ed25519.PublicKeySize {
		return Claims{}, ErrInvalid
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || !ed25519.Verify(publicKey, []byte(parts[0]+"."+parts[1]), signature) {
		return Claims{}, ErrInvalid
	}
	var claims Claims
	if err := decodePart(parts[1], &claims); err != nil {
		return Claims{}, ErrInvalid
	}
	if claims.Issuer != Issuer || claims.Audience != Audience || claims.Status != "eligible" ||
		claims.SchemaVersion != SchemaVersion || claims.Subject == "" || claims.TokenID == "" ||
		claims.IssuedAt <= 0 || claims.ExpiresAt <= claims.IssuedAt ||
		time.Unix(claims.ExpiresAt, 0).After(time.Unix(claims.IssuedAt, 0).Add(MaxLifetime)) {
		return Claims{}, ErrInvalid
	}
	if !time.Unix(claims.ExpiresAt, 0).After(now) {
		return Claims{}, ErrExpired
	}
	return claims, nil
}

func decodePart(encoded string, destination any) error {
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, destination)
}

func mustPublicKey(encoded string) ed25519.PublicKey {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(raw) != ed25519.PublicKeySize {
		panic("invalid bundled self-host entitlement public key")
	}
	return ed25519.PublicKey(raw)
}
