package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/httpapi"

	"github.com/go-chi/chi/v5"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/kannachi323/misty/server/internal/platform/security"
)

// newConversationTestRouter mounts only the conversation endpoints this test
// exercises, matching the routes registered in mountSpacesRoutes.
func newConversationTestRouter(t *testing.T, spaces *SpacesService) *chi.Mux {
	t.Helper()
	router := chi.NewRouter()
	router.MethodFunc(http.MethodGet, "/spaces/{spaceID}/conversations", spaces.Conversations())
	router.MethodFunc(http.MethodPost, "/spaces/{spaceID}/conversations", spaces.Conversations())
	router.MethodFunc(http.MethodPatch, "/spaces/{spaceID}/conversations/{conversationID}", spaces.Conversation())
	router.MethodFunc(http.MethodGet, "/spaces/{spaceID}/conversations/{conversationID}/messages", spaces.ConversationMessages())
	return router
}

func newConversationTestBearerToken(t *testing.T, database *db.Database, userID string) string {
	t.Helper()
	token := uniqueTestEmail("token-" + userID)
	if err := database.CreateSession(security.HashToken(token), userID); err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	return token
}

func performConversationRequest(t *testing.T, router *chi.Mux, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var payload strings.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("json.Marshal() error = %v", err)
		}
		payload = *strings.NewReader(string(encoded))
	}
	req := httptest.NewRequest(method, path, &payload)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// TestConversationHTTPAccessControl guards, at the HTTP handler layer, the
