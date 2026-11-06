package api

import (
	"context"
	"encoding/base64"
)

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	. "github.com/misty-org/misty-server/internal/platform/httpapi"

	"github.com/go-chi/chi/v5"
	"github.com/misty-org/misty-server/internal/platform/db"
	"github.com/misty-org/misty-server/internal/platform/security"
)

func newConversationTestBearerToken(t *testing.T, database *db.Database, userID string) string {
	t.Helper()
	signer, err := security.SessionSignerFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	token, err := signer.Mint(userID, uniqueTestEmail("session"), "access", time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
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
	if signer, err := security.SessionSignerFromEnv(); err == nil {
		if _, err := signer.Verify(token, "access"); err == nil {
			req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: token})
		} else {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	} else {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// newConversationTestRouter mounts only the conversation endpoints this test
// exercises, matching the routes registered in mountSpacesRoutes.
func newConversationTestRouter(t *testing.T, spaces *SpacesService) *chi.Mux {
	t.Helper()
	router := chi.NewRouter()
	router.MethodFunc(http.MethodGet, "/spaces/{spaceID}/conversations", spaces.Conversations())
	router.MethodFunc(http.MethodPost, "/spaces/{spaceID}/conversations", spaces.Conversations())
	router.MethodFunc(http.MethodPost, "/spaces/{spaceID}/conversations/dm", spaces.DirectConversation())
	router.MethodFunc(http.MethodPatch, "/spaces/{spaceID}/conversations/{conversationID}", spaces.Conversation())
	router.MethodFunc(http.MethodGet, "/spaces/{spaceID}/conversations/{conversationID}/messages", spaces.ConversationMessages())
	return router
}

// TestConversationHTTPAccessControl guards, at the HTTP handler layer, who sees
// what in a Space's chat: a DM only its participants, not even the Space's owner;
// a group chat everyone in the Space. It complements the DB-layer coverage
// (TestUpdateSpaceConversationRestrictedToCreator) with real requests.
func TestConversationHTTPAccessControl(t *testing.T) {
	database := openPresenceTestDatabase(t)
	ctx := context.Background()

	key := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
	spaces, err := NewSpacesService(database, nil, key)
	if err != nil {
		t.Fatalf("NewSpacesService() error = %v", err)
	}
	router := newConversationTestRouter(t, spaces)

	owner, err := database.CreateUser(uniqueTestEmail("owner"), "password123")
	if err != nil {
		t.Fatalf("CreateUser(owner) error = %v", err)
	}
	member, err := database.CreateUser(uniqueTestEmail("member"), "password123")
	if err != nil {
		t.Fatalf("CreateUser(member) error = %v", err)
	}
	spacemate, err := database.CreateUser(uniqueTestEmail("spacemate"), "password123")
	if err != nil {
		t.Fatalf("CreateUser(spacemate) error = %v", err)
	}
	outsider, err := database.CreateUser(uniqueTestEmail("outsider"), "password123")
	if err != nil {
		t.Fatalf("CreateUser(outsider) error = %v", err)
	}

	space, err := database.TestingCreateSpace(ctx, owner.ID, "Conversations")
	if err != nil {
		t.Fatalf("CreateSpace(owner) error = %v", err)
	}

	// member and spacemate both join the Space, and only member is in the DM
	// below. outsider never joins the Space at all.
	for _, invited := range []*db.User{member, spacemate} {
		invite, inviteErr := database.TestingInviteToSpace(ctx, owner.ID, space.ID, invited.Email)
		if inviteErr != nil {
			t.Fatalf("InviteToSpace(%s) error = %v", invited.Email, inviteErr)
		}
		if _, inviteErr = database.RespondToSpaceInvite(ctx, invited.ID, invite.ID, true); inviteErr != nil {
			t.Fatalf("RespondToSpaceInvite(%s) error = %v", invited.Email, inviteErr)
		}
	}

	ownerToken := newConversationTestBearerToken(t, database, owner.ID)
	memberToken := newConversationTestBearerToken(t, database, member.ID)
	spacemateToken := newConversationTestBearerToken(t, database, spacemate.ID)
	outsiderToken := newConversationTestBearerToken(t, database, outsider.ID)

	listPath := "/spaces/" + space.ID + "/conversations"

	// Opening a DM makes the real kind, and opening it again finds the same one.
	rec := performConversationRequest(t, router, http.MethodPost, listPath+"/dm", memberToken, map[string]any{"user_id": spacemate.ID})
	if rec.Code != http.StatusOK {
		t.Fatalf("POST direct status = %d, body = %s", rec.Code, rec.Body)
	}
	var conversation db.SpaceConversation
	if err := json.Unmarshal(rec.Body.Bytes(), &conversation); err != nil || conversation.Kind != "dm" || conversation.VisibleToSpace {
		t.Fatalf("direct conversation = %#v, %v", conversation, err)
	}
	rec = performConversationRequest(t, router, http.MethodPost, listPath+"/dm", spacemateToken, map[string]any{"user_id": member.ID})
	var again db.SpaceConversation
	if err := json.Unmarshal(rec.Body.Bytes(), &again); err != nil || again.ID != conversation.ID {
		t.Fatalf("the other person's direct conversation = %#v, %v", again, err)
	}

	messagesPath := "/spaces/" + space.ID + "/conversations/" + conversation.ID + "/messages"
	conversationPath := "/spaces/" + space.ID + "/conversations/" + conversation.ID

	// A participant sees it in their list.
	rec = performConversationRequest(t, router, http.MethodGet, listPath, memberToken, nil)
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

	// The Space's owner isn't in it, so doesn't see it in their list, even though
	// they can otherwise read the whole Space.
	rec = performConversationRequest(t, router, http.MethodGet, listPath, ownerToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET conversations (owner) status = %d, body = %s", rec.Code, rec.Body)
	}
	var ownerList struct {
		Conversations []map[string]any `json:"conversations"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &ownerList); err != nil {
		t.Fatalf("unmarshal owner conversations: %v", err)
	}
	if len(ownerList.Conversations) != 0 {
		t.Fatalf("owner conversation list = %#v, want none", ownerList.Conversations)
	}

	// The owner is rejected outright if they try to read it directly by ID.
	rec = performConversationRequest(t, router, http.MethodGet, messagesPath, ownerToken, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("GET messages (owner) status = %d, body = %s, want 403", rec.Code, rec.Body)
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

	// Nobody can change a DM, or reach into it by renaming it, the Space's owner included.
	editBody := map[string]any{"title": "Hijacked"}
	for _, token := range []string{ownerToken, memberToken} {
		rec = performConversationRequest(t, router, http.MethodPatch, conversationPath, token, editBody)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("PATCH DM status = %d, body = %s, want 403", rec.Code, rec.Body)
		}
	}

	// A group chat is public to the Space: everyone in it sees it without being
	// named, and someone who joins later does too. Outsiders still don't.
	rec = performConversationRequest(t, router, http.MethodPost, listPath, memberToken, map[string]any{"title": "Launch crew"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST group chat status = %d, body = %s", rec.Code, rec.Body)
	}
	var group db.SpaceConversation
	if err := json.Unmarshal(rec.Body.Bytes(), &group); err != nil || group.Kind != "standard" || !group.VisibleToSpace || len(group.Participants) != 3 {
		t.Fatalf("group chat = %#v, %v", group, err)
	}
	groupMessages := "/spaces/" + space.ID + "/conversations/" + group.ID + "/messages"
	for _, token := range []string{ownerToken, memberToken, spacemateToken} {
		rec = performConversationRequest(t, router, http.MethodGet, groupMessages, token, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET group chat messages status = %d, body = %s", rec.Code, rec.Body)
		}
	}
	rec = performConversationRequest(t, router, http.MethodGet, groupMessages, outsiderToken, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("GET group chat messages (outsider) status = %d, want 403", rec.Code)
	}
	invite, err := database.TestingInviteToSpace(ctx, owner.ID, space.ID, outsider.Email)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.RespondToSpaceInvite(ctx, outsider.ID, invite.ID, true); err != nil {
		t.Fatal(err)
	}
	rec = performConversationRequest(t, router, http.MethodGet, groupMessages, outsiderToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET group chat messages (after joining) status = %d, body = %s", rec.Code, rec.Body)
	}
	joined, err := database.SpaceConversations(ctx, outsider.ID, space.ID)
	if err != nil || len(joined) != 1 || joined[0].ID != group.ID {
		t.Fatalf("someone who joined later sees %#v, %v, want just the group chat", joined, err)
	}

	// Only the Space's owner renames a group chat.
	rec = performConversationRequest(t, router, http.MethodPatch, "/spaces/"+space.ID+"/conversations/"+group.ID, memberToken, editBody)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("PATCH group chat (member) status = %d, body = %s, want 403", rec.Code, rec.Body)
	}
	rec = performConversationRequest(t, router, http.MethodPatch, "/spaces/"+space.ID+"/conversations/"+group.ID, ownerToken, map[string]any{"title": "Launch crew v2"})
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH group chat (owner) status = %d, body = %s", rec.Code, rec.Body)
	}
}
