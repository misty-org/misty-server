package api

import (
	"net/http"
	"slices"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/kannachi323/misty/server/internal/appcatalog"
	. "github.com/kannachi323/misty/server/internal/platform/httpapi"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/kannachi323/misty/server/internal/platform/security"
)

func TestTerminalUpgradeRequiresCurrentPermissionConsentAndRetiresOldSessions(t *testing.T) {
	testOfficialAppPermissionUpgrade(t, "terminal", 1, []string{"terminal.execute"})
}
func TestPlannerUpgradeRequiresCurrentPermissionConsentAndRetiresOldSessions(t *testing.T) {
	testOfficialAppPermissionUpgrade(t, "planner", 3, []string{"tasks.read", "tasks.write", "calendar.read", "calendar.write", "roadmaps.read", "roadmaps.write"})
}
func TestJournalUpgradeRequiresCurrentPermissionConsentAndRetiresOldSessions(t *testing.T) {
	testOfficialAppPermissionUpgrade(t, "journal", 2, []string{"notes.read", "notes.write", "drawings.read", "drawings.write", "spaces.read"})
}
func testOfficialAppPermissionUpgrade(t *testing.T, appID string, oldPermission int, oldScopes []string) {
	t.Helper()
	database := openPresenceTestDatabase(t)
	user, err := database.CreateUserWithUsername("Terminal owner", "terminal_"+uuid.NewString()[:8], uniqueTestEmail("terminal-upgrade"), "password123")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.InstallUserApp(t.Context(), user.ID, appID, "1.0.0", oldPermission, oldScopes); err != nil {
		t.Fatal(err)
	}
	oldToken := security.HashToken("terminal-upgrade-old-token-" + uuid.NewString())
	if _, err := database.CreateAppRuntimeSession(t.Context(), user.ID, appID, oldToken, "", db.AppRuntimeSessionTTL); err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	router.Put("/me/apps/{appID}", MyOfficialApp(database))
	router.Post("/me/apps/{appID}/sessions", CreateOfficialAppSession(database))
	token := newConversationTestBearerToken(t, database, user.ID)
	rejected := performConversationRequest(t, router, http.MethodPut, "/me/apps/"+appID, token, map[string]any{"permission_version": oldPermission})
	if rejected.Code != http.StatusConflict {
		t.Fatalf("stale consent = %d: %s", rejected.Code, rejected.Body.String())
	}
	session, err := database.AppRuntimeSessionByToken(t.Context(), oldToken)
	if err != nil || session == nil || !slices.Equal(session.Scopes, oldScopes) {
		t.Fatalf("stale consent changed grants: %#v, %v", session, err)
	}
	catalog, _ := appcatalog.Find(appID)
	accepted := performConversationRequest(t, router, http.MethodPut, "/me/apps/"+appID, token, map[string]any{"permission_version": catalog.PermissionVersion})
	if accepted.Code != http.StatusOK {
		t.Fatalf("reviewed upgrade = %d: %s", accepted.Code, accepted.Body.String())
	}
	if session, err := database.AppRuntimeSessionByToken(t.Context(), oldToken); err != nil || session != nil {
		t.Fatalf("old session survived upgrade: %#v, %v", session, err)
	}
	newSession, err := database.CreateAppRuntimeSession(t.Context(), user.ID, appID, security.HashToken("terminal-upgrade-new-token-"+uuid.NewString()), "", db.AppRuntimeSessionTTL)
	if err != nil {
		t.Fatal(err)
	}
	for _, scope := range catalog.Scopes {
		if !slices.Contains(newSession.Scopes, scope) {
			t.Fatalf("reviewed scope %s missing", scope)
		}
	}
	created := performConversationRequest(t, router, http.MethodPost, "/me/apps/"+appID+"/sessions", token, map[string]any{"space_id": ""})
	if created.Code != http.StatusCreated {
		t.Fatalf("downloaded app HTTP session = %d: %s", created.Code, created.Body.String())
	}
}

func TestAccountConnectionRPCStillRequiresItsBoundSpaceMembership(t *testing.T) {
	database := openPresenceTestDatabase(t)
	user, err := database.CreateUserWithUsername("RPC owner", "rpc_"+uuid.NewString()[:8], uniqueTestEmail("rpc-connections"), "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.CreateSpace(t.Context(), user.ID, "RPC Space")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.InstallUserApp(t.Context(), user.ID, "planner", "1.0.0", 1, []string{"connections.read"}); err != nil {
		t.Fatal(err)
	}
	token := "rpc-connections-app-token-" + uuid.NewString()
	if _, err := database.CreateAppRuntimeSession(t.Context(), user.ID, "planner", security.HashToken(token), space.ID, db.AppRuntimeSessionTTL); err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	dispatched := 0
	router.Get("/connections", func(w http.ResponseWriter, r *http.Request) { dispatched++; w.WriteHeader(http.StatusOK) })
	router.Post("/app-runtime/rpc", OfficialAppRPC(database, router, ""))
	request := map[string]any{"protocol": 2, "method": "connections.list"}
	response := performConversationRequest(t, router, http.MethodPost, "/app-runtime/rpc", token, request)
	if response.Code != http.StatusOK || dispatched != 1 {
		t.Fatalf("connection RPC = %d: %s", response.Code, response.Body.String())
	}
	request["params"] = map[string]any{"path": map[string]string{"spaceID": "another_space"}}
	response = performConversationRequest(t, router, http.MethodPost, "/app-runtime/rpc", token, request)
	if response.Code != http.StatusBadRequest || dispatched != 1 {
		t.Fatalf("cross-Space RPC = %d: %s", response.Code, response.Body.String())
	}
	delete(request, "params")
	if _, err := database.Conn.ExecContext(t.Context(), "DELETE FROM space_members WHERE space_id=$1 AND user_id=$2", space.ID, user.ID); err != nil {
		t.Fatal(err)
	}
	response = performConversationRequest(t, router, http.MethodPost, "/app-runtime/rpc", token, request)
	if response.Code != http.StatusForbidden || dispatched != 1 {
		t.Fatalf("revoked membership RPC = %d: %s", response.Code, response.Body.String())
	}
}
