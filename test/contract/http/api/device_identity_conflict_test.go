package api

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	. "github.com/kannachi323/misty/server/internal/platform/httpapi"
)

func TestDeviceRegistrationReturnsIdentityConflict(t *testing.T) {
	database := openPresenceTestDatabase(t)
	user, err := database.CreateUser("Device owner", uniqueTestEmail("device-identity"), "password123")
	if err != nil {
		t.Fatal(err)
	}
	token := newConversationTestBearerToken(t, database, user.ID)
	router := chi.NewRouter()
	router.Post("/devices", NewAgentsService(database).RegisterDevice())
	payload := map[string]any{"name": "Device", "publicKey": base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("a", 32))), "keyAlgorithm": "ed25519", "platform": "macos", "p2pEndpointId": strings.Repeat("a", 64), "protocolVersions": []string{"misty-device/1"}, "capabilities": map[string]any{}}
	first := performConversationRequest(t, router, http.MethodPost, "/devices", token, payload)
	if first.Code != http.StatusCreated {
		t.Fatalf("registration: %d %s", first.Code, first.Body.String())
	}
	payload["publicKey"] = base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("b", 32)))
	conflict := performConversationRequest(t, router, http.MethodPost, "/devices", token, payload)
	var body map[string]string
	if err := json.Unmarshal(conflict.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if conflict.Code != http.StatusConflict || body["code"] != "device_identity_conflict" {
		t.Fatalf("conflict: %d %s", conflict.Code, conflict.Body.String())
	}
	unauthenticated := performConversationRequest(t, router, http.MethodPost, "/devices", "", payload)
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated: %d", unauthenticated.Code)
	}
}
