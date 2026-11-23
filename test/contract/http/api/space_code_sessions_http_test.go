package api

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	. "github.com/misty-org/misty-server/internal/platform/httpapi"
)

const codeSessionTestCommit = "0123456789abcdef0123456789abcdef01234567"

func newCodeSessionTestRouter(spaces *SpacesService) *chi.Mux {
	router := chi.NewRouter()
	router.Get("/spaces/{spaceID}/code-sessions", spaces.SpaceCodeSessions())
	router.Post("/spaces/{spaceID}/code-sessions", spaces.SpaceCodeSessions())
	router.Get("/spaces/{spaceID}/code-sessions/{sessionID}", spaces.SpaceCodeSession())
	router.Post("/spaces/{spaceID}/code-sessions/{sessionID}/end", spaces.EndSpaceCodeSession())
	router.Post("/spaces/{spaceID}/code-sessions/{sessionID}/collaboration-ticket", spaces.SpaceCodeSessionCollaborationTicket())
	return router
}

func TestSpaceCodeSessionRoutes(t *testing.T) {
	database := openPresenceTestDatabase(t)
	ctx := context.Background()
	collab := testCollabConfig(t)
	spaces, err := NewSpacesService(database, base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
	if err != nil {
		t.Fatal(err)
	}
	spaces.SetJournalCollab(collab)
	router := newCodeSessionTestRouter(spaces)

	owner, err := database.CreateUser(uniqueTestEmail("code-owner"), "password123")
	if err != nil {
		t.Fatal(err)
	}
	member, err := database.CreateUser(uniqueTestEmail("code-member"), "password123")
	if err != nil {
		t.Fatal(err)
	}
	outsider, err := database.CreateUser(uniqueTestEmail("code-outsider"), "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.TestingCreateSpace(ctx, owner.ID, "Live code")
	if err != nil {
		t.Fatal(err)
	}
	otherSpace, err := database.TestingCreateSpace(ctx, owner.ID, "Elsewhere")
	if err != nil {
		t.Fatal(err)
	}
	invite, err := database.TestingInviteToSpace(ctx, owner.ID, space.ID, member.Email)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.RespondToSpaceInvite(ctx, member.ID, invite.ID, true); err != nil {
		t.Fatal(err)
	}
	ownerToken := newConversationTestBearerToken(t, database, owner.ID)
	memberToken := newConversationTestBearerToken(t, database, member.ID)
	outsiderToken := newConversationTestBearerToken(t, database, outsider.ID)
	base := "/spaces/" + space.ID + "/code-sessions"
	send := func(token, method, target string, body any, want int) map[string]any {
		t.Helper()
		return routeJSON(t, performConversationRequest(t, router, method, target, token, body), want)
	}
	repoKey := "github.com/misty-org/demo:" + codeSessionTestCommit
	start := map[string]any{
		"name": "Fix the login page", "repoKey": repoKey,
		"baseCommit": strings.ToUpper(codeSessionTestCommit), "branch": "misty/live/fix-login",
	}

	created := send(memberToken, http.MethodPost, base, start, http.StatusCreated)
	sessionID, _ := created["id"].(string)
	startedBy, _ := created["startedBy"].(map[string]any)
	if sessionID == "" || created["spaceId"] != space.ID || created["name"] != "Fix the login page" ||
		created["repoKey"] != repoKey || created["baseCommit"] != codeSessionTestCommit ||
		created["branch"] != "misty/live/fix-login" || created["createdAt"] == nil ||
		created["endedAt"] != nil || created["outcome"] != nil {
		t.Fatalf("created = %v", created)
	}
	if startedBy["id"] != member.ID || startedBy["name"] != member.Username {
		t.Fatalf("startedBy = %v, want %s (%s)", startedBy, member.ID, member.Username)
	}
	for _, field := range []string{"space_id", "repo_key", "base_commit", "started_by", "created_at", "acl_version", "ACLVersion"} {
		if _, ok := created[field]; ok {
			t.Fatalf("created session has %q: %v", field, created)
		}
	}

	for name, change := range map[string]map[string]any{
		"no name":           {"name": "  "},
		"long name":         {"name": strings.Repeat("n", 81)},
		"not a live branch": {"branch": "feature/fix-login"},
		"leading hyphen":    {"branch": "misty/live/-fix"},
		"uppercase branch":  {"branch": "misty/live/Fix"},
		"long branch":       {"branch": "misty/live/" + strings.Repeat("a", 64)},
		"short commit":      {"baseCommit": codeSessionTestCommit[:39]},
		"not hex":           {"baseCommit": strings.Repeat("z", 40)},
		"in-between length": {"baseCommit": codeSessionTestCommit + "0"},
		"long repo key":     {"repoKey": strings.Repeat("r", 513)},
		"no repo key":       {"repoKey": ""},
	} {
		body := map[string]any{}
		for key, value := range start {
			body[key] = value
		}
		for key, value := range change {
			body[key] = value
		}
		if got := performConversationRequest(t, router, http.MethodPost, base, memberToken, body); got.Code != http.StatusBadRequest {
			t.Fatalf("%s = %d %s", name, got.Code, got.Body.String())
		}
	}
	sha256Body := map[string]any{
		"name": "Long hashes", "repoKey": repoKey, "branch": "misty/live/sha256",
		"baseCommit": strings.Repeat("ab", 32),
	}
	sha256Session := send(ownerToken, http.MethodPost, base, sha256Body, http.StatusCreated)

	conflict := send(ownerToken, http.MethodPost, base, start, http.StatusConflict)
	if conflict["code"] != "branch_in_use" {
		t.Fatalf("conflict = %v", conflict)
	}
	otherRepo := map[string]any{}
	for key, value := range start {
		otherRepo[key] = value
	}
	otherRepo["repoKey"] = "github.com/misty-org/other:" + codeSessionTestCommit
	send(ownerToken, http.MethodPost, base, otherRepo, http.StatusCreated)

	if got := performConversationRequest(t, router, http.MethodGet, base, outsiderToken, nil); got.Code != http.StatusForbidden {
		t.Fatalf("outsider list = %d", got.Code)
	}
	if got := performConversationRequest(t, router, http.MethodPost, base, outsiderToken, start); got.Code != http.StatusForbidden {
		t.Fatalf("outsider create = %d", got.Code)
	}
	if got := performConversationRequest(t, router, http.MethodGet, base+"/"+sessionID, outsiderToken, nil); got.Code != http.StatusNotFound {
		t.Fatalf("outsider get = %d", got.Code)
	}
	if got := performConversationRequest(t, router, http.MethodPost, base+"/"+sessionID+"/collaboration-ticket", outsiderToken, nil); got.Code != http.StatusNotFound {
		t.Fatalf("outsider ticket = %d", got.Code)
	}
	if got := performConversationRequest(t, router, http.MethodGet, "/spaces/"+otherSpace.ID+"/code-sessions/"+sessionID, ownerToken, nil); got.Code != http.StatusNotFound {
		t.Fatalf("other Space get = %d", got.Code)
	}

	listed := send(ownerToken, http.MethodGet, base, nil, http.StatusOK)
	if !codeSessionListed(listed, sessionID) || !codeSessionListed(listed, sha256Session["id"].(string)) {
		t.Fatalf("list = %v", listed)
	}
	read := send(ownerToken, http.MethodGet, base+"/"+sessionID, nil, http.StatusOK)
	if read["id"] != sessionID || read["branch"] != "misty/live/fix-login" {
		t.Fatalf("read = %v", read)
	}

	ticket := send(ownerToken, http.MethodPost, base+"/"+sessionID+"/collaboration-ticket", nil, http.StatusCreated)
	if ticket["role"] != "editor" || ticket["ticket"] == "" || ticket["room"] != collab.CodeRoomID(sessionID) ||
		!strings.Contains(ticket["url"].(string), "/parties/code-room/") || ticket["expiresAt"] == nil {
		t.Fatalf("ticket = %v", ticket)
	}
	if _, ok := ticket["expires_at"]; ok {
		t.Fatalf("ticket uses snake case: %v", ticket)
	}
	claims := decodeTicketClaims(t, ticket["ticket"].(string))
	if claims.ResourceType != "code" || claims.ResourceID != sessionID || claims.CodeSessionID != sessionID ||
		claims.SpaceID != space.ID || claims.Subject != owner.ID || claims.ACLVersion < 1 {
		t.Fatalf("claims = %#v", claims)
	}

	if got := performConversationRequest(t, router, http.MethodPost, base+"/"+sessionID+"/end", ownerToken, map[string]any{"outcome": "kept"}); got.Code != http.StatusForbidden {
		t.Fatalf("owner ends a member's session = %d %s", got.Code, got.Body.String())
	}
	if got := performConversationRequest(t, router, http.MethodPost, base+"/"+sessionID+"/end", memberToken, map[string]any{"outcome": "shipped"}); got.Code != http.StatusBadRequest {
		t.Fatalf("unknown outcome = %d", got.Code)
	}
	if got := performConversationRequest(t, router, http.MethodPost, base+"/"+sessionID+"/end", outsiderToken, map[string]any{"outcome": "kept"}); got.Code != http.StatusNotFound {
		t.Fatalf("outsider end = %d", got.Code)
	}
	ended := send(memberToken, http.MethodPost, base+"/"+sessionID+"/end", map[string]any{"outcome": "merged"}, http.StatusOK)
	if ended["id"] != sessionID || ended["endedAt"] == nil || ended["outcome"] != "merged" {
		t.Fatalf("ended = %v", ended)
	}
	again := send(memberToken, http.MethodPost, base+"/"+sessionID+"/end", map[string]any{"outcome": "kept"}, http.StatusConflict)
	if again["code"] != "session_ended" {
		t.Fatalf("second end = %v", again)
	}

	if got := performConversationRequest(t, router, http.MethodPost, base+"/"+sessionID+"/collaboration-ticket", memberToken, nil); got.Code != http.StatusNotFound {
		t.Fatalf("ticket after end = %d %s", got.Code, got.Body.String())
	}
	afterEnd := send(ownerToken, http.MethodGet, base+"/"+sessionID, nil, http.StatusOK)
	if afterEnd["endedAt"] == nil || afterEnd["outcome"] != "merged" {
		t.Fatalf("read after end = %v", afterEnd)
	}
	if codeSessionListed(send(ownerToken, http.MethodGet, base, nil, http.StatusOK), sessionID) {
		t.Fatal("an ended session is still listed")
	}
	send(ownerToken, http.MethodPost, base, start, http.StatusCreated)

	originalClient := NoteControlHTTPClient
	t.Cleanup(func() { NoteControlHTTPClient = originalClient })
	purgedRooms := map[string]bool{}
	NoteControlHTTPClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		timestamp := request.Header.Get("X-Misty-Timestamp")
		if request.Header.Get("X-Misty-Signature") != collab.SignControlRequest(timestamp, body) {
			t.Fatalf("unsigned control command to %s", request.URL)
		}
		var envelope NoteControlEnvelope
		if err := json.Unmarshal(body, &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.Command == "purge" && envelope.ResourceID == sessionID &&
			strings.HasSuffix(request.URL.Path, "/parties/code-room/"+collab.CodeRoomID(sessionID)) {
			purgedRooms[sessionID] = true
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"ok":true,"purged":true}`)),
			Header:     make(http.Header),
		}, nil
	})}
	if _, err := spaces.ProcessCodeSessionControlCommands(ctx, 200); err != nil {
		t.Fatal(err)
	}
	if !purgedRooms[sessionID] {
		t.Fatalf("the ended session's room was not purged: %v", purgedRooms)
	}
	var delivered bool
	if err := database.SpaceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_code_session_control_outbox
			WHERE session_id=$1 AND command='purge' AND delivered_at IS NOT NULL)`, sessionID).Scan(&delivered)
	}); err != nil {
		t.Fatal(err)
	}
	if !delivered {
		t.Fatal("the purge command is still pending")
	}
}

func codeSessionListed(listed map[string]any, sessionID string) bool {
	sessions, _ := listed["sessions"].([]any)
	for _, item := range sessions {
		if session, ok := item.(map[string]any); ok && session["id"] == sessionID {
			return true
		}
	}
	return false
}
