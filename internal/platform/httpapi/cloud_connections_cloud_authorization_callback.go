package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func refreshCloudToken(ctx context.Context, definition cloudOAuthDefinition, secret cloudOAuthSecret) (providerTokenEnvelope, error) {
	if secret.Token.RefreshToken == "" {
		return providerTokenEnvelope{}, errors.New("refresh token is missing")
	}
	values := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {secret.Token.RefreshToken},
		"client_id": {secret.ClientID}, "client_secret": {secret.ClientSecret}}
	token, err := requestCloudToken(ctx, definition.TokenURL, values)
	if token.RefreshToken == "" {
		token.RefreshToken = secret.Token.RefreshToken
	}
	return token, err
}

func requestCloudToken(ctx context.Context, endpoint string, values url.Values) (providerTokenEnvelope, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return providerTokenEnvelope{}, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	response, err := connectedAccountHTTPClient(20 * time.Second).Do(request)
	if err != nil {
		return providerTokenEnvelope{}, err
	}
	defer response.Body.Close()
	raw, err := readConnectedAccountResponse(response.Body)
	if err != nil {
		return providerTokenEnvelope{}, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return providerTokenEnvelope{}, fmt.Errorf("cloud token endpoint returned %s", response.Status)
	}
	var token providerTokenEnvelope
	if err := json.Unmarshal(raw, &token); err != nil || token.AccessToken == "" {
		return providerTokenEnvelope{}, errors.New("cloud token response is invalid")
	}
	return token, nil
}

func TestingCloudCallbackURL(r *http.Request, provider string) string {
	return requestPublicAPIBase(r) + "/oauth/cloud/" + url.PathEscape(provider) + "/callback"
}

func TestingCloudAPIBase(provider string) string {
	switch provider {
	case "drive":
		return "https://www.googleapis.com"
	case "dropbox":
		return "https://api.dropboxapi.com"
	case "onedrive":
		return "https://graph.microsoft.com/v1.0"
	default:
		return ""
	}
}
