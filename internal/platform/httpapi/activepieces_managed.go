package api

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
)

type ManagedActivepiecesClient interface {
	Access(context.Context, string) (endpointURL string, bearer string, err error)
}

type managedActivepiecesClient struct {
	baseURL         string
	mcpURL          string
	bootstrapSecret string
	httpClient      *http.Client

	mu       sync.Mutex
	sessions map[string]managedActivepiecesSession
}

type managedActivepiecesSession struct {
	Bearer   string
	Expires  time.Time
	Project  string
	UserID   string
	Platform string
}

type activepiecesAuthenticationResponse struct {
	ID         string `json:"id"`
	Token      string `json:"token"`
	ProjectID  string `json:"projectId"`
	PlatformID string `json:"platformId"`
}

type activepiecesMCPTokenResponse struct {
	Token string `json:"mcpToken"`
}

func ManagedActivepiecesFromEnv() (ManagedActivepiecesClient, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(envconfig.Getenv("MISTY_ACTIVEPIECES_PROXY_URL")), "/")
	if baseURL == "" {
		return nil, nil
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme != "http" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("MISTY_ACTIVEPIECES_PROXY_URL must be a complete internal HTTP origin")
	}
	mcpURL, err := configuredActivepiecesMCPURL()
	if err != nil {
		return nil, err
	}
	secret := strings.TrimSpace(envconfig.Getenv("MISTY_ACTIVEPIECES_BOOTSTRAP_SECRET"))
	if len(secret) < 32 {
		return nil, errors.New("MISTY_ACTIVEPIECES_BOOTSTRAP_SECRET must contain at least 32 characters")
	}
	return &managedActivepiecesClient{
		baseURL:         baseURL,
		mcpURL:          mcpURL,
		bootstrapSecret: secret,
		httpClient:      &http.Client{Timeout: 15 * time.Second},
		sessions:        map[string]managedActivepiecesSession{},
	}, nil
}

func (c *managedActivepiecesClient) Access(ctx context.Context, mistyUserID string) (string, string, error) {
	if strings.TrimSpace(mistyUserID) == "" {
		return "", "", errors.New("activepieces managed access requires a Misty user")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if cached, ok := c.sessions[mistyUserID]; ok && time.Until(cached.Expires) > 90*time.Second {
		return c.mcpURL, cached.Bearer, nil
	}

	admin, err := c.ensureAdmin(ctx)
	if err != nil {
		return "", "", err
	}
	user, err := c.ensureUser(ctx, admin, mistyUserID)
	if err != nil {
		return "", "", err
	}
	if user.ProjectID == "" {
		return "", "", errors.New("activepieces did not assign an automation project")
	}
	var token activepiecesMCPTokenResponse
	if err := c.requestJSON(ctx, http.MethodPost, "/api/v1/projects/"+url.PathEscape(user.ProjectID)+"/mcp-server/token", user.Token, nil, &token); err != nil {
		return "", "", fmt.Errorf("mint Activepieces project token: %w", err)
	}
	if token.Token == "" {
		return "", "", errors.New("activepieces returned an empty project token")
	}
	c.sessions[mistyUserID] = managedActivepiecesSession{
		Bearer: token.Token, Expires: time.Now().UTC().Add(14 * time.Minute), Project: user.ProjectID,
		UserID: user.ID, Platform: user.PlatformID,
	}
	return c.mcpURL, token.Token, nil
}

func (c *managedActivepiecesClient) ensureAdmin(ctx context.Context) (activepiecesAuthenticationResponse, error) {
	email, password := c.identity("service-admin")
	admin, signInErr := c.signIn(ctx, email, password)
	if signInErr == nil && admin.ProjectID != "" {
		return admin, nil
	}

	var onboarding activepiecesAuthenticationResponse
	err := c.requestJSON(ctx, http.MethodPost, "/api/v1/authentication/sign-up", "", map[string]any{
		"email": email, "password": password, "firstName": "Misty", "lastName": "Automations",
		"trackEvents": false, "newsLetter": false,
	}, &onboarding)
	if err != nil {
		if signInErr != nil {
			return activepiecesAuthenticationResponse{}, fmt.Errorf("bootstrap Activepieces service account: %w", err)
		}
		return activepiecesAuthenticationResponse{}, errors.New("activepieces service account has no project")
	}
	if onboarding.Token == "" {
		return activepiecesAuthenticationResponse{}, errors.New("activepieces returned an empty onboarding token")
	}
	var created activepiecesAuthenticationResponse
	if err := c.requestJSON(ctx, http.MethodPost, "/api/v1/platforms", onboarding.Token, map[string]string{"name": "Misty Automations"}, &created); err != nil {
		return activepiecesAuthenticationResponse{}, fmt.Errorf("create Activepieces workspace: %w", err)
	}
	return created, nil
}

func (c *managedActivepiecesClient) ensureUser(ctx context.Context, admin activepiecesAuthenticationResponse, mistyUserID string) (activepiecesAuthenticationResponse, error) {
	email, password := c.identity("misty-user:" + mistyUserID)
	if user, err := c.signIn(ctx, email, password); err == nil && user.ProjectID != "" {
		return user, nil
	}
	if err := c.requestJSON(ctx, http.MethodPost, "/api/v1/user-invitations", admin.Token, map[string]string{
		"type": "PLATFORM", "email": email, "platformRole": "MEMBER",
	}, nil); err != nil {
		return activepiecesAuthenticationResponse{}, fmt.Errorf("prepare Activepieces user workspace: %w", err)
	}
	var user activepiecesAuthenticationResponse
	if err := c.requestJSON(ctx, http.MethodPost, "/api/v1/authentication/sign-up", "", map[string]any{
		"email": email, "password": password, "firstName": "Misty", "lastName": "User",
		"trackEvents": false, "newsLetter": false,
	}, &user); err != nil {
		return activepiecesAuthenticationResponse{}, fmt.Errorf("create Activepieces user workspace: %w", err)
	}
	return user, nil
}

func (c *managedActivepiecesClient) signIn(ctx context.Context, email, password string) (activepiecesAuthenticationResponse, error) {
	var response activepiecesAuthenticationResponse
	err := c.requestJSON(ctx, http.MethodPost, "/api/v1/authentication/sign-in", "", map[string]string{
		"email": email, "password": password,
	}, &response)
	return response, err
}

func (c *managedActivepiecesClient) identity(scope string) (string, string) {
	mac := hmac.New(sha256.New, []byte(c.bootstrapSecret))
	_, _ = mac.Write([]byte(scope))
	sum := mac.Sum(nil)
	identifier := hex.EncodeToString(sum[:10])
	password := base64.RawURLEncoding.EncodeToString(sum)
	return "automations+" + identifier + "@mistysys.com", password
}

func (c *managedActivepiecesClient) requestJSON(ctx context.Context, method, path, bearer string, body any, result any) error {
	var input io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		input = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, input)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		limited, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("Activepieces returned %d: %s", response.StatusCode, strings.TrimSpace(string(limited)))
	}
	if result == nil || response.StatusCode == http.StatusNoContent {
		_, _ = io.Copy(io.Discard, response.Body)
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(result); err != nil {
		return fmt.Errorf("decode Activepieces response: %w", err)
	}
	return nil
}

func TestingNewManagedActivepieces(baseURL, mcpURL, secret string, client *http.Client) ManagedActivepiecesClient {
	return &managedActivepiecesClient{
		baseURL: strings.TrimRight(baseURL, "/"), mcpURL: mcpURL, bootstrapSecret: secret,
		httpClient: client, sessions: map[string]managedActivepiecesSession{},
	}
}
