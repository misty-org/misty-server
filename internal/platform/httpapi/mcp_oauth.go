package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"golang.org/x/oauth2"
)

const mcpOAuthSecretAAD = "misty-mcp-oauth-v1"

type mcpOAuthEnvelope struct {
	Provider         string    `json:"provider"`
	EndpointURL      string    `json:"endpoint_url"`
	ResourceURL      string    `json:"resource_url"`
	Issuer           string    `json:"issuer"`
	AuthorizationURL string    `json:"authorization_url"`
	TokenURL         string    `json:"token_url"`
	ClientID         string    `json:"client_id"`
	ClientSecret     string    `json:"client_secret,omitempty"`
	TokenAuthMethod  string    `json:"token_auth_method,omitempty"`
	Scopes           []string  `json:"scopes,omitempty"`
	Verifier         string    `json:"verifier,omitempty"`
	RequireIssuer    bool      `json:"require_issuer,omitempty"`
	AccessToken      string    `json:"access_token,omitempty"`
	RefreshToken     string    `json:"refresh_token,omitempty"`
	TokenType        string    `json:"token_type,omitempty"`
	Expiry           time.Time `json:"expiry,omitempty"`
}

func (s *SpacesService) BeginActivepiecesAuthorization() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		endpoint, err := configuredActivepiecesMCPURL()
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "activepieces_not_configured"})
			return
		}

		callback := requestPublicAPIBase(r) + "/oauth/mcp/activepieces/callback"
		envelope, err := discoverMCPOAuth(r.Context(), endpoint, callback)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"code": "mcp_oauth_discovery_failed"})
			return
		}
		envelope.Provider = "activepieces"
		envelope.Verifier = oauth2.GenerateVerifier()
		state := randomProviderValue(32)
		encoded, _ := json.Marshal(envelope)
		ciphertext, nonce, err := s.encryptMCPOAuthSecret(encoded)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		expires := time.Now().UTC().Add(10 * time.Minute)
		if err := s.database.CreateMCPOAuthState(r.Context(), hashProviderValue(state), db.MCPOAuthState{OwnerUserID: userID, SecretCiphertext: ciphertext, SecretNonce: nonce, ExpiresAt: expires}); err != nil {
			writeSpaceError(w, err)
			return
		}

		config := envelope.oauthConfig(callback)
		options := []oauth2.AuthCodeOption{
			oauth2.S256ChallengeOption(envelope.Verifier),
			oauth2.SetAuthURLParam("resource", envelope.ResourceURL),
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"authorization_url": config.AuthCodeURL(state, options...),
			"state_expires_at":  expires,
		})
	}
}

func configuredActivepiecesMCPURL() (string, error) {
	endpoint := strings.TrimSpace(envconfig.Getenv("MISTY_ACTIVEPIECES_MCP_URL"))
	if !validMCPHTTPSURL(endpoint) {
		return "", errors.New("MISTY_ACTIVEPIECES_MCP_URL must be a complete HTTPS URL")
	}
	return endpoint, nil
}

func TestingConfiguredActivepiecesMCPURL() (string, error) {
	return configuredActivepiecesMCPURL()
}

