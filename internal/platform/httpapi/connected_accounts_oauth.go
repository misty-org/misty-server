package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"

	"github.com/go-chi/chi/v5"
)

type ConnectedAccountOAuthDefinition struct {
	ID, Name, AuthorizeURL, TokenURL, ClientIDEnv, ClientSecretEnv, IdentityURL string
	IdentityMethod                                                              string
	TokenAuthBasic                                                              bool
	DisablePKCE                                                                 bool
	IdentityTokenQuery                                                          bool
	BaseScopes                                                                  []string
	CapabilityScopes                                                            map[string][]string
	AuthorizationParams                                                         map[string]string
}

var TestingConnectedAccountOAuthCatalog = map[string]ConnectedAccountOAuthDefinition{
	"google": {
		ID: "google", Name: "Google", AuthorizeURL: "https://accounts.google.com/o/oauth2/v2/auth",
		TokenURL: "https://oauth2.googleapis.com/token", IdentityURL: "https://openidconnect.googleapis.com/v1/userinfo",
		ClientIDEnv: "GOOGLE_CLIENT_ID", ClientSecretEnv: "GOOGLE_CLIENT_SECRET",
		BaseScopes: []string{"openid", "email", "profile"},
		CapabilityScopes: map[string][]string{
			"mail":           {"https://www.googleapis.com/auth/gmail.modify", "https://www.googleapis.com/auth/gmail.send"},
			"calendar_read":  {"https://www.googleapis.com/auth/calendar.readonly"},
			"calendar_write": {"https://www.googleapis.com/auth/calendar.readonly", "https://www.googleapis.com/auth/calendar.events"},
			"files":          {"https://www.googleapis.com/auth/drive"},
		},
	},
	"microsoft": {
		ID: "microsoft", Name: "Microsoft",
		AuthorizeURL: "https://login.microsoftonline.com/common/oauth2/v2.0/authorize",
		TokenURL:     "https://login.microsoftonline.com/common/oauth2/v2.0/token",
		IdentityURL:  "https://graph.microsoft.com/v1.0/me?$select=id,displayName,userPrincipalName,mail",
		ClientIDEnv:  "MICROSOFT_CLIENT_ID", ClientSecretEnv: "MICROSOFT_CLIENT_SECRET",
		BaseScopes: []string{"openid", "profile", "email", "offline_access", "User.Read"},
		CapabilityScopes: map[string][]string{
			"mail":  {"Mail.ReadWrite", "Mail.Send"},
			"files": {"Files.ReadWrite.All"},
		},
	},
	"dropbox": {
		ID: "dropbox", Name: "Dropbox", AuthorizeURL: "https://www.dropbox.com/oauth2/authorize",
		TokenURL: "https://api.dropboxapi.com/oauth2/token", IdentityURL: "https://api.dropboxapi.com/2/users/get_current_account",
		IdentityMethod: http.MethodPost,
		ClientIDEnv:    "MISTY_DROPBOX_CLIENT_ID", ClientSecretEnv: "MISTY_DROPBOX_CLIENT_SECRET",
		CapabilityScopes: map[string][]string{
			"files": {"account_info.read", "files.metadata.read", "files.content.read", "files.content.write"},
		},
	},
	"figma": {
		ID: "figma", Name: "Figma", AuthorizeURL: "https://www.figma.com/oauth",
		TokenURL: "https://api.figma.com/v1/oauth/token", IdentityURL: "https://api.figma.com/v1/me",
		ClientIDEnv: "FIGMA_CLIENT_ID", ClientSecretEnv: "FIGMA_CLIENT_SECRET",
		TokenAuthBasic: true,
		BaseScopes:     []string{"current_user:read"},
		CapabilityScopes: map[string][]string{
			"drawings_read":     {"file_metadata:read", "file_content:read", "file_versions:read", "file_comments:read"},
			"drawings_comments": {"file_comments:write"},
			// Team/folder discovery is optional because Figma does not make the
			// corresponding list endpoints generally available to public OAuth apps.
			"drawings_projects": {"folders:read"},
			"drawings_webhooks": {"webhooks:write"},
		},
	},
	"discord": {
		ID: "discord", Name: "Discord", AuthorizeURL: "https://discord.com/oauth2/authorize",
		TokenURL: "https://discord.com/api/v10/oauth2/token", IdentityURL: "https://discord.com/api/v10/users/@me",
		ClientIDEnv: "DISCORD_CLIENT_ID", ClientSecretEnv: "DISCORD_CLIENT_SECRET",
		BaseScopes: []string{"identify", "guilds", "bot"},
		AuthorizationParams: map[string]string{
			"permissions": "68608",
		},
		CapabilityScopes: map[string][]string{
			"social_read":       {"identify", "guilds"},
			"social_send":       {"identify", "guilds"},
			"social_automation": {"identify", "guilds"},
		},
	},
	"instagram": {
		ID: "instagram", Name: "Instagram", AuthorizeURL: "https://www.instagram.com/oauth/authorize",
		TokenURL: "https://api.instagram.com/oauth/access_token", IdentityURL: "https://graph.instagram.com/me?fields=id,username",
		ClientIDEnv: "INSTAGRAM_CLIENT_ID", ClientSecretEnv: "INSTAGRAM_CLIENT_SECRET",
		DisablePKCE: true, IdentityTokenQuery: true,
		BaseScopes: []string{"instagram_business_basic", "instagram_business_manage_messages"},
		CapabilityScopes: map[string][]string{
			"social_read":       {"instagram_business_basic", "instagram_business_manage_messages"},
			"social_send":       {"instagram_business_basic", "instagram_business_manage_messages"},
			"social_automation": {"instagram_business_basic", "instagram_business_manage_messages"},
		},
	},
}

