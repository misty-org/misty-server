package api

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/kannachi323/misty/server/internal/appcatalog"
	. "github.com/kannachi323/misty/server/internal/platform/httpapi"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/kannachi323/misty/server/internal/platform/security"
)

func TestConnectedAccountRemovalRPCRequiresOwnershipAndWriteScope(t *testing.T) {
	database := openPresenceTestDatabase(t)
	newUser := func() string {
		t.Helper()
		user, err := database.CreateUserWithUsername("Connection RPC", "conn_"+uuid.NewString()[:8], uniqueTestEmail("connection-rpc"), "password123")
		if err != nil {
			t.Fatal(err)
		}
		return user.ID
	}
	owner, other := newUser(), newUser()
	space, err := database.CreateSpace(t.Context(), owner, "Connections")
	if err != nil {
		t.Fatal(err)
	}
	spaces, err := NewSpacesService(database, nil, base64.StdEncoding.EncodeToString([]byte(strings.Repeat("c", 32))))
	if err != nil {
		t.Fatal(err)
	}
	// Microsoft has no remote per-token revocation here; these test credentials
	// are never sent to any provider or real connected account.
	ciphertext, nonce, err := spaces.TestingEncryptConnectedAccountAccessToken("microsoft", "fixture-only-token")
	if err != nil {
		t.Fatal(err)
	}
	connection := func(userID string) string {
		t.Helper()
		item, err := database.SaveConnectedAccount(t.Context(), db.ConnectedAccount{UserID: userID, Provider: "microsoft", AccountID: uuid.NewString(), CredentialCiphertext: ciphertext, CredentialNonce: nonce, KeyVersion: 1, Capabilities: []string{"mail"}})
		if err != nil {
			t.Fatal(err)
		}
		return item.ID
	}
	own, foreign := connection(owner), connection(other)
	app, ok := appcatalog.Find("inbox")
	if !ok {
		t.Fatal("missing Inbox")
	}
	mint := func(scopes []string) string {
		t.Helper()
		if _, err := database.InstallUserApp(t.Context(), owner, app.ID, app.Version, app.PermissionVersion, scopes); err != nil {
			t.Fatal(err)
		}
		token := "connection-rpc-" + uuid.NewString()
		if _, err := database.CreateAppRuntimeSession(t.Context(), owner, app.ID, security.HashToken(token), space.ID, db.AppRuntimeSessionTTL); err != nil {
			t.Fatal(err)
		}
		return token
	}
	router := chi.NewRouter()
	router.Delete("/connections/{connectionID}", spaces.DeleteConnectedAccount())
	router.Post("/app-runtime/rpc", OfficialAppRPC(database, router, ""))
	call := func(token, connectionID string, want int) {
		t.Helper()
		response := performConversationRequest(t, router, "POST", "/app-runtime/rpc", token, map[string]any{"protocol": 2, "method": "connections.remove", "params": map[string]any{"path": map[string]string{"connectionID": connectionID}}})
		if response.Code != want {
			t.Fatalf("remove status=%d want=%d body=%s", response.Code, want, response.Body.String())
		}
		if want == 204 && response.Body.Len() != 0 {
			t.Fatal("removal returned a body")
		}
	}
	call(mint([]string{"connections.read"}), own, 403)
	writer := mint([]string{"connections.write"})
	call(writer, foreign, 404)
	call(writer, "../invalid", 400)
	if _, err := database.ConnectedAccount(t.Context(), owner, own); err != nil {
		t.Fatal("denied operation changed own connection", err)
	}
	call(writer, own, 204)
	call(writer, own, 404)
	if _, err := database.ConnectedAccount(t.Context(), owner, own); !errors.Is(err, db.ErrSpaceNotFound) {
		t.Fatal("removed connection still accessible", err)
	}
	if _, err := database.ConnectedAccount(t.Context(), other, foreign); err != nil {
		t.Fatal("changed another account's connection", err)
	}
}
