package api

import (
	"crypto/rand"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	cap "github.com/kannachi323/misty/server/internal/capabilities"
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
func (s *SpacesService) SDKBackendConnectionControl() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := trustedSDKUser(w, r, s.database)
		if !ok {
			return
		}
		appID, connectionID := chi.URLParam(r, "appID"), chi.URLParam(r, "connectionID")
		if r.Method == http.MethodDelete {
			if err := s.database.RevokeSDKBackendConnection(r.Context(), userID, appID, connectionID); err != nil {
				writeSDKError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if !sdkProviderAdmissionEnabled(w) {
			return
		}
		var body struct {
			ExpectedRevision int    `json:"expectedRevision"`
			EndpointURL      string `json:"endpointURL"`
			BearerToken      string `json:"bearerToken"`
		}
		if !decodeCapabilityRequest(w, r, &body) {
			return
		}
		if !cap.ValidID(connectionID) || body.ExpectedRevision < 0 || body.ExpectedRevision >= 2147483647 || len(body.EndpointURL) > 2048 || mcpintegration.ValidateEndpointURL(body.EndpointURL) != nil || body.BearerToken == "" || len(body.BearerToken) > 8192 || strings.ContainsAny(body.BearerToken, "\r\n\t ") {
			writeSDKError(w, cap.ErrInvalid)
			return
		}
		encrypted, err := s.encryptSDKBackendBearer(userID, appID, connectionID, body.ExpectedRevision+1, body.BearerToken)
		if err != nil {
			writeSDKError(w, err)
			return
		}
		revision, err := s.database.ConfigureSDKBackendConnection(r.Context(), userID, db.SDKBackendConnection{UserID: userID, AppID: appID, ID: connectionID, EndpointURL: body.EndpointURL, BearerCiphertext: encrypted, KeyVersion: int(s.keyVer)}, body.ExpectedRevision)
		if err != nil {
			writeSDKError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, map[string]any{"connectionId": connectionID, "revision": revision})
	}
}
func ConfigureSDKTarget(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := trustedSDKUser(w, r, database)
		if !ok {
			return
		}
		if !sdkProviderAdmissionEnabled(w) {
			return
		}
		var body cap.TargetConfiguration
		if !decodeCapabilityRequest(w, r, &body) {
			return
		}
		target, err := database.ConfigureSDKTarget(r.Context(), userID, body)
		if err != nil {
			writeSDKError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, map[string]any{"target": target})
	}
}
func SDKTargetControlList(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := trustedSDKUser(w, r, database)
		if !ok {
			return
		}
		limit := 50
		if raw := r.URL.Query().Get("limit"); raw != "" {
			var err error
			limit, err = strconv.Atoi(raw)
			if err != nil {
				writeSDKError(w, cap.ErrInvalid)
				return
			}
		}
		page, err := database.SDKTargetsForControl(r.Context(), userID, r.URL.Query().Get("cursor"), limit)
		if err != nil {
			writeSDKError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, page)
	}
}

func RevokeSDKTarget(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := trustedSDKUser(w, r, database)
		if !ok {
			return
		}
		if err := database.RevokeSDKTarget(r.Context(), userID, chi.URLParam(r, "targetID")); err != nil {
			writeSDKError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
func ResolveSDKTargets(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, database)
		if !ok {
			return
		}
		var body cap.TargetResolve
		if !decodeCapabilityRequest(w, r, &body) {
			return
		}
		targets, err := database.ResolveSDKTargets(r.Context(), userID, body)
		if err != nil {
			writeSDKError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"targets": targets})
	}
}

// The transport never receives raw host credentials through a model or provider
// hint. The persisted connection revision is the only source of its endpoint.
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
