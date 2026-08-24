package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

type providerAPIError struct {
	Status     int
	BodyDigest string
}

func (e *providerAPIError) Error() string {
	return fmt.Sprintf("provider request returned %d (body %s)", e.Status, e.BodyDigest)
}

func (s *SpacesService) ProviderSharedResource() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		if err := s.database.DisableProviderSharedResource(r.Context(), userID, chi.URLParam(r, "spaceID"), chi.URLParam(r, "resourceID")); err != nil {
			writeSpaceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *SpacesService) discoverProviderResources(ctx context.Context, userID, spaceID, integrationID string) ([]availableProviderResource, error) {
	return nil, db.ErrSpaceInvalid
}

func providerJSONRequest(ctx context.Context, token, tokenType, method, endpoint string, body any, headers map[string]string) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		encoded, _ := json.Marshal(body)
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return nil, err
	}
	if tokenType == "" {
		tokenType = "Bearer"
	}
	if strings.TrimSpace(token) != "" {
		request.Header.Set("Authorization", tokenType+" "+token)
	}
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 4<<20+1))
	if err != nil {
		return nil, err
	}
	if len(payload) > 4<<20 {
		return nil, errors.New("provider response exceeded 4 MiB")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		digest := sha256.Sum256(payload)
		return payload, &providerAPIError{Status: response.StatusCode, BodyDigest: hex.EncodeToString(digest[:6])}
	}
	return payload, nil
}