func (s *SpacesService) ActivepiecesAuthorizationCallback() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if refusal := r.URL.Query().Get("error"); refusal != "" {
			TestingWriteOAuthCallbackFailure(w, "Activepieces", TestingOAuthDeniedByProvider, errors.New(TestingProviderRefusalDetail(r)))
			return
		}
		code, state := r.URL.Query().Get("code"), r.URL.Query().Get("state")
		if code == "" || state == "" {
			TestingWriteOAuthCallbackFailure(w, "Activepieces", TestingOAuthMalformedRedirect, errors.New("authorization code or state is missing"))
			return
		}
		stored, err := s.database.ConsumeMCPOAuthState(r.Context(), hashProviderValue(state))
		if err != nil {
			TestingWriteOAuthCallbackFailure(w, "Activepieces", TestingOAuthStaleState, err)
			return
		}
		raw, err := s.decryptMCPOAuthSecret(stored.SecretCiphertext, stored.SecretNonce)
		if err != nil {
			TestingWriteOAuthCallbackFailure(w, "Activepieces", TestingOAuthStaleState, err)
			return
		}
		var envelope mcpOAuthEnvelope
		if json.Unmarshal(raw, &envelope) != nil || envelope.Provider != "activepieces" {
			TestingWriteOAuthCallbackFailure(w, "Activepieces", TestingOAuthStaleState, errors.New("authorization state is invalid"))
			return
		}
		if envelope.RequireIssuer {
			issuer := strings.TrimSuffix(r.URL.Query().Get("iss"), "/")
			if issuer == "" || issuer != strings.TrimSuffix(envelope.Issuer, "/") {
				TestingWriteOAuthCallbackFailure(w, "Activepieces", TestingOAuthMalformedRedirect, errors.New("authorization issuer did not match"))
				return
			}
		}
		callback := TestingActivepiecesCallbackURL(r)
		token, err := exchangeMCPOAuthCode(r.Context(), envelope, callback, code)
		if err != nil || token.AccessToken == "" {
			TestingWriteOAuthCallbackFailure(w, "Activepieces", TestingOAuthExchangeFailed, err)
			return
		}
		envelope.Verifier = ""
		envelope.AccessToken = token.AccessToken
		envelope.RefreshToken = token.RefreshToken
		envelope.TokenType = token.TokenType
		envelope.Expiry = token.Expiry
		bearerCiphertext, bearerNonce, err := s.encryptMCPBearer(token.AccessToken)
		if err != nil {
			TestingWriteOAuthCallbackFailure(w, "Activepieces", TestingOAuthNotSaved, err)
			return
		}
		connection, err := s.database.CreateMCPRemoteConnection(r.Context(), db.MCPRemoteConnection{
			OwnerUserID: stored.OwnerUserID, Name: "Activepieces", Provider: "activepieces",
			EndpointURL: envelope.EndpointURL, BearerCiphertext: bearerCiphertext,
			BearerNonce: bearerNonce, KeyVersion: int(s.keyVer),
		})
		if err != nil {
			TestingWriteOAuthCallbackFailure(w, "Activepieces", TestingOAuthNotSaved, err)
			return
		}
		credentialRaw, _ := json.Marshal(envelope)
		credentialCiphertext, credentialNonce, err := s.encryptMCPOAuthSecret(credentialRaw)
		if err != nil {
			TestingWriteOAuthCallbackFailure(w, "Activepieces", TestingOAuthNotSaved, err)
			return
		}
		var expiresAt *time.Time
		if !token.Expiry.IsZero() {
			value := token.Expiry.UTC()
			expiresAt = &value
		}
		if err := s.database.SaveMCPOAuthCredential(r.Context(), db.MCPOAuthCredential{ConnectionID: connection.ID, OwnerUserID: stored.OwnerUserID, CredentialCiphertext: credentialCiphertext, CredentialNonce: credentialNonce, KeyVersion: s.keyVer, ExpiresAt: expiresAt}); err != nil {
			TestingWriteOAuthCallbackFailure(w, "Activepieces", TestingOAuthNotSaved, err)
			return
		}

		if discovery, discoverErr := s.mcpConnectorClient.Discover(r.Context(), connection.EndpointURL, token.AccessToken); discoverErr == nil {
			tools := normalizeMCPDiscoveryForProvider(connection.ID, connection.Provider, discovery.Tools)
			_, _ = s.database.SaveMCPDiscovery(r.Context(), stored.OwnerUserID, db.MCPDiscoverySnapshot{ConnectionID: connection.ID, ProtocolVersion: discovery.ProtocolVersion, ServerName: discovery.ServerName, ServerVersion: discovery.ServerVersion, CatalogFingerprint: mcpCatalogFingerprint(tools), ToolCount: len(tools), Status: "complete"}, tools)
			_, _ = s.database.SetMCPConnectionHealth(r.Context(), stored.OwnerUserID, connection.ID, "active", "", true)
		} else {
			_, _ = s.database.SetMCPConnectionHealth(r.Context(), stored.OwnerUserID, connection.ID, "needs_attention", mcpErrorCode(discoverErr), false)
		}
		TestingWriteProviderCompletionPage(w, "Activepieces", "your automation workspace")
	}
}

