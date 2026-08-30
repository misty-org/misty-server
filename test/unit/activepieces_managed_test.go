package unit

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	api "github.com/kannachi323/misty/server/internal/platform/httpapi"
)

func TestManagedActivepiecesProvisionsPrivateProjectWithoutBrowserOAuth(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	requests := []string{}
	adminCreated := false
	userCreated := false
	adminEmail := ""
	userEmail := ""

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.Method+" "+r.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/authentication/sign-in":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			email, _ := body["email"].(string)
			if strings.Contains(email, "automations+") && strings.Contains(email, "@mistysys.com") {
				if adminCreated && email == adminEmail {
					writeTestJSON(w, http.StatusOK, map[string]any{"id": "admin", "token": "admin-token", "projectId": "admin-project", "platformId": "platform"})
					return
				}
				if userCreated && email == userEmail {
					writeTestJSON(w, http.StatusOK, map[string]any{"id": "user", "token": "user-token", "projectId": "user-project", "platformId": "platform"})
					return
				}
			}
			writeTestJSON(w, http.StatusUnauthorized, map[string]string{"code": "invalid_credentials"})
		case "/api/v1/authentication/sign-up":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			email, _ := body["email"].(string)
			if !adminCreated {
				adminCreated = true
				adminEmail = email
				writeTestJSON(w, http.StatusOK, map[string]any{"id": "admin-identity", "token": "onboarding-token"})
				return
			}
			userCreated = true
			writeTestJSON(w, http.StatusOK, map[string]any{"id": "user", "token": "user-token", "projectId": "user-project", "platformId": "platform"})
		case "/api/v1/platforms":
			if r.Header.Get("Authorization") != "Bearer onboarding-token" {
				t.Fatalf("unexpected platform authorization: %q", r.Header.Get("Authorization"))
			}
			writeTestJSON(w, http.StatusOK, map[string]any{"id": "admin", "token": "admin-token", "projectId": "admin-project", "platformId": "platform"})
		case "/api/v1/user-invitations":
			if r.Header.Get("Authorization") != "Bearer admin-token" {
				t.Fatalf("unexpected invitation authorization: %q", r.Header.Get("Authorization"))
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			userEmail, _ = body["email"].(string)
			writeTestJSON(w, http.StatusCreated, map[string]any{"id": "invitation"})
		case "/api/v1/projects/user-project/mcp-server/token":
			if r.Header.Get("Authorization") != "Bearer user-token" {
				t.Fatalf("unexpected project authorization: %q", r.Header.Get("Authorization"))
			}
			writeTestJSON(w, http.StatusOK, map[string]string{"mcpToken": "managed-mcp-token"})
		default:
			writeTestJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		}
	}))
	defer server.Close()

	client := api.TestingNewManagedActivepieces(server.URL, "https://automations.example.com/mcp", strings.Repeat("s", 32), server.Client())
	endpoint, token, err := client.Access(t.Context(), "misty-user-1")
	if err != nil {
		t.Fatalf("managed access: %v", err)
	}
	if endpoint != "https://automations.example.com/mcp" || token != "managed-mcp-token" {
		t.Fatalf("endpoint=%q token=%q", endpoint, token)
	}

	requestCount := len(requests)
	_, cachedToken, err := client.Access(t.Context(), "misty-user-1")
	if err != nil || cachedToken != token {
		t.Fatalf("cached access token=%q err=%v", cachedToken, err)
	}
	if len(requests) != requestCount {
		t.Fatalf("expected cached access, got %d extra requests", len(requests)-requestCount)
	}
}

func writeTestJSON(w http.ResponseWriter, status int, value any) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
