package api

import (
	"crypto/rand"
	"fmt"
	"io"
	"net/http"

	mcpintegration "github.com/kannachi323/misty/server/internal/integrations/mcp"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func sdkBackendSecretAAD(userID, appID, connectionID string, revision int) []byte {
	return []byte(fmt.Sprintf("misty-sdk-backend-v1:%s:%s:%s:%d", userID, appID, connectionID, revision))
}
func (s *SpacesService) encryptSDKBackendBearer(userID, appID, connectionID string, revision int, bearer string) ([]byte, error) {
	if s.aead == nil {
		return nil, db.ErrSpaceInvalid
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return s.aead.Seal(nonce, nonce, []byte(bearer), sdkBackendSecretAAD(userID, appID, connectionID, revision)), nil
}
func (s *SpacesService) decryptSDKBackendBearer(connection db.SDKBackendConnection) (string, error) {
	if s.aead == nil || connection.KeyVersion != int(s.keyVer) || len(connection.BearerCiphertext) < s.aead.NonceSize()+s.aead.Overhead() {
		return "", db.ErrSDKProviderUnavailable
	}
	n := s.aead.NonceSize()
	data := connection.BearerCiphertext
	clear, err := s.aead.Open(nil, data[:n], data[n:], sdkBackendSecretAAD(connection.UserID, connection.AppID, connection.ID, connection.Revision))
	if err != nil {
		return "", db.ErrSDKProviderUnavailable
	}
	return string(clear), nil
}
func (s *SpacesService) TestingSetSDKBackendClientFactory(factory func(string, string) (*http.Client, error)) {
	s.sdkBackendClientFactory = factory
}

func (s *SpacesService) sdkBackendHTTPClient(connection db.SDKBackendConnection) (*http.Client, error) {
	bearer, err := s.decryptSDKBackendBearer(connection)
	if err != nil {
		return nil, err
	}
	if s.sdkBackendClientFactory != nil {
		return s.sdkBackendClientFactory(connection.EndpointURL, bearer)
	}
	return mcpintegration.NewHTTPClient(connection.EndpointURL, bearer, mcpintegration.Limits{MaxRequestBytes: 1 << 20, MaxResponseBytes: 1 << 20})
}
