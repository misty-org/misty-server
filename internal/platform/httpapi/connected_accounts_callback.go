package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"

	"github.com/go-chi/chi/v5"
)

func (s *SpacesService) ConnectedAccountAuthorizationCallback() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		provider, code, state := chi.URLParam(r, "provider"), r.URL.Query().Get("code"), r.URL.Query().Get("state")
		if refusal := r.URL.Query().Get("error"); refusal != "" {
			TestingWriteOAuthCallbackFailure(w, provider, TestingOAuthDeniedByProvider, errors.New(TestingProviderRefusalDetail(r)))
			return
		}
		definition, exists := TestingConnectedAccountOAuthCatalog[provider]
		if !exists || code == "" || state == "" {
			TestingWriteOAuthCallbackFailure(w, provider, TestingOAuthMalformedRedirect, fmt.Errorf(
				"known_provider=%t code_present=%t state_present=%t", exists, code != "", state != "",
			))
			return
		}
		stored, err := s.database.ConsumeConnectedAccountOAuthState(r.Context(), hashProviderValue(state))
		if err != nil || stored.Provider != provider {
			TestingWriteOAuthCallbackFailure(w, provider, TestingOAuthStaleState, err)
			return
		}
		verifier, err := s.decryptConnectedAccountSecret(provider, stored.VerifierCiphertext, stored.VerifierNonce)
		if err != nil {
			TestingWriteOAuthCallbackFailure(w, provider, TestingOAuthStaleState, err)
			return
		}
		token, err := exchangeConnectedAccountCode(r.Context(), definition, code, string(verifier), TestingConnectedAccountCallbackURL(r, provider))
		if err != nil || token.AccessToken == "" {
			TestingWriteOAuthCallbackFailure(w, provider, TestingOAuthExchangeFailed, err)
			return
		}
		accountID, accountDisplay := fetchConnectedAccountIdentity(r.Context(), definition, token)
		if accountID == "" {
			TestingWriteOAuthCallbackFailure(w, provider, TestingOAuthTokenUnusable, errors.New("provider identity is unavailable"))
			return
		}
		capabilities, scopes := stored.Capabilities, stored.RequestedScopes
		if token.Scope != "" {
			scopes = strings.Fields(strings.ReplaceAll(token.Scope, ",", " "))
		}
		if previous, previousErr := s.database.ConnectedAccountByIdentity(r.Context(), stored.UserID, provider, accountID); previousErr == nil {
			if previous.RevokedAt == nil {
				capabilities = mergeConnectedAccountValues(previous.Capabilities, capabilities)
				scopes = mergeConnectedAccountValues(previous.GrantedScopes, scopes)
			}
			if token.RefreshToken == "" && previous.RevokedAt == nil {
				if raw, decryptErr := s.decryptConnectedAccountSecret(provider, previous.CredentialCiphertext, previous.CredentialNonce); decryptErr == nil {
					var old providerTokenEnvelope
					if json.Unmarshal(raw, &old) == nil {
						token.RefreshToken = old.RefreshToken
					}
				}
			}
		}
		encoded, _ := json.Marshal(token)
		ciphertext, nonce, err := s.encryptConnectedAccountSecret(provider, encoded)
		if err != nil {
			TestingWriteOAuthCallbackFailure(w, provider, TestingOAuthNotSaved, err)
			return
		}
		var expiresAt *time.Time
		if token.ExpiresIn > 0 {
			value := time.Now().UTC().Add(time.Duration(token.ExpiresIn) * time.Second)
			expiresAt = &value
		}
		item, err := s.database.SaveConnectedAccount(r.Context(), db.ConnectedAccount{
			UserID: stored.UserID, Provider: provider, AccountID: accountID,
			AccountDisplay: accountDisplay, CredentialCiphertext: ciphertext,
			CredentialNonce: nonce, KeyVersion: s.keyVer, Capabilities: capabilities,
			GrantedScopes: scopes, ExpiresAt: expiresAt,
		})
		if err != nil {
			TestingWriteOAuthCallbackFailure(w, provider, TestingOAuthNotSaved, err)
			return
		}
		TestingWriteProviderCompletionPage(w, definition.Name, item.AccountDisplay)
	}
}

func exchangeConnectedAccountCode(ctx context.Context, definition ConnectedAccountOAuthDefinition, code, verifier, redirect string) (providerTokenEnvelope, error) {
	values := url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirect},
		"client_id":     {connectedAccountClientID(definition)},
		"client_secret": {connectedAccountClientSecret(definition)}, "code_verifier": {verifier},
	}
	return requestConnectedAccountToken(ctx, definition, values)
}

