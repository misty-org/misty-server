package api

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	mcpintegration "github.com/kannachi323/misty/server/internal/integrations/mcp"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"golang.org/x/oauth2"
)

func TestingActivepiecesCallbackURL(r *http.Request) string {
	return requestPublicAPIBase(r) + "/oauth/mcp/activepieces/callback"
}

func discoverMCPOAuth(ctx context.Context, endpoint, callback string) (mcpOAuthEnvelope, error) {
	resource, err := url.Parse(endpoint)
	if err != nil {
		return mcpOAuthEnvelope{}, err
	}
	rootResource := *resource
	rootResource.Path, rootResource.RawPath = "", ""
	pathMetadata := *resource
	pathMetadata.Path = "/.well-known/oauth-protected-resource/" + strings.TrimLeft(resource.Path, "/")
	rootMetadata := *resource
	rootMetadata.Path = "/.well-known/oauth-protected-resource"

	var protected *oauthex.ProtectedResourceMetadata
	for _, candidate := range []struct{ metadata, resource string }{{pathMetadata.String(), endpoint}, {rootMetadata.String(), rootResource.String()}} {
		client, clientErr := mcpOAuthHTTPClient(candidate.metadata)
		if clientErr != nil {
			continue
		}
		protected, err = oauthex.GetProtectedResourceMetadata(ctx, candidate.metadata, candidate.resource, client)
		client.CloseIdleConnections()
		if err == nil && protected != nil && len(protected.AuthorizationServers) > 0 {
			break
		}
		protected = nil
	}
	if protected == nil {
		protected = &oauthex.ProtectedResourceMetadata{Resource: endpoint, AuthorizationServers: []string{rootResource.String()}}
	}
	issuer := protected.AuthorizationServers[0]
	metadata, err := discoverMCPAuthServer(ctx, issuer)
	if err != nil {
		return mcpOAuthEnvelope{}, err
	}
	if metadata == nil {
		base := strings.TrimSuffix(issuer, "/")
		metadata = &oauthex.AuthServerMeta{Issuer: issuer, AuthorizationEndpoint: base + "/authorize", TokenEndpoint: base + "/token", RegistrationEndpoint: base + "/register"}
	}
	if metadata.RegistrationEndpoint == "" {
		return mcpOAuthEnvelope{}, errors.New("authorization server does not support dynamic client registration")
	}
	registrationClient, err := mcpOAuthHTTPClient(metadata.RegistrationEndpoint)
	if err != nil {
		return mcpOAuthEnvelope{}, err
	}
	registration, err := oauthex.RegisterClient(ctx, metadata.RegistrationEndpoint, &oauthex.ClientRegistrationMetadata{
		RedirectURIs: []string{callback}, GrantTypes: []string{"authorization_code", "refresh_token"},
		ResponseTypes: []string{"code"}, ClientName: "Misty Automations",
	}, registrationClient)
	registrationClient.CloseIdleConnections()
	if err != nil {
		return mcpOAuthEnvelope{}, err
	}
	scopes := append([]string(nil), protected.ScopesSupported...)
	if slices.Contains(metadata.ScopesSupported, "offline_access") && !slices.Contains(scopes, "offline_access") {
		scopes = append(scopes, "offline_access")
	}
	method := registration.TokenEndpointAuthMethod
	if method == "" {
		method = "client_secret_basic"
	}
	return mcpOAuthEnvelope{
		EndpointURL: endpoint, ResourceURL: protected.Resource, Issuer: metadata.Issuer,
		AuthorizationURL: metadata.AuthorizationEndpoint, TokenURL: metadata.TokenEndpoint,
		ClientID: registration.ClientID, ClientSecret: registration.ClientSecret,
		TokenAuthMethod: method, Scopes: scopes,
		RequireIssuer: metadata.AuthorizationResponseIssParameterSupported,
	}, nil
}

func discoverMCPAuthServer(ctx context.Context, issuer string) (*oauthex.AuthServerMeta, error) {
	parsed, err := url.Parse(issuer)
	if err != nil {
		return nil, err
	}
	paths := []string{}
	if parsed.Path == "" || parsed.Path == "/" {
		paths = []string{"/.well-known/oauth-authorization-server", "/.well-known/openid-configuration"}
	} else {
		path := strings.Trim(parsed.Path, "/")
		paths = []string{"/.well-known/oauth-authorization-server/" + path, "/.well-known/openid-configuration/" + path, "/" + path + "/.well-known/openid-configuration"}
	}
	for _, path := range paths {
		candidate := *parsed
		candidate.Path, candidate.RawPath = path, ""
		client, clientErr := mcpOAuthHTTPClient(candidate.String())
		if clientErr != nil {
			return nil, clientErr
		}
		metadata, metadataErr := oauthex.GetAuthServerMeta(ctx, candidate.String(), issuer, client)
		client.CloseIdleConnections()
		if metadataErr != nil {
			return nil, metadataErr
		}
		if metadata != nil {
			return metadata, nil
		}
	}
	return nil, nil
}

