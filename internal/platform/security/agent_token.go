package security

import (
	"crypto/hkdf"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Agent tokens prove which of a member's agents is calling the A2A endpoint.
//
// A token is minted only for a signed-in member and only for an agent that
// member owns. It names the account session that minted it, so it is useless
// without that session's cookie, and a session cookie alone cannot speak as
// an agent. Tokens are signed with a key derived from the account signing key
// under a separate label, so a session token can never verify as an agent
// token or the other way round, and rotating the account key rotates both.
const (
	AgentTokenTTL      = 5 * time.Minute
	agentTokenIssuer   = "misty-api"
	agentTokenAudience = "misty-a2a"
	agentTokenUse      = "agent"
	agentTokenKeyLabel = "misty agent token v1"
	agentTokenMaxSize  = 2048
)

var ErrInvalidAgentToken = errors.New("invalid agent token")

type AgentTokenClaims struct {
	jwt.RegisteredClaims
	AgentID     string `json:"agent_id"`
	SessionHash string `json:"sid_hash"`
	Kind        string `json:"token_use"`
}

type AgentTokenSigner struct {
	keyID string
	keys  map[string][]byte
}

// AgentTokenSigner derives the agent token keys from the account keys,
// keeping the previous key valid during rotation.
func (s *SessionSigner) AgentTokenSigner() (*AgentTokenSigner, error) {
	signer := &AgentTokenSigner{keys: map[string][]byte{}}
	for id, key := range s.keys {
		derived, err := hkdf.Key(sha256.New, key, nil, agentTokenKeyLabel, 32)
		if err != nil {
			return nil, err
		}
		derivedID := sessionKeyID(derived)
		signer.keys[derivedID] = derived
		if id == s.keyID {
			signer.keyID = derivedID
		}
	}
	if signer.keyID == "" {
		return nil, errors.New("agent token signing key unavailable")
	}
	return signer, nil
}

// Mint signs a token for one agent of userID, bound to the account session
// whose identifier is sessionID.
func (s *AgentTokenSigner) Mint(userID, agentID, sessionID string, now time.Time) (string, time.Time, error) {
	if userID == "" || agentID == "" || sessionID == "" {
		return "", time.Time{}, errors.New("invalid agent token claims")
	}
	id, err := GenerateSecureToken()
	if err != nil {
		return "", time.Time{}, err
	}
	expires := now.Add(AgentTokenTTL).Truncate(time.Second)
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, AgentTokenClaims{
		RegisteredClaims: jwt.RegisteredClaims{Issuer: agentTokenIssuer, Subject: userID, Audience: jwt.ClaimStrings{agentTokenAudience},
			ExpiresAt: jwt.NewNumericDate(expires), IssuedAt: jwt.NewNumericDate(now), NotBefore: jwt.NewNumericDate(now), ID: id},
		AgentID: agentID, SessionHash: HashToken(sessionID), Kind: agentTokenUse,
	})
	token.Header["kid"] = s.keyID
	signed, err := token.SignedString(s.keys[s.keyID])
	return signed, expires, err
}

// Verify checks the signature, lifetime and audience, and that the token
// belongs to userID's current session. The caller still confirms the member
// owns the agent, since that can change after the token was minted.
func (s *AgentTokenSigner) Verify(raw, userID, sessionID string) (*AgentTokenClaims, error) {
	if raw == "" || len(raw) > agentTokenMaxSize || userID == "" || sessionID == "" {
		return nil, ErrInvalidAgentToken
	}
	claims := &AgentTokenClaims{}
	token, err := jwt.ParseWithClaims(raw, claims, func(token *jwt.Token) (any, error) {
		kid, _ := token.Header["kid"].(string)
		key, ok := s.keys[kid]
		if !ok {
			return nil, errors.New("unknown signing key")
		}
		return key, nil
	}, jwt.WithValidMethods([]string{"HS256"}), jwt.WithIssuer(agentTokenIssuer), jwt.WithAudience(agentTokenAudience),
		jwt.WithExpirationRequired(), jwt.WithIssuedAt(), jwt.WithLeeway(5*time.Second))
	if err != nil || !token.Valid {
		return nil, ErrInvalidAgentToken
	}
	if claims.Kind != agentTokenUse || claims.AgentID == "" || claims.ID == "" || claims.IssuedAt == nil ||
		claims.ExpiresAt.Sub(claims.IssuedAt.Time) > AgentTokenTTL ||
		subtle.ConstantTimeCompare([]byte(claims.Subject), []byte(userID)) != 1 ||
		subtle.ConstantTimeCompare([]byte(claims.SessionHash), []byte(HashToken(sessionID))) != 1 {
		return nil, ErrInvalidAgentToken
	}
	return claims, nil
}
