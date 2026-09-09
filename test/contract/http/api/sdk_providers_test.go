package api

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"github.com/google/uuid"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	cap "github.com/kannachi323/misty/server/internal/capabilities"
	. "github.com/kannachi323/misty/server/internal/platform/httpapi"
)

func TestSDKProviderHTTPInstallAndAccountRPC(t *testing.T) {
	t.Setenv("MISTY_SDK_PROVIDERS_ENABLED", "false")
	database := openPresenceTestDatabase(t)
	user, err := database.CreateUser("SDK HTTP", uniqueTestEmail("sdk-http"), "password123")
	if err != nil {
		t.Fatal(err)
	}
	accountToken := newConversationTestBearerToken(t, database, user.ID)
	provider := map[string]any{"id": "example.habits/backend", "version": 1, "label": "Habit tracker", "route": map[string]any{"kind": "backend", "connectionId": "10000000-0000-4000-8000-000000000001"}, "capabilities": []any{map[string]any{"name": "habits.list", "version": 1, "description": "List habits", "requiredScopes": []string{"habits.list"}, "inputSchema": map[string]any{"type": "object"}, "outputSchema": map[string]any{"type": "array"}, "effects": map[string]any{"kind": "read", "incidental": []string{}, "approval": "none", "retry": "read_only"}}}}
	provider["capabilities"] = append(provider["capabilities"].([]any), map[string]any{"name": "habits.record", "version": 1, "description": "Record a habit", "requiredScopes": []string{"habits.record"}, "inputSchema": map[string]any{"type": "object"}, "outputSchema": map[string]any{"type": "array"}, "effects": map[string]any{"kind": "write", "incidental": []string{}, "approval": "scoped", "retry": "reconcile"}})
	document, _ := json.Marshal(map[string]any{"appId": "example.habits", "version": "1.0.0", "permissionVersion": 1, "scopes": []string{"capabilities.providers.write", "capabilities.read", "capabilities.invoke", "habits.list", "habits.record"}, "capabilities": map[string]any{"protocol": 1, "providers": []any{provider}}})
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signed := cap.SignedManifest{Document: string(document), PublicKey: base64.StdEncoding.EncodeToString(pub), Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(key, []byte(cap.SignatureDomain+string(document))))}
	verified, err := cap.Verify(signed)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewSpacesService(database, nil, base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	router.Post("/me/sdk-apps/install", InstallSDKApp(database))
	router.Post("/me/sdk-apps/{appID}/sessions", CreateSDKAppSession(database))
	router.Delete("/me/sdk-apps/{appID}", UninstallSDKApp(database))
	router.Post("/capabilities/providers", RegisterSDKProvider(database))
	router.Delete("/capabilities/providers/{providerID}", SDKProviderLifecycle(database))
	router.Put("/capabilities/providers/{providerID}/availability", SDKProviderLifecycle(database))
	router.Post("/capabilities/discover", DiscoverSDKProviders(database))
	router.Post("/app-runtime/rpc", OfficialAppRPC(database, router, ""))
	router.Put("/me/sdk-apps/{appID}/connections/{connectionID}", service.SDKBackendConnectionControl())
	router.Post("/me/sdk-targets", ConfigureSDKTarget(database))
	router.Get("/me/sdk-targets", SDKTargetControlList(database))
	router.Post("/capabilities/targets/resolve", ResolveSDKTargets(database))
	responseList := performConversationRequest(t, router, http.MethodGet, "/me/sdk-targets?limit=1", accountToken, nil)
	if responseList.Code != http.StatusOK || responseList.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("trusted target inventory: %d %s", responseList.Code, responseList.Body.String())
	}

	installBody := map[string]any{"manifest": signed, "reviewedDigest": verified.Digest}
	response := performConversationRequest(t, router, http.MethodPost, "/me/sdk-apps/install", accountToken, installBody)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("disabled admission: %d %s", response.Code, response.Body.String())
	}
	t.Setenv("MISTY_SDK_PROVIDERS_ENABLED", "true")
	response = performConversationRequest(t, router, http.MethodPost, "/me/sdk-apps/install", accountToken, installBody)
	if response.Code != 201 {
		t.Fatalf("install: %d %s", response.Code, response.Body.String())
	}
	response = performConversationRequest(t, router, http.MethodPost, "/me/sdk-apps/example.habits/sessions", accountToken, map[string]any{})
	if response.Code != 201 {
		t.Fatalf("session: %d %s", response.Code, response.Body.String())
	}
	var issued struct {
		Token string `json:"token"`
	}
	if json.Unmarshal(response.Body.Bytes(), &issued) != nil || issued.Token == "" {
		t.Fatal("missing app token")
	}
	response = performConversationRequest(t, router, http.MethodPost, "/me/sdk-apps/install", issued.Token, installBody)
	if response.Code != 403 {
		t.Fatalf("app self-install: %d %s", response.Code, response.Body.String())
	}
	rpc := func(method string, params map[string]any, status int) {
		t.Helper()
		response := performConversationRequest(t, router, http.MethodPost, "/app-runtime/rpc", issued.Token, map[string]any{"protocol": 2, "method": method, "params": params})
		if response.Code != status {
			t.Fatalf("%s: %d want %d: %s", method, response.Code, status, response.Body.String())
		}
	}
	rpc("capabilities.providers.register", map[string]any{"body": map[string]any{"manifestDigest": verified.Digest, "provider": provider}}, 200)
	rpc("capabilities.discover", map[string]any{"body": map[string]any{}}, 200)
	rpc("capabilities.providers.availability", map[string]any{"path": map[string]string{"providerID": "example.habits/backend"}, "body": cap.Availability{State: "available", ObservedAt: time.Now().UTC()}}, 204)
	connectionPath := "/me/sdk-apps/example.habits/connections/10000000-0000-4000-8000-000000000001"
	configure := map[string]any{"expectedRevision": 0, "endpointURL": "https://habits.example.com/execute", "bearerToken": "private-provider-token"}
	response = performConversationRequest(t, router, http.MethodPut, connectionPath, issued.Token, configure)
	if response.Code != 403 {
		t.Fatalf("app set credentials: %d %s", response.Code, response.Body.String())
	}
	response = performConversationRequest(t, router, http.MethodPut, connectionPath, accountToken, configure)
	if response.Code != 200 || strings.Contains(response.Body.String(), "private-provider-token") {
		t.Fatalf("configure backend: %d %s", response.Code, response.Body.String())
	}
	targetID := uuid.NewString()
	targetRequest := cap.TargetConfiguration{TargetID: targetID, ProviderID: "example.habits/backend", ProviderVersion: 1, Label: "My habit account", Capabilities: []string{"habits.list", "habits.record"}, CallerApps: []string{"example.habits"}}
	response = performConversationRequest(t, router, http.MethodPost, "/me/sdk-targets", issued.Token, targetRequest)
	if response.Code != 403 {
		t.Fatalf("app granted target: %d %s", response.Code, response.Body.String())
	}
	response = performConversationRequest(t, router, http.MethodPost, "/me/sdk-targets", accountToken, targetRequest)
	if response.Code != 200 || strings.Contains(response.Body.String(), "bearer") || strings.Contains(response.Body.String(), "endpoint") {
		t.Fatalf("configure target: %d %s", response.Code, response.Body.String())
	}
	rpc("capabilities.targets.resolve", map[string]any{"body": map[string]any{"capability": "habits.list", "targetId": targetID}}, 200)
	response = performConversationRequest(t, router, http.MethodPost, "/capabilities/targets/resolve", issued.Token, map[string]any{"capability": "habits.list", "targetId": targetID})
	var targetPage struct {
		Targets []cap.Target `json:"targets"`
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &targetPage) != nil || len(targetPage.Targets) != 1 {
		t.Fatalf("resolved targets: %d %s", response.Code, response.Body.String())
	}
	rpc("capabilities.discover", map[string]any{"body": map[string]any{"targetId": targetID}}, 200)

	testSDKInvocationHTTPExecution(t, database, service, router, issued.Token, accountToken, targetID)

	rpc("capabilities.providers.availability", map[string]any{"path": map[string]string{"providerID": "example.habits/backend"}, "body": cap.Availability{State: "available", ObservedAt: time.Now().UTC()}}, 204)
	rpc("capabilities.providers.unregister", map[string]any{"path": map[string]string{"providerID": "another.habits/backend"}}, 403)
	rpc("capabilities.providers.unregister", map[string]any{"path": map[string]string{"providerID": "example.habits/backend"}}, 204)
	rpc("capabilities.providers.availability", map[string]any{"path": map[string]string{"providerID": "example.habits/backend"}, "body": cap.Availability{State: "available", ObservedAt: time.Now().UTC()}}, 409)
	response = performConversationRequest(t, router, http.MethodDelete, "/me/sdk-apps/example.habits", accountToken, nil)
	if response.Code != 200 {
		t.Fatalf("uninstall: %d %s", response.Code, response.Body.String())
	}
	rpc("capabilities.discover", map[string]any{"body": map[string]any{}}, 401)
}