func mcpOAuthHTTPClient(endpoint string) (*http.Client, error) {
	return mcpintegration.NewHTTPClient(endpoint, "", mcpintegration.DefaultLimits())
}

func (envelope mcpOAuthEnvelope) oauthConfig(redirect string) *oauth2.Config {
	return &oauth2.Config{
		ClientID: envelope.ClientID, ClientSecret: envelope.ClientSecret, RedirectURL: redirect,
		Scopes:   envelope.Scopes,
		Endpoint: oauth2.Endpoint{AuthURL: envelope.AuthorizationURL, TokenURL: envelope.TokenURL, AuthStyle: mcpOAuthAuthStyle(envelope.TokenAuthMethod)},
	}
}

func mcpOAuthAuthStyle(method string) oauth2.AuthStyle {
	switch method {
	case "client_secret_post", "none":
		return oauth2.AuthStyleInParams
	case "client_secret_basic", "":
		return oauth2.AuthStyleInHeader
	default:
		return oauth2.AuthStyleAutoDetect
	}
}

func TestingMCPOAuthAuthStyle(method string) oauth2.AuthStyle {
	return mcpOAuthAuthStyle(method)
}

func exchangeMCPOAuthCode(ctx context.Context, envelope mcpOAuthEnvelope, redirect, code string) (*oauth2.Token, error) {
	client, err := mcpOAuthHTTPClient(envelope.TokenURL)
	if err != nil {
		return nil, err
	}
	defer client.CloseIdleConnections()
	ctx = context.WithValue(ctx, oauth2.HTTPClient, client)
	return envelope.oauthConfig(redirect).Exchange(ctx, code, oauth2.VerifierOption(envelope.Verifier), oauth2.SetAuthURLParam("resource", envelope.ResourceURL))
}

func (s *SpacesService) activepiecesAccessToken(ctx context.Context, userID string, connection *db.MCPRemoteConnection) (string, error) {
	credential, err := s.database.MCPOAuthCredential(ctx, userID, connection.ID)
	if err != nil {
		return "", err
	}
	raw, err := s.decryptMCPOAuthSecret(credential.CredentialCiphertext, credential.CredentialNonce)
	if err != nil {
		return "", err
	}
	var envelope mcpOAuthEnvelope
	if json.Unmarshal(raw, &envelope) != nil || envelope.Provider != "activepieces" || envelope.AccessToken == "" {
		return "", errors.New("activepieces OAuth credential is invalid")
	}
	if envelope.Expiry.IsZero() || time.Until(envelope.Expiry) > 90*time.Second {
		return envelope.AccessToken, nil
	}
	if envelope.RefreshToken == "" {
		return "", errors.New("activepieces authorization expired; reconnect it")
	}
	client, err := mcpOAuthHTTPClient(envelope.TokenURL)
	if err != nil {
		return "", err
	}
	defer client.CloseIdleConnections()
	refreshContext := context.WithValue(ctx, oauth2.HTTPClient, client)
	previous := &oauth2.Token{AccessToken: envelope.AccessToken, RefreshToken: envelope.RefreshToken, TokenType: envelope.TokenType, Expiry: envelope.Expiry}
	next, err := envelope.oauthConfig("").TokenSource(refreshContext, previous).Token()
	if err != nil || next.AccessToken == "" {
		return "", errors.New("activepieces OAuth token could not be refreshed")
	}
	if next.RefreshToken == "" {
		next.RefreshToken = envelope.RefreshToken
	}
	envelope.AccessToken, envelope.RefreshToken = next.AccessToken, next.RefreshToken
	envelope.TokenType, envelope.Expiry = next.TokenType, next.Expiry
	encoded, _ := json.Marshal(envelope)
	ciphertext, nonce, err := s.encryptMCPOAuthSecret(encoded)
	if err != nil {
		return "", err
	}
	bearerCiphertext, bearerNonce, err := s.encryptMCPBearer(next.AccessToken)
	if err != nil {
		return "", err
	}
	var expiresAt *time.Time
	if !next.Expiry.IsZero() {
		value := next.Expiry.UTC()
		expiresAt = &value
	}
	err = s.database.UpdateMCPOAuthToken(ctx, db.MCPOAuthCredential{ConnectionID: connection.ID, OwnerUserID: userID, CredentialCiphertext: ciphertext, CredentialNonce: nonce, KeyVersion: s.keyVer, ExpiresAt: expiresAt}, bearerCiphertext, bearerNonce)
	if err != nil {
		return "", err
	}
	return next.AccessToken, nil
}

func (s *SpacesService) encryptMCPOAuthSecret(value []byte) ([]byte, []byte, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, err
	}
	return s.aead.Seal(nil, nonce, value, []byte(mcpOAuthSecretAAD)), nonce, nil
}

func (s *SpacesService) decryptMCPOAuthSecret(ciphertext, nonce []byte) ([]byte, error) {
	return s.aead.Open(nil, nonce, ciphertext, []byte(mcpOAuthSecretAAD))
}