type ConnectedAccountResponse struct {
	ID             string     `json:"id"`
	Provider       string     `json:"provider"`
	AccountID      string     `json:"account_id"`
	AccountDisplay string     `json:"account_display"`
	Capabilities   []string   `json:"capabilities"`
	GrantedScopes  []string   `json:"granted_scopes"`
	Status         string     `json:"status"`
	LastErrorCode  string     `json:"last_error_code,omitempty"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
}

func publicConnectedAccount(item db.ConnectedAccount) ConnectedAccountResponse {
	return ConnectedAccountResponse{
		ID: item.ID, Provider: item.Provider, AccountID: item.AccountID,
		AccountDisplay: item.AccountDisplay, Capabilities: item.Capabilities,
		GrantedScopes: item.GrantedScopes, Status: item.Status,
		LastErrorCode: item.LastErrorCode, ExpiresAt: item.ExpiresAt,
	}
}

func (s *SpacesService) ConnectedAccounts() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		items, err := s.database.ConnectedAccounts(r.Context(), userID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		connections := make([]ConnectedAccountResponse, 0, len(items))
		for _, item := range items {
			connections = append(connections, publicConnectedAccount(item))
		}
		providers := map[string]bool{}
		for id, definition := range TestingConnectedAccountOAuthCatalog {
			providers[id] = connectedAccountClientID(definition) != "" && connectedAccountClientSecret(definition) != ""
		}
		writeJSON(w, http.StatusOK, map[string]any{"connections": connections, "providers": providers})
	}
}

func (s *SpacesService) BeginConnectedAccountAuthorization() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		provider := chi.URLParam(r, "provider")
		definition, exists := TestingConnectedAccountOAuthCatalog[provider]
		if !exists {
			writeSpaceError(w, db.ErrSpaceInvalid)
			return
		}
		clientID, clientSecret := connectedAccountClientID(definition), connectedAccountClientSecret(definition)
		if clientID == "" || clientSecret == "" {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "provider_not_configured", "provider": provider})
			return
		}
		var body struct {
			Capabilities []string `json:"capabilities"`
			ReturnTo     string   `json:"return_to"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		if len(body.Capabilities) == 0 {
			body.Capabilities = []string{"mail"}
			if provider == "figma" {
				body.Capabilities = []string{"drawings_read"}
			} else if provider == "discord" || provider == "instagram" {
				body.Capabilities = []string{"social_read", "social_send"}
			}
		}
		if provider == "figma" {
			// Figma has one active app token per user. Always request the union of
			// previously granted capabilities so incremental consent cannot replace
			// a read-capable token with a narrower comments-only token.
			if accounts, accountErr := s.database.ConnectedAccounts(r.Context(), userID); accountErr == nil {
				for _, account := range accounts {
					if account.Provider == provider && account.Status == "active" && account.RevokedAt == nil {
						body.Capabilities = mergeConnectedAccountValues(body.Capabilities, account.Capabilities)
					}
				}
			}
		}
		capabilities, scopes, valid := TestingConnectedAccountRequestedScopes(definition, body.Capabilities)
		if !valid || !TestingValidProviderReturnPath(body.ReturnTo) {
			writeSpaceError(w, db.ErrSpaceInvalid)
			return
		}
		state, verifier := randomProviderValue(32), randomProviderValue(48)
		ciphertext, nonce, err := s.encryptConnectedAccountSecret(provider, []byte(verifier))
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		expiresAt := time.Now().UTC().Add(10 * time.Minute)
		err = s.database.CreateConnectedAccountOAuthState(r.Context(), hashProviderValue(state), db.ConnectedAccountOAuthState{
			UserID: userID, Provider: provider, Capabilities: capabilities, RequestedScopes: scopes,
			VerifierCiphertext: ciphertext, VerifierNonce: nonce, ReturnTo: body.ReturnTo, ExpiresAt: expiresAt,
		})
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		callback := TestingConnectedAccountCallbackURL(r, provider)
		params := url.Values{
			"client_id": {clientID}, "redirect_uri": {callback}, "response_type": {"code"},
			"state": {state}, "scope": {strings.Join(scopes, " ")},
		}
		if !definition.DisablePKCE {
			digest := sha256.Sum256([]byte(verifier))
			params.Set("code_challenge", base64.RawURLEncoding.EncodeToString(digest[:]))
			params.Set("code_challenge_method", "S256")
		}
		for key, value := range definition.AuthorizationParams {
			params.Set(key, value)
		}
		if provider == "google" {
			params.Set("access_type", "offline")
			params.Set("include_granted_scopes", "true")
			params.Set("prompt", "consent")
		} else if provider == "dropbox" {
			params.Set("token_access_type", "offline")
		} else if provider != "instagram" && provider != "discord" {
			params.Set("prompt", "select_account")
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"provider": provider, "capabilities": capabilities,
			"authorization_url": definition.AuthorizeURL + "?" + params.Encode(),
			"state_expires_at":  expiresAt,
		})
	}
}