// security property the user asked about explicitly: a user only ever sees
// conversations they own or are a member of. This complements the existing
// DB-layer coverage (TestSpaceGroupConversationsStayScopedToSelectedMembers,
// TestUpdateSpaceConversationRestrictedToCreator) with an end-to-end check
// that the HTTP handlers enforce the same rules through real requests.
func TestConversationHTTPAccessControl(t *testing.T) {
	database := openPresenceTestDatabase(t)
	ctx := context.Background()

	key := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
	spaces, err := NewSpacesService(database, nil, key)
	if err != nil {
		t.Fatalf("NewSpacesService() error = %v", err)
	}
	router := newConversationTestRouter(t, spaces)

	owner, err := database.CreateUser("Convo Owner", uniqueTestEmail("owner"), "password123")
	if err != nil {
		t.Fatalf("CreateUser(owner) error = %v", err)
	}
	member, err := database.CreateUser("Convo Member", uniqueTestEmail("member"), "password123")
	if err != nil {
		t.Fatalf("CreateUser(member) error = %v", err)
	}
	spacemate, err := database.CreateUser("Convo Spacemate", uniqueTestEmail("spacemate"), "password123")
	if err != nil {
		t.Fatalf("CreateUser(spacemate) error = %v", err)
	}
	outsider, err := database.CreateUser("Convo Outsider", uniqueTestEmail("outsider"), "password123")
	if err != nil {
		t.Fatalf("CreateUser(outsider) error = %v", err)
	}

	space, err := database.CreateSpace(ctx, owner.ID, "Conversations")
	if err != nil {
		t.Fatalf("CreateSpace(owner) error = %v", err)
	}

	// member and spacemate both join the Space, but only member is added to
	// the conversation below. outsider never joins the Space at all.
	for _, invited := range []*db.User{member, spacemate} {
		invite, inviteErr := database.InviteToSpace(ctx, owner.ID, space.ID, invited.Email)
		if inviteErr != nil {
			t.Fatalf("InviteToSpace(%s) error = %v", invited.Email, inviteErr)
		}
		if _, inviteErr = database.RespondToSpaceInvite(ctx, invited.ID, invite.ID, true); inviteErr != nil {
			t.Fatalf("RespondToSpaceInvite(%s) error = %v", invited.Email, inviteErr)
		}
	}

	conversation, err := database.CreateSpaceConversation(ctx, owner.ID, space.ID, "Launch crew", []db.SpaceActorRef{{Kind: "person", UserID: member.ID}})
	if err != nil {
		t.Fatalf("CreateSpaceConversation() error = %v", err)
	}

	ownerToken := newConversationTestBearerToken(t, database, owner.ID)
	memberToken := newConversationTestBearerToken(t, database, member.ID)
	spacemateToken := newConversationTestBearerToken(t, database, spacemate.ID)
	outsiderToken := newConversationTestBearerToken(t, database, outsider.ID)

	listPath := "/spaces/" + space.ID + "/conversations"
	messagesPath := "/spaces/" + space.ID + "/conversations/" + conversation.ID + "/messages"
	conversationPath := "/spaces/" + space.ID + "/conversations/" + conversation.ID

	// A member of the conversation sees it in their list.
	rec := performConversationRequest(t, router, http.MethodGet, listPath, memberToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET conversations (member) status = %d, body = %s", rec.Code, rec.Body)
	}
	var memberList struct {
		Conversations []map[string]any `json:"conversations"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &memberList); err != nil {
		t.Fatalf("unmarshal member conversations: %v", err)
	}
	if len(memberList.Conversations) != 1 || memberList.Conversations[0]["id"] != conversation.ID {
		t.Fatalf("member conversation list = %#v, want just %q", memberList.Conversations, conversation.ID)
	}

	// A fellow Space member who was never added to the conversation must not
	// see it in their list, even though they can otherwise read the Space.
	rec = performConversationRequest(t, router, http.MethodGet, listPath, spacemateToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET conversations (spacemate) status = %d, body = %s", rec.Code, rec.Body)
	}
	var spacemateList struct {
		Conversations []map[string]any `json:"conversations"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &spacemateList); err != nil {
		t.Fatalf("unmarshal spacemate conversations: %v", err)
	}
	if len(spacemateList.Conversations) != 0 {
		t.Fatalf("spacemate conversation list = %#v, want none", spacemateList.Conversations)
	}

	// That same non-member is rejected outright if they try to read the
	// conversation's messages directly by ID.
	rec = performConversationRequest(t, router, http.MethodGet, messagesPath, spacemateToken, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("GET messages (spacemate) status = %d, body = %s, want 403", rec.Code, rec.Body)
	}

	// A user outside the Space entirely is rejected too (space-level check
	// fires before the conversation-membership check even runs).
	rec = performConversationRequest(t, router, http.MethodGet, listPath, outsiderToken, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("GET conversations (outsider) status = %d, body = %s, want 403", rec.Code, rec.Body)
	}
	rec = performConversationRequest(t, router, http.MethodGet, messagesPath, outsiderToken, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("GET messages (outsider) status = %d, body = %s, want 403", rec.Code, rec.Body)
	}

	// Editing is restricted to the conversation's creator: neither the
	// non-member Space mate nor an actual conversation member (who didn't
	// create it) may rename it or change its membership.
	editBody := map[string]any{"title": "Hijacked", "participants": []db.SpaceActorRef{
		{Kind: "person", UserID: owner.ID},
		{Kind: "person", UserID: member.ID},
	}}
	rec = performConversationRequest(t, router, http.MethodPatch, conversationPath, spacemateToken, editBody)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("PATCH conversation (spacemate) status = %d, body = %s, want 403", rec.Code, rec.Body)
	}
	rec = performConversationRequest(t, router, http.MethodPatch, conversationPath, memberToken, editBody)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("PATCH conversation (non-creator member) status = %d, body = %s, want 403", rec.Code, rec.Body)
	}

	// The creator can rename the conversation and add the space mate; only
	// then does the space mate gain visibility into it.
	growBody := map[string]any{"title": "Launch crew v2", "participants": []db.SpaceActorRef{
		{Kind: "person", UserID: member.ID},
		{Kind: "person", UserID: spacemate.ID},
	}}
	rec = performConversationRequest(t, router, http.MethodPatch, conversationPath, ownerToken, growBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH conversation (owner) status = %d, body = %s", rec.Code, rec.Body)
	}

	rec = performConversationRequest(t, router, http.MethodGet, messagesPath, spacemateToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET messages (spacemate after being added) status = %d, body = %s", rec.Code, rec.Body)
	}
}
