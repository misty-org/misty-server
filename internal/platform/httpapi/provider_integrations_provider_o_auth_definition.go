package api

import (
	"crypto/rand"
	"io"
	"strings"

	envconfig "github.com/kannachi323/misty/server/internal/platform/config"


)

type providerOAuthDefinition struct {
	ID, Name, AuthorizeURL, TokenURL, ClientIDEnv, ClientSecretEnv string
	Scopes                                                         []string
	PKCE                                                           bool
}




func TestingProviderOAuthClientID(definition providerOAuthDefinition) string {
	return strings.TrimSpace(envconfig.Getenv(definition.ClientIDEnv))
}

func TestingProviderOAuthClientSecret(definition providerOAuthDefinition) string {
	return strings.TrimSpace(envconfig.Getenv(definition.ClientSecretEnv))
}

type providerTokenEnvelope struct {
	AccessToken   string                     `json:"access_token"`
	RefreshToken  string                     `json:"refresh_token,omitempty"`
	TokenType     string                     `json:"token_type,omitempty"`
	Scope         string                     `json:"scope,omitempty"`
	ExpiresIn     int                        `json:"expires_in,omitempty"`
	IDToken       string                     `json:"id_token,omitempty"`
	Team          *struct{ ID, Name string } `json:"team,omitempty"`
	WorkspaceID   string                     `json:"workspace_id,omitempty"`
	WorkspaceName string                     `json:"workspace_name,omitempty"`
}

func TestingValidProviderReturnPath(value string) bool {
	if value == "" {
		return true
	}
	return strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "//") && !strings.ContainsAny(value, "\\\r\n")
}



func (s *SpacesService) encryptProviderSecret(provider string, plaintext []byte) ([]byte, []byte, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, err
	}
	return s.aead.Seal(nil, nonce, plaintext, []byte("misty-provider-v2:"+provider)), nonce, nil
}

func (s *SpacesService) decryptProviderSecret(provider string, ciphertext, nonce []byte) ([]byte, error) {
	plaintext, err := s.aead.Open(nil, nonce, ciphertext, []byte("misty-provider-v2:"+provider))
	if err == nil || provider != "google" {
		return plaintext, err
	}
	// Existing Google Calendar credentials were sealed with the legacy provider
	// identifier. The migration changes only metadata, so retain a read path until
	// each credential is refreshed and sealed under the shared Google identity.
	return s.aead.Open(nil, nonce, ciphertext, []byte("misty-provider-v2:google_calendar"))
}
