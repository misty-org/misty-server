package security

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const IntegrationTokenTTL = 5 * time.Minute
const integrationAudience = "misty-integration"
const integrationKind = "integration"

type IntegrationClaims struct {
	Subject   string
	SpaceID   string
	Access    []string
	KeyPrefix string
	Name      string
	ExpiresAt time.Time
}

type integrationTokenClaims struct {
	jwt.RegisteredClaims
	SpaceID   string   `json:"space"`
	Access    []string `json:"access"`
	KeyPrefix string   `json:"key_prefix"`
	Name      string   `json:"name"`
	Kind      string   `json:"token_use"`
}

func (s *SessionSigner) MintIntegration(claims IntegrationClaims, expires time.Time) (string, error) {
	if claims.Subject == "" || claims.SpaceID == "" || len(claims.Access) == 0 || !expires.After(time.Now()) {
		return "", errors.New("invalid integration claims")
	}
	id, err := GenerateSecureToken()
	if err != nil {
		return "", err
	}
	now := time.Now()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, integrationTokenClaims{
		RegisteredClaims: jwt.RegisteredClaims{Issuer: sessionIssuer, Subject: claims.Subject,
			Audience: jwt.ClaimStrings{integrationAudience}, ExpiresAt: jwt.NewNumericDate(expires),
			IssuedAt: jwt.NewNumericDate(now), ID: id},
		SpaceID: claims.SpaceID, Access: append([]string(nil), claims.Access...), KeyPrefix: claims.KeyPrefix,
		Name: claims.Name, Kind: integrationKind,
	})
	token.Header["kid"] = s.keyID
	return token.SignedString(s.key)
}

func (s *SessionSigner) VerifyIntegration(raw string) (IntegrationClaims, error) {
	if len(raw) > 4096 {
		return IntegrationClaims{}, errors.New("invalid integration token")
	}
	claims := &integrationTokenClaims{}
	token, err := jwt.ParseWithClaims(raw, claims, func(token *jwt.Token) (any, error) {
		kid, _ := token.Header["kid"].(string)
		key, ok := s.keys[kid]
		if !ok {
			return nil, errors.New("unknown signing key")
		}
		return key, nil
	}, jwt.WithValidMethods([]string{"HS256"}), jwt.WithIssuer(sessionIssuer), jwt.WithAudience(integrationAudience),
		jwt.WithExpirationRequired(), jwt.WithIssuedAt())
	if err != nil || !token.Valid {
		return IntegrationClaims{}, errors.New("invalid integration token")
	}
	if claims.Kind != integrationKind || claims.Subject == "" || claims.SpaceID == "" || len(claims.Access) == 0 ||
		claims.ID == "" || claims.IssuedAt == nil || claims.ExpiresAt == nil {
		return IntegrationClaims{}, errors.New("invalid integration claims")
	}
	return IntegrationClaims{Subject: claims.Subject, SpaceID: claims.SpaceID, Access: claims.Access,
		KeyPrefix: claims.KeyPrefix, Name: claims.Name, ExpiresAt: claims.ExpiresAt.Time}, nil
}