func (s *SpacesService) ActivepiecesFlows() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		endpoint, bearer, managed, err := s.activepiecesAutomationAccess(r.Context(), userID)
		if managed {
			if err != nil {
				writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "activepieces_not_ready"})
				return
			}
			result, callErr := s.mcpConnectorClient.CallTool(r.Context(), endpoint, bearer, "ap_list_flows", json.RawMessage(`{"limit":100}`))
			if callErr != nil {
				writeJSON(w, http.StatusBadGateway, map[string]string{"code": mcpErrorCode(callErr)})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"connected": true, "managed": true, "structured_content": result.StructuredContent, "text": result.Text})
			return
		}

		connections, err := s.database.MCPRemoteConnections(r.Context(), userID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		var connection *db.MCPRemoteConnection
		for index := range connections {
			if connections[index].Provider == "activepieces" {
				connection = &connections[index]
				break
			}
		}
		if connection == nil {
			writeJSON(w, http.StatusOK, map[string]any{"connected": false, "flows": []any{}})
			return
		}
		item, bearer, err := s.mcpConnectionAccess(r, userID, connection.ID)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"code": "mcp_oauth_refresh_failed"})
			return
		}
		result, err := s.mcpConnectorClient.CallTool(r.Context(), item.EndpointURL, bearer, "ap_list_flows", json.RawMessage(`{"limit":100}`))
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"code": mcpErrorCode(err)})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"connected": true, "connection": item, "structured_content": result.StructuredContent, "text": result.Text})
	}
}

// ActivepiecesTool gives Misty's first-party automation editor a narrow,
// authenticated bridge to the same project-scoped MCP tools used by agents.
// Credentials remain server-side and the existing provider allowlist is the
// single source of truth for which operations the UI may invoke.
func (s *SpacesService) ActivepiecesTool() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		toolName := strings.TrimSpace(chi.URLParam(r, "toolName"))
		if !allowedActivepiecesMCPTool(toolName) {
			writeJSON(w, http.StatusNotFound, map[string]string{"code": "automation_tool_not_found"})
			return
		}
		var body struct {
			Arguments json.RawMessage `json:"arguments"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		if len(body.Arguments) == 0 {
			body.Arguments = json.RawMessage(`{}`)
		}
		var arguments map[string]any
		if json.Unmarshal(body.Arguments, &arguments) != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "automation_invalid_arguments"})
			return
		}

		endpoint, bearer, managed, managedErr := s.activepiecesAutomationAccess(r.Context(), userID)
		if managed {
			if managedErr != nil {
				writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "activepieces_not_ready"})
				return
			}
			result, callErr := s.mcpConnectorClient.CallTool(r.Context(), endpoint, bearer, toolName, body.Arguments)
			if callErr != nil {
				writeJSON(w, http.StatusBadGateway, map[string]string{"code": mcpErrorCode(callErr)})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"structured_content": result.StructuredContent, "text": result.Text})
			return
		}

		connections, err := s.database.MCPRemoteConnections(r.Context(), userID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		var connectionID string
		for _, connection := range connections {
			if connection.Provider == "activepieces" {
				connectionID = connection.ID
				break
			}
		}
		if connectionID == "" {
			writeJSON(w, http.StatusConflict, map[string]string{"code": "activepieces_not_connected"})
			return
		}
		item, bearer, err := s.mcpConnectionAccess(r, userID, connectionID)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"code": "mcp_oauth_refresh_failed"})
			return
		}
		result, err := s.mcpConnectorClient.CallTool(r.Context(), item.EndpointURL, bearer, toolName, body.Arguments)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"code": mcpErrorCode(err)})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"structured_content": result.StructuredContent,
			"text":               result.Text,
		})
	}
}

func (s *SpacesService) activepiecesAutomationAccess(ctx context.Context, userID string) (string, string, bool, error) {
	if s.managedActivepieces == nil {
		return "", "", false, nil
	}
	endpoint, bearer, err := s.managedActivepieces.Access(ctx, userID)
	return endpoint, bearer, true, err
}
