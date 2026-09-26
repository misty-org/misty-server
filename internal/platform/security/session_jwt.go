package security

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
)

const AccessTokenTTL = 5 * time.Minute
const sessionIssuer = "misty-login"
const sessionAudience = "misty-api"

type SessionClaims struct {
	jwt.RegisteredClaims
	SessionID string `json:"sid"`
	Kind      string `json:"token_use"`
}

type SessionSigner struct {
	key   []byte
	keyID string
	keys  map[string][]byte
}

var developmentKey = sync.OnceValues(func() ([]byte, error) {
	key := make([]byte, 32)
	_, err := rand.Read(key)
	return key, err
})

// SessionSignerFromEnv requires a stable deployment secret in production.
// Development uses a process-local random key, so restart signs users out.
func SessionSignerFromEnv() (*SessionSigner, error) {
	raw := strings.TrimSpace(envconfig.Getenv("MISTY_AUTH_SIGNING_KEY"))
	var key []byte
	var err error
	if raw == "" {
		if strings.EqualFold(strings.TrimSpace(envconfig.Getenv("MISTY_ENVIRONMENT")), "production") {
			return nil, errors.New("MISTY_AUTH_SIGNING_KEY is required in production")
		}
		key, err = developmentKey()
	} else {
		key, err = decodeSessionKey(raw)
	}
	if err != nil {
		return nil, err
	}
	signer := &SessionSigner{key: key, keyID: sessionKeyID(key), keys: map[string][]byte{sessionKeyID(key): key}}
	if previous := strings.TrimSpace(envconfig.Getenv("MISTY_AUTH_SIGNING_KEY_PREVIOUS")); previous != "" {
		previousKey, err := decodeSessionKey(previous)
		if err != nil {
			return nil, err
		}
		signer.keys[sessionKeyID(previousKey)] = previousKey
	}
	return signer, nil
}

func decodeSessionKey(raw string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil || len(key) < 32 {
		return nil, errors.New("auth signing keys must be base64-encoded with at least 32 random bytes")
	}
	return key, nil
}

func sessionKeyID(key []byte) string { sum := sha256.Sum256(key); return hex.EncodeToString(sum[:8]) }

func (s *SessionSigner) Mint(userID, sessionID, kind string, expires time.Time) (string, error) {
	if userID == "" || sessionID == "" || (kind != "access" && kind != "refresh") || !expires.After(time.Now()) {
		return "", errors.New("invalid session claims")
	}
	id, err := GenerateSecureToken()
	if err != nil {
		return "", err
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, SessionClaims{
		RegisteredClaims: jwt.RegisteredClaims{Issuer: sessionIssuer, Subject: userID, Audience: jwt.ClaimStrings{sessionAudience},
			ExpiresAt: jwt.NewNumericDate(expires), IssuedAt: jwt.NewNumericDate(time.Now()), ID: id},
		SessionID: sessionID, Kind: kind,
	})
	token.Header["kid"] = s.keyID
	return token.SignedString(s.key)
}

func (s *SessionSigner) Verify(raw, kind string) (*SessionClaims, error) {
	if len(raw) > 4096 {
		return nil, errors.New("invalid session token")
	}
	claims := &SessionClaims{}
	token, err := jwt.ParseWithClaims(raw, claims, func(token *jwt.Token) (any, error) {
		kid, _ := token.Header["kid"].(string)
		key, ok := s.keys[kid]
		if !ok {
			return nil, errors.New("unknown signing key")
		}
		return key, nil
	}, jwt.WithValidMethods([]string{"HS256"}), jwt.WithIssuer(sessionIssuer), jwt.WithAudience(sessionAudience),
		jwt.WithExpirationRequired(), jwt.WithIssuedAt())
	if err != nil || !token.Valid {
		return nil, errors.New("invalid session token")
	}
	if claims.Kind != kind || claims.Subject == "" || claims.SessionID == "" || claims.ID == "" || claims.IssuedAt == nil {
		return nil, errors.New("invalid session claims")
	}
	return claims, nil
}