func refreshConnectedAccountToken(ctx context.Context, definition ConnectedAccountOAuthDefinition, refreshToken string) (providerTokenEnvelope, error) {
	if refreshToken == "" {
		return providerTokenEnvelope{}, errors.New("refresh token is missing")
	}
	values := url.Values{
		"grant_type": {"refresh_token"}, "refresh_token": {refreshToken},
		"client_id":     {connectedAccountClientID(definition)},
		"client_secret": {connectedAccountClientSecret(definition)},
	}
	return requestConnectedAccountToken(ctx, definition, values)
}

func requestConnectedAccountToken(ctx context.Context, definition ConnectedAccountOAuthDefinition, values url.Values) (providerTokenEnvelope, error) {
	if definition.TokenAuthBasic {
		values.Del("client_id")
		values.Del("client_secret")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, definition.TokenURL, strings.NewReader(values.Encode()))
	if err != nil {
		return providerTokenEnvelope{}, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	if definition.TokenAuthBasic {
		request.SetBasicAuth(connectedAccountClientID(definition), connectedAccountClientSecret(definition))
	}
	response, err := connectedAccountHTTPClient(20 * time.Second).Do(request)
	if err != nil {
		return providerTokenEnvelope{}, err
	}
	defer response.Body.Close()
	raw, readErr := readConnectedAccountResponse(response.Body)
	if readErr != nil {
		return providerTokenEnvelope{}, readErr
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return providerTokenEnvelope{}, fmt.Errorf("connected account token endpoint returned %s", response.Status)
	}
	var token providerTokenEnvelope
	if json.Unmarshal(raw, &token) != nil || token.AccessToken == "" {
		return providerTokenEnvelope{}, errors.New("connected account token response is invalid")
	}
	return token, nil
}

func fetchConnectedAccountIdentity(ctx context.Context, definition ConnectedAccountOAuthDefinition, token providerTokenEnvelope) (string, string) {
	method := definition.IdentityMethod
	if method == "" {
		method = http.MethodGet
	}
	request, err := http.NewRequestWithContext(ctx, method, definition.IdentityURL, nil)
	if err != nil {
		return "", ""
	}
	request.Header.Set("Authorization", firstNonempty(token.TokenType, "Bearer")+" "+token.AccessToken)
	request.Header.Set("Accept", "application/json")
	if method == http.MethodPost {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := connectedAccountHTTPClient(15 * time.Second).Do(request)
	if err != nil {
		return "", ""
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", ""
	}
	raw, err := readConnectedAccountResponse(response.Body)
	if err != nil {
		return "", ""
	}
	var value map[string]any
	if json.Unmarshal(raw, &value) != nil {
		return "", ""
	}
	id := firstProviderString(value, "sub", "id", "account_id")
	display := firstProviderString(value, "email", "mail", "userPrincipalName", "displayName", "handle", "name", "display_name")
	if display == "" {
		display = definition.Name + " account"
	}
	return id, display
}

const connectedAccountResponseLimit = 1 << 20

const TestingConnectedAccountResponseLimit = connectedAccountResponseLimit

func TestingRequestConnectedAccountToken(ctx context.Context, definition ConnectedAccountOAuthDefinition, values url.Values) error {
	_, err := requestConnectedAccountToken(ctx, definition, values)
	return err
}

func TestingFetchConnectedAccountIdentity(ctx context.Context, definition ConnectedAccountOAuthDefinition, accessToken, tokenType string) (string, string) {
	return fetchConnectedAccountIdentity(ctx, definition, providerTokenEnvelope{AccessToken: accessToken, TokenType: tokenType})
}

func connectedAccountHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func readConnectedAccountResponse(body io.Reader) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(body, connectedAccountResponseLimit+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > connectedAccountResponseLimit {
		return nil, errors.New("connected account provider response is too large")
	}
	return raw, nil
}

func mergeConnectedAccountValues(groups ...[]string) []string {
	seen := map[string]bool{}
	values := []string{}
	for _, group := range groups {
		for _, value := range group {
			value = strings.TrimSpace(value)
			if value == "" || seen[value] {
				continue
			}
			seen[value] = true
			values = append(values, value)
		}
	}
	sort.Strings(values)
	return values
}
