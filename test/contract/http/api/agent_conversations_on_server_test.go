package api

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/misty-org/misty-server/internal/accounts"
	"github.com/misty-org/misty-server/internal/platform/db"
	. "github.com/misty-org/misty-server/internal/platform/httpapi"
)

// Agent conversations live on the server, per person per Space, and are always
// private to their owner (docs/agents/README.md, Where conversations live).

type onServerFixture struct {
	database *db.Database
	service  *AIService
	spaces   *SpacesService
	router   *chi.Mux
	owner    *db.User
	member   *db.User
	space    *db.Space
	agentID  string
}

func newOnServerFixture(t *testing.T) *onServerFixture {
	t.Helper()
	database := openPresenceTestDatabase(t)
	ctx := context.Background()
	key := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
	spaces, err := NewSpacesService(database, nil, key)
	if err != nil {
		t.Fatal(err)
	}
	service := NewAIService(database, nil)
	owner, err := database.CreateUser(uniqueTestEmail("o"), "password123")
	if err != nil {
		t.Fatal(err)
	}
	member, err := database.CreateUser(uniqueTestEmail("m"), "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.TestingCreateSpace(ctx, member.ID, "Shared work")
	if err != nil {
		t.Fatal(err)
	}
	invite, err := database.TestingInviteToSpace(ctx, member.ID, space.ID, owner.Email)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.RespondToSpaceInvite(ctx, owner.ID, invite.ID, true); err != nil {
		t.Fatal(err)
	}
	identity, err := database.EnsureAskIdentity(ctx, owner.ID, "test-model")
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	router.Get("/misty/conversations", service.MistyConversations())
	router.Post("/misty/conversations", service.MistyConversations())
	router.Post("/misty/conversations/{conversationID}/turns", service.MistyConversationTurns())
	router.Post("/misty/conversations/{conversationID}/ledger", service.MistyConversationLedger())
	router.Delete("/misty/conversations/{conversationID}", service.MistyConversation())
	router.Patch("/misty/conversations/{conversationID}", service.MistyConversation())
	router.Get("/invocations/{invocationID}/progress", service.InvocationProgress())
	router.Get("/invocations/{invocationID}/tool-output", service.InvocationToolOutput())
	router.Get("/misty/lessons", service.MistyLessons())
	router.Put("/misty/lessons/{kind}/{id}", service.MistyLesson())
	return &onServerFixture{database: database, service: service, spaces: spaces, router: router, owner: owner, member: member, space: space, agentID: identity.ID}
}

func (f *onServerFixture) request(t *testing.T, method, path, userID string, body any) *httptest.ResponseRecorder {
	t.Helper()
	return performConversationRequest(t, f.router, method, path, newConversationTestBearerToken(t, f.database, userID), body)
}

func (f *onServerFixture) conversations(t *testing.T, userID string) []map[string]any {
	t.Helper()
	rec := f.request(t, http.MethodGet, "/misty/conversations", userID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET conversations status = %d, body = %s", rec.Code, rec.Body)
	}
	var list struct {
		Conversations []map[string]any `json:"conversations"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	return list.Conversations
}

func (f *onServerFixture) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	if err := f.database.WithRLSContext(context.Background(), db.ServiceRLSSettings(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(context.Background(), query, args...)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func (f *onServerFixture) scalar(t *testing.T, query string, args ...any) string {
	t.Helper()
	var value sql.NullString
	if err := f.database.WithRLSContext(context.Background(), db.ServiceRLSSettings(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(context.Background(), query, args...).Scan(&value)
	}); err != nil {
		t.Fatal(err)
	}
	return value.String
}

// finishedRun stores one finished turn in a conversation and returns its id.
func (f *onServerFixture) finishedRun(t *testing.T, conversationID, reply string) *db.AIInvocationRecord {
	t.Helper()
	ctx := context.Background()
	id := "invocation_" + uuid.NewString()
	stored, _, err := f.database.CreateAIInvocationRecord(ctx, db.AIInvocationRecord{
		ID: id, UserID: f.owner.ID, ConversationID: conversationID, SurfaceID: "global", Mode: "drawer", Trigger: "message", State: "running",
		IdempotencyKey: id, RequestPayload: json.RawMessage(`{"prompt":"what does the page say"}`), ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.CommitAIInvocationEvent(ctx, f.owner.ID, stored.ID, "reply", "assistant.message", MustAPIRawJSON(map[string]any{"type": "assistant.message", "text": reply}), "completed"); err != nil {
		t.Fatal(err)
	}
	return &stored
}

func TestAgentConversationIsPrivateToItsOwnerInASharedSpace(t *testing.T) {
	f := newOnServerFixture(t)
	ctx := context.Background()
	// The window's Space decides where a new conversation goes.
	rec := f.request(t, http.MethodPost, "/misty/conversations", f.owner.ID, map[string]any{"title": "Budget", "space_id": f.space.ID})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST conversation status = %d, body = %s", rec.Code, rec.Body)
	}
	var created struct {
		ID      string `json:"id"`
		SpaceID string `json:"spaceId"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil || created.SpaceID != f.space.ID {
		t.Fatalf("created = %#v, %v", created, err)
	}
	run := f.finishedRun(t, created.ID, "Secret budget figures")
	if run.SpaceID != f.space.ID {
		t.Fatalf("run Space = %q, want %q", run.SpaceID, f.space.ID)
	}

	// Another person's Space is refused.
	outsider, err := f.database.TestingCreateSpace(ctx, f.member.ID, "Theirs")
	if err != nil {
		t.Fatal(err)
	}
	if rec := f.request(t, http.MethodPost, "/misty/conversations", f.owner.ID, map[string]any{"title": "Nope", "space_id": outsider.ID}); rec.Code == http.StatusCreated {
		t.Fatal("a conversation went into a Space its owner isn't in")
	}

	// The owner reads it, with its tool-free reply, from any signed-in session.
	mine := f.conversations(t, f.owner.ID)
	if len(mine) != 1 || mine[0]["id"] != created.ID || !strings.Contains(string(mustJSON(t, mine[0]["messages"])), "Secret budget figures") {
		t.Fatalf("owner's conversations = %#v", mine)
	}

	// The Space's owner, who is the other member here, reads nothing of it, by any path.
	if theirs := f.conversations(t, f.member.ID); len(theirs) != 0 {
		t.Fatalf("Space owner sees = %#v", theirs)
	}
	if rec := f.request(t, http.MethodGet, "/invocations/"+run.ID+"/progress", f.member.ID, nil); rec.Code == http.StatusOK {
		t.Fatalf("Space owner read the run's timeline: %s", rec.Body)
	}
	if rec := f.request(t, http.MethodGet, "/invocations/"+run.ID+"/tool-output", f.member.ID, nil); strings.Contains(rec.Body.String(), "Secret") || strings.Contains(rec.Body.String(), run.ID) {
		t.Fatalf("Space owner read the run's tool output: %s", rec.Body)
	}
	if rec := f.request(t, http.MethodPatch, "/misty/conversations/"+created.ID, f.member.ID, map[string]any{"title": "Mine now"}); rec.Code == http.StatusOK {
		t.Fatalf("Space owner renamed it: %s", rec.Body)
	}
	if rec := f.request(t, http.MethodDelete, "/misty/conversations/"+created.ID, f.member.ID, nil); rec.Code == http.StatusNoContent {
		if len(f.conversations(t, f.owner.ID)) != 1 {
			t.Fatal("Space owner deleted it")
		}
	}
	if _, _, err := f.database.AIInvocationEvents(ctx, f.member.ID, run.ID, 0); err == nil {
		t.Fatal("Space owner read the run's events")
	}
	// Row security says the same without the code's owner predicates.
	if got := rowsVisibleTo(t, f.database, f.member.ID, created.ID, run.ID); got != 0 {
		t.Fatalf("row security showed the Space owner %d rows", got)
	}
	if got := rowsVisibleTo(t, f.database, f.owner.ID, created.ID, run.ID); got < 3 {
		t.Fatalf("row security showed the conversation's owner %d rows", got)
	}
}

// rowsVisibleTo counts a conversation's rows under an unprivileged role, so the
// policies alone decide. The role lives only in this rolled-back transaction.
func rowsVisibleTo(t *testing.T, database *db.Database, userID, conversationID, invocationID string) int {
	t.Helper()
	ctx := context.Background()
	tx, err := database.Conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, statement := range []string{
		`CREATE ROLE misty_rls_probe NOLOGIN`,
		`GRANT USAGE ON SCHEMA public TO misty_rls_probe`,
		`GRANT SELECT ON misty_ask_conversations, ai_invocations, ai_invocation_events, misty_ask_conversation_events, ai_conversation_attachments TO misty_rls_probe`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	for key, value := range accounts.UserScope(userID) {
		if _, err := tx.ExecContext(ctx, `SELECT set_config($1,$2,true)`, key, value); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.ExecContext(ctx, `SET LOCAL ROLE misty_rls_probe`); err != nil {
		t.Fatal(err)
	}
	var visible int
	if err := tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM misty_ask_conversations WHERE id=$1)+(SELECT count(*) FROM ai_invocations WHERE id=$2)+(SELECT count(*) FROM ai_invocation_events WHERE invocation_id=$2)+(SELECT count(*) FROM misty_ask_conversation_events WHERE conversation_id=$1)+(SELECT count(*) FROM ai_conversation_attachments WHERE conversation_id=$1)`, conversationID, invocationID).Scan(&visible); err != nil {
		t.Fatal(err)
	}
	return visible
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func TestRedactedToolResultIsNotStoredReadable(t *testing.T) {
	f := newOnServerFixture(t)
	ctx := context.Background()
	conversationID, err := f.database.CreatePersonalAgentConversation(ctx, f.owner.ID, f.space.ID, f.agentID)
	if err != nil {
		t.Fatal(err)
	}
	run := f.finishedRun(t, conversationID, "Done")
	const secret = "the-page-said-this-in-private"
	for _, call := range []struct {
		id, name, result string
	}{
		// An app's result, which is only ever kept sealed.
		{"call-app", "integration.execute", `{"data":"` + secret + `"}`},
		// A result a tool marked redacted itself.
		{"call-marked", "browser.inspect", `{"redacted":true,"text":"` + secret + `"}`},
	} {
		if err := f.spaces.TestingStoreAIToolOutput(ctx, run, call.id, call.name, json.RawMessage(call.result)); err != nil {
			t.Fatal(err)
		}
	}
	// An ordinary result is stored and readable, with credential-named fields removed.
	if err := f.spaces.TestingStoreAIToolOutput(ctx, run, "call-page", "browser.inspect", json.RawMessage(`{"text":"page text","api_key":"sk-live-123","nested":{"session_token":"abc"}}`)); err != nil {
		t.Fatal(err)
	}
	stored := f.scalar(t, `SELECT string_agg(data::text,' ') FROM misty_ask_conversation_events WHERE conversation_id=$1`, conversationID)
	if strings.Contains(stored, secret) || strings.Contains(stored, "sk-live-123") || strings.Contains(stored, `"abc"`) {
		t.Fatalf("stored readable: %s", stored)
	}
	rec := f.request(t, http.MethodGet, "/invocations/"+run.ID+"/tool-output", f.owner.ID, nil)
	var outputs struct {
		Items []db.AIToolOutput `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &outputs); err != nil || len(outputs.Items) != 3 {
		t.Fatalf("outputs = %s, %v", rec.Body, err)
	}
	if !outputs.Items[0].Sealed || !outputs.Items[1].Sealed || outputs.Items[0].Output != nil {
		t.Fatalf("redacted results aren't sealed: %#v", outputs.Items)
	}
	if outputs.Items[2].Sealed || !strings.Contains(string(outputs.Items[2].Output), "page text") {
		t.Fatalf("an ordinary result wasn't kept: %#v", outputs.Items[2])
	}
	// A screenshot goes to the object store, never inline, and is named in the output.
	store, err := NewLocalLibraryObjectStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f.spaces.SetToolOutputStore(store)
	png := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII="
	if err := f.spaces.TestingStoreAIToolOutput(ctx, run, "call-shot", "browser.visual", json.RawMessage(`{"image":{"dataUrl":"data:image/png;base64,`+png+`"}}`)); err != nil {
		t.Fatal(err)
	}
	shot := f.scalar(t, `SELECT data::text FROM misty_ask_conversation_events WHERE conversation_id=$1 AND data->>'tool_call_id'='call-shot'`, conversationID)
	if strings.Contains(shot, png) || !strings.Contains(shot, "aiatt_") {
		t.Fatalf("screenshot stored = %s", shot)
	}
	key := f.scalar(t, `SELECT object_key FROM ai_conversation_attachments WHERE conversation_id=$1 AND scope='tool_output'`, conversationID)
	reader, _, err := store.Open(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(reader)
	reader.Close()
	if len(data) == 0 {
		t.Fatal("screenshot object is empty")
	}
	// It stays out of what the model and the transcript treat as the person's attachments.
	listed, err := f.database.AIConversationAttachmentsForInvocation(ctx, f.owner.ID, run.ID, true)
	if err != nil || len(listed) != 0 {
		t.Fatalf("attachments for the run = %#v, %v", listed, err)
	}
}

func TestConversationRetentionDeletesServerHistory(t *testing.T) {
	f := newOnServerFixture(t)
	ctx := context.Background()
	if _, err := f.database.UpdateAISettings(ctx, f.owner.ID, true, 7, true, true); err != nil {
		t.Fatal(err)
	}
	expired, err := f.database.CreatePersonalAgentConversation(ctx, f.owner.ID, "", f.agentID)
	if err != nil {
		t.Fatal(err)
	}
	kept, err := f.database.CreatePersonalAgentConversation(ctx, f.owner.ID, "", f.agentID)
	if err != nil {
		t.Fatal(err)
	}
	run := f.finishedRun(t, expired, "Old reply")
	keptRun := f.finishedRun(t, kept, "Recent reply")
	if err := f.spaces.TestingStoreAIToolOutput(ctx, run, "call-1", "web.fetch", json.RawMessage(`{"text":"old page"}`)); err != nil {
		t.Fatal(err)
	}
	f.exec(t, `UPDATE misty_ask_conversations SET updated_at=NOW()-INTERVAL '8 days' WHERE id=$1`, expired)
	f.exec(t, `UPDATE ai_invocations SET created_at=NOW()-INTERVAL '8 days',expires_at=NOW()-INTERVAL '7 days' WHERE id=$1`, run.ID)
	// A finished run past its 24-hour expiry stays while retention lasts.
	f.exec(t, `UPDATE ai_invocations SET expires_at=NOW()-INTERVAL '1 hour' WHERE id=$1`, keptRun.ID)
	if _, err := f.database.PurgeExpiredAITransients(ctx, 1000); err != nil {
		t.Fatal(err)
	}
	if f.scalar(t, `SELECT id FROM ai_invocations WHERE id=$1`, keptRun.ID) != keptRun.ID {
		t.Fatal("a turn inside retention was deleted at its expiry")
	}
	purged := int64(0)
	for {
		n, _, err := f.database.PurgeExpiredAgentConversations(ctx, 1000)
		if err != nil {
			t.Fatal(err)
		}
		purged += n
		if n == 0 {
			break
		}
	}
	if purged == 0 {
		t.Fatal("nothing purged")
	}
	if got := f.scalar(t, `SELECT string_agg(id,',') FROM misty_ask_conversations WHERE id=ANY(ARRAY[$1,$2])`, expired, kept); got != kept {
		t.Fatalf("conversations left = %q, want only %q", got, kept)
	}
	if got := f.scalar(t, `SELECT count(*)::text FROM ai_invocations WHERE id=$1`, run.ID); got != "0" {
		t.Fatal("an expired conversation's turn is still stored")
	}
	if got := f.scalar(t, `SELECT count(*)::text FROM misty_ask_conversation_events WHERE conversation_id=$1`, expired); got != "0" {
		t.Fatal("an expired conversation's tool output is still stored")
	}
}
