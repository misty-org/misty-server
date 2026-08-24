package api

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

const (
	mcpAccessAudience = "misty-mcp"
	mcpAccessIssuer   = "misty-control-plane"
	mcpAccessTTL      = 5 * time.Minute
	mcpAccessMaxSize  = 16 << 10
)

var errMCPAccessDenied = errors.New("MCP access denied")

type mcpAccessClaims struct {
	Version      int    `json:"v"`
	Issuer       string `json:"iss"`
	Audience     string `json:"aud"`
	Subject      string `json:"sub"`
	RunID        string `json:"run_id"`
	RuntimeRunID string `json:"runtime_run_id"`
	TokenID      string `json:"jti"`
	IssuedAt     int64  `json:"iat"`
	ExpiresAt    int64  `json:"exp"`
}

func (s *SpacesService) AgentRuntimeMCPAccess() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body agentRuntimeIdentity
		if !readAgentRuntimeRequest(s.agentRuntime, w, r, &body) {
			return
		}
		if !s.agentRuntime.Enabled() {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "agent_runtime_unavailable"})
			return
		}
		runID := chi.URLParam(r, "runID")
		subject := ""
		if isAIInvocationRuntimeID(runID) {
			record, err := s.database.ValidateAIInvocationRuntime(r.Context(), runID, body.RuntimeRunID)
			if err != nil {
				writeAgentError(w, err)
				return
			}
			subject = record.UserID
		} else {
			run, _, err := s.database.ValidatePersonalAgentTaskRuntime(r.Context(), runID, body.RuntimeRunID)
			if err != nil {
				writeAgentError(w, err)
				return
			}
			subject = run.OwnerUserID
		}
		now := time.Now().UTC()
		claims := mcpAccessClaims{
			Version: 1, Issuer: mcpAccessIssuer, Audience: mcpAccessAudience,
			Subject: subject, RunID: runID, RuntimeRunID: body.RuntimeRunID,
			TokenID: randomMCPTokenID(), IssuedAt: now.Unix(), ExpiresAt: now.Add(mcpAccessTTL).Unix(),
		}
		token, err := signMCPAccessToken(s.agentRuntime.secret, claims)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "mcp_token_failed"})
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, map[string]any{
			"access_token": token,
			"token_type":   "Bearer",
			"expires_in":   int(mcpAccessTTL.Seconds()),
			"mcp_path":     "/mcp",
			"protocol":     "2026-07-28",
		})
	}
}

func randomMCPTokenID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(value[:])
}

func signMCPAccessToken(secret []byte, claims mcpAccessClaims) (string, error) {
	if len(secret) < 32 || claims.TokenID == "" {
		return "", errMCPAccessDenied
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte("v1." + encoded))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return "v1." + encoded + "." + signature, nil
}

func verifyMCPAccessToken(token string, secrets ...[]byte) (mcpAccessClaims, error) {
	var claims mcpAccessClaims
	token = strings.TrimSpace(token)
	if token == "" || len(token) > mcpAccessMaxSize {
		return claims, errMCPAccessDenied
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != "v1" {
		return claims, errMCPAccessDenied
	}
	signed := parts[0] + "." + parts[1]
	provided, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(provided) != sha256.Size {
		return claims, errMCPAccessDenied
	}
	valid := false
	for _, secret := range secrets {
		if len(secret) < 32 {
			continue
		}
		mac := hmac.New(sha256.New, secret)
		_, _ = mac.Write([]byte(signed))
		valid = hmac.Equal(provided, mac.Sum(nil)) || valid
	}
	if !valid {
		return claims, errMCPAccessDenied
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || json.Unmarshal(payload, &claims) != nil {
		return mcpAccessClaims{}, errMCPAccessDenied
	}
	now := time.Now().UTC().Unix()
	if claims.Version != 1 || claims.Issuer != mcpAccessIssuer || claims.Audience != mcpAccessAudience ||
		claims.Subject == "" || claims.RunID == "" || claims.RuntimeRunID == "" || claims.TokenID == "" ||
		claims.IssuedAt > now+30 || claims.ExpiresAt <= now || claims.ExpiresAt-claims.IssuedAt > int64(mcpAccessTTL/time.Second)+30 {
		return mcpAccessClaims{}, errMCPAccessDenied
	}
	return claims, nil
}

func TestingSignMCPAccessToken(secret []byte, subject, runID, runtimeRunID, tokenID, audience string, issuedAt, expiresAt time.Time) (string, error) {
	if strings.TrimSpace(audience) == "" {
		audience = mcpAccessAudience
	}
	return signMCPAccessToken(secret, mcpAccessClaims{
		Version: 1, Issuer: mcpAccessIssuer, Audience: audience,
		Subject: subject, RunID: runID, RuntimeRunID: runtimeRunID, TokenID: tokenID,
		IssuedAt: issuedAt.UTC().Unix(), ExpiresAt: expiresAt.UTC().Unix(),
	})
}

func TestingVerifyMCPAccessToken(token string, secrets ...[]byte) (subject, runID, runtimeRunID, tokenID string, err error) {
	claims, err := verifyMCPAccessToken(token, secrets...)
	return claims.Subject, claims.RunID, claims.RuntimeRunID, claims.TokenID, err
}