func TestingConnectedAccountRequestedScopes(definition ConnectedAccountOAuthDefinition, requested []string) ([]string, []string, bool) {
	capabilitySet, scopeSet := map[string]bool{}, map[string]bool{}
	for _, scope := range definition.BaseScopes {
		scopeSet[scope] = true
	}
	for _, capability := range requested {
		capability = strings.ToLower(strings.TrimSpace(capability))
		scopes, exists := definition.CapabilityScopes[capability]
		if !exists {
			return nil, nil, false
		}
		capabilitySet[capability] = true
		for _, scope := range scopes {
			scopeSet[scope] = true
		}
	}
	capabilities, scopes := make([]string, 0, len(capabilitySet)), make([]string, 0, len(scopeSet))
	for capability := range capabilitySet {
		capabilities = append(capabilities, capability)
	}
	for scope := range scopeSet {
		scopes = append(scopes, scope)
	}
	sort.Strings(capabilities)
	sort.Strings(scopes)
	return capabilities, scopes, len(capabilities) > 0
}

func connectedAccountClientID(definition ConnectedAccountOAuthDefinition) string {
	return strings.TrimSpace(envconfig.Getenv(definition.ClientIDEnv))
}

func connectedAccountClientSecret(definition ConnectedAccountOAuthDefinition) string {
	return strings.TrimSpace(envconfig.Getenv(definition.ClientSecretEnv))
}

func (s *SpacesService) encryptConnectedAccountSecret(provider string, plaintext []byte) ([]byte, []byte, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, err
	}
	return s.aead.Seal(nil, nonce, plaintext, []byte("misty-connected-account-v1:"+provider)), nonce, nil
}

func (s *SpacesService) decryptConnectedAccountSecret(provider string, ciphertext, nonce []byte) ([]byte, error) {
	return s.aead.Open(nil, nonce, ciphertext, []byte("misty-connected-account-v1:"+provider))
}

func TestingConnectedAccountCallbackURL(r *http.Request, provider string) string {
	return requestPublicAPIBase(r) + "/oauth/connections/" + url.PathEscape(provider) + "/callback"
}
