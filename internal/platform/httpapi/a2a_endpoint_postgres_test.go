package api

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/kannachi323/misty/server/internal/platform/security"
	"github.com/kannachi323/misty/server/test/testkit"
)

func TestA2AEndpointPostgres(t *testing.T) {
	if os.Getenv("TEST_DB_NAME") != "misty_agent_members_test" {
		t.Skip("requires isolated agent members test database")
	}
	restorePoll := a2aStreamPoll
	a2aStreamPoll = 200 * time.Millisecond
	t.Cleanup(func() { a2aStreamPoll = restorePoll })
	database := testkit.OpenDatabase(t)
	ctx := t.Context()
	alice, _ := database.CreateUser("Alice", "a2a-alice@example.test", "password123")
	bob, _ := database.CreateUser("Bob", "a2a-bob@example.test", "password123")
	outsider, _ := database.CreateUser("Outsider", "a2a-outsider@example.test", "password123")
	space, err := database.TestingCreateSpace(ctx, alice.ID, "Launch team")
	if err != nil {
		t.Fatal(err)
	}
	if err := database.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO space_members(space_id,user_id,role) VALUES($1,$2,'member')`, space.ID, bob.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	researcher, _ := database.SavePersonalAgent(ctx, bob.ID, "", db.AgentProfileInput{Name: "Researcher", Enabled: true})
	assistant, _ := database.SavePersonalAgent(ctx, alice.ID, "", db.AgentProfileInput{Name: "Assistant", Enabled: true})
	planner, _ := database.SavePersonalAgent(ctx, alice.ID, "", db.AgentProfileInput{Name: "Planner", Enabled: true})
	outsiderAgent, _ := database.SavePersonalAgent(ctx, outsider.ID, "", db.AgentProfileInput{Name: "Stranger", Enabled: true})
	if _, err := database.SaveSpaceAgentListing(ctx, bob.ID, space.ID, researcher.ID, db.SpaceAgentListingInput{Description: "Answers research questions"}); err != nil {
		t.Fatal(err)
	}
	service := &SpacesService{database: database}
	router := chi.NewRouter()
	router.Post("/api/a2a/spaces/{spaceID}/agents/{agentID}", service.A2AAgent())
	router.Get("/api/a2a/spaces/{spaceID}/agents/{agentID}/.well-known/agent-card.json", service.A2AAgentCard())
	router.Post("/api/a2a/agents/{agentID}/token", service.A2AAgentToken())
	router.Get("/api/a2a/push", service.A2APushInbox())
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	signer, err := security.SessionSignerFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/a2a/spaces/" + space.ID + "/agents/" + researcher.ID
	request := func(user, session, agentToken, method, target string, body []byte) *http.Request {
		t.Helper()
		cookie, _ := signer.Mint(user, session, "access", time.Now().Add(time.Hour))
		r, _ := http.NewRequestWithContext(ctx, method, server.URL+target, bytes.NewReader(body))
		r.AddCookie(&http.Cookie{Name: TestingSessionCookieName, Value: cookie})
		if agentToken != "" {
			r.Header.Set(a2aAgentTokenHeader, agentToken)
		}
		return r
	}
	mint := func(user, agentID string) (string, int) {
		t.Helper()
		response, err := http.DefaultClient.Do(request(user, "a2a-session-"+user, "", http.MethodPost, "/api/a2a/agents/"+agentID+"/token", nil))
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var out struct {
			Token string `json:"token"`
		}
		_ = json.NewDecoder(response.Body).Decode(&out)
		return out.Token, response.StatusCode
	}
	rpc := func(method string, params any) []byte {
		raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
		return raw
	}
	send := func(user, agentToken, method string, params any) (map[string]any, int) {
		t.Helper()
		response, err := http.DefaultClient.Do(request(user, "a2a-session-"+user, agentToken, http.MethodPost, path, rpc(method, params)))
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(response.Body).Decode(&out)
		return out, response.StatusCode
	}
	errorCode := func(response map[string]any) float64 {
		t.Helper()
		failure, ok := response["error"].(map[string]any)
		if !ok {
			t.Fatalf("expected an error, got %v", response)
		}
		return failure["code"].(float64)
	}
	taskState := func(response map[string]any) (string, string) {
		t.Helper()
		result, ok := response["result"].(map[string]any)
		if !ok {
			t.Fatalf("no task in %v", response)
		}
		return result["id"].(string), result["status"].(map[string]any)["state"].(string)
	}
	message := func(id, text string) map[string]any {
		return map[string]any{"message": map[string]any{"role": "user", "kind": "message", "messageId": id,
			"parts": []map[string]any{{"kind": "text", "text": text}}}}
	}

	// Agent tokens: only for your own enabled agents, and required on every call.
	if _, status := mint(alice.ID, researcher.ID); status != http.StatusNotFound {
		t.Fatalf("minting a token for another member's agent = %d", status)
	}
	aliceToken, status := mint(alice.ID, assistant.ID)
	if status != http.StatusOK || aliceToken == "" {
		t.Fatalf("mint = %d", status)
	}
	plannerToken, _ := mint(alice.ID, planner.ID)
	strangerToken, _ := mint(outsider.ID, outsiderAgent.ID)
	if _, status := send(alice.ID, "", "message/send", message("m-0", "hi")); status != http.StatusUnauthorized {
		t.Fatalf("call without an agent token = %d", status)
	}
	if _, status := send(bob.ID, aliceToken, "message/send", message("m-0", "hi")); status != http.StatusUnauthorized {
		t.Fatalf("another member replayed Alice's agent token: %d", status)
	}
	stolen, _ := http.DefaultClient.Do(request(alice.ID, "another-session", aliceToken, http.MethodPost, path, rpc("message/send", message("m-0", "hi"))))
	stolen.Body.Close()
	if stolen.StatusCode != http.StatusUnauthorized {
		t.Fatalf("agent token from another session = %d", stolen.StatusCode)
	}

	// Send, replay, approve, and the task is scoped to the agent that sent it.
	first, _ := send(alice.ID, aliceToken, "message/send", message("m-1", "What is the launch date?"))
	id, state := taskState(first)
	if state != "auth-required" {
		t.Fatalf("new request state = %s, want auth-required while Bob decides", state)
	}
	if replay, _ := send(alice.ID, aliceToken, "message/send", message("m-1", "What is the launch date?")); replay["result"].(map[string]any)["id"] != id {
		t.Fatalf("repeated messageId created a second task: %v", replay)
	}
	if response, _ := send(alice.ID, plannerToken, "tasks/get", map[string]any{"id": id}); errorCode(response) != a2aTaskNotFound {
		t.Fatalf("Alice's other agent read the task: %v", response)
	}
	stored, err := database.A2AAgentRequest(ctx, alice.ID, assistant.ID, id)
	if err != nil || stored.RequesterAgentID != assistant.ID {
		t.Fatalf("request did not record the calling agent: %v %v", stored, err)
	}

	// Push notifications go only to the Misty inbox.
	for _, target := range []string{"https://hooks.example.test/a2a", "http://127.0.0.1:9/a2a/push", "/a2a/push?x=1", "/api/a2a/push#frag"} {
		response, _ := send(alice.ID, aliceToken, "tasks/pushNotificationConfig/set", map[string]any{"taskId": id,
			"pushNotificationConfig": map[string]any{"url": target}})
		if errorCode(response) != a2aPushNotSupported {
			t.Fatalf("push to %s = %v", target, response)
		}
	}
	setPush, _ := send(alice.ID, aliceToken, "tasks/pushNotificationConfig/set", map[string]any{"taskId": id,
		"pushNotificationConfig": map[string]any{"id": "inbox-1", "url": "/api/a2a/push", "token": "match-me"}})
	if setPush["result"] == nil {
		t.Fatalf("push config set = %v", setPush)
	}
	if list, _ := send(alice.ID, aliceToken, "tasks/pushNotificationConfig/list", map[string]any{"id": id}); len(list["result"].([]any)) != 1 {
		t.Fatalf("push config list = %v", list)
	}
	if got, _ := send(alice.ID, aliceToken, "tasks/pushNotificationConfig/get", map[string]any{"id": id, "pushNotificationConfigId": "inbox-1"}); got["result"].(map[string]any)["taskId"] != id {
		t.Fatalf("push config get = %v", got)
	}
	if response, _ := send(alice.ID, plannerToken, "tasks/pushNotificationConfig/list", map[string]any{"id": id}); errorCode(response) != a2aTaskNotFound {
		t.Fatalf("another agent listed the push configs: %v", response)
	}

	// The inbox delivers the current state once, then each change.
	inboxContext, closeInbox := context.WithCancel(ctx)
	inboxRequest := request(alice.ID, "a2a-session-"+alice.ID, aliceToken, http.MethodGet, "/api/a2a/push", nil).WithContext(inboxContext)
	inbox, err := http.DefaultClient.Do(inboxRequest)
	if err != nil || inbox.StatusCode != http.StatusOK {
		t.Fatalf("inbox = %v %v", inbox, err)
	}
	pushes := sseEvents(t, inbox.Body)
	readPush := func(want string) {
		t.Helper()
		select {
		case event := <-pushes:
			var push struct {
				ConfigID string         `json:"pushNotificationConfigId"`
				Token    string         `json:"token"`
				Task     map[string]any `json:"task"`
			}
			if err := json.Unmarshal([]byte(event), &push); err != nil {
				t.Fatalf("push %q: %v", event, err)
			}
			if push.ConfigID != "inbox-1" || push.Token != "match-me" || push.Task["id"] != id {
				t.Fatalf("push = %+v", push)
			}
			if got := push.Task["status"].(map[string]any)["state"]; got != want {
				t.Fatalf("pushed state = %v, want %s", got, want)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("no push for %s", want)
		}
	}
	readPush("auth-required")
	if _, err := database.DecideAgentMemberRequest(ctx, bob.ID, id, true); err != nil {
		t.Fatal(err)
	}
	readPush("submitted")
	closeInbox()
	inbox.Body.Close()

	// A stream follows a new task through approval to its reply.
	streamContext, closeStream := context.WithTimeout(ctx, 20*time.Second)
	defer closeStream()
	stream, err := http.DefaultClient.Do(request(alice.ID, "a2a-session-"+alice.ID, aliceToken, http.MethodPost, path,
		rpc("message/stream", message("m-2", "Summarize the launch plan"))).WithContext(streamContext))
	if err != nil || stream.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("stream = %v %v", stream, err)
	}
	updates := sseEvents(t, stream.Body)
	nextUpdate := func() map[string]any {
		t.Helper()
		select {
		case event := <-updates:
			var out map[string]any
			if err := json.Unmarshal([]byte(event), &out); err != nil {
				t.Fatalf("stream event %q: %v", event, err)
			}
			return out["result"].(map[string]any)
		case <-time.After(10 * time.Second):
			t.Fatal("stream stalled")
		}
		return nil
	}
	opened := nextUpdate()
	streamed := opened["id"].(string)
	if opened["kind"] != "task" || opened["status"].(map[string]any)["state"] != "auth-required" {
		t.Fatalf("stream opened with %v", opened)
	}
	if _, err := database.DecideAgentMemberRequest(ctx, bob.ID, streamed, true); err != nil {
		t.Fatal(err)
	}
	if update := nextUpdate(); update["kind"] != "status-update" || update["final"] != false || update["status"].(map[string]any)["state"] != "submitted" {
		t.Fatalf("approval update = %v", update)
	}
	if err := database.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE space_runs SET state='completed',result='{"text":"Launch is Friday."}'::jsonb,completed_at=NOW()
			WHERE id=(SELECT child_run_id FROM agent_member_requests WHERE id=$1)`, streamed)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if update := nextUpdate(); update["kind"] != "artifact-update" || !strings.Contains(string(mustJSONForTest(update)), "Launch is Friday.") {
		t.Fatalf("reply update = %v", update)
	}
	if update := nextUpdate(); update["kind"] != "status-update" || update["final"] != true || update["status"].(map[string]any)["state"] != "completed" {
		t.Fatalf("final update = %v", update)
	}
	if _, open := <-updates; open {
		t.Fatal("stream stayed open after the final update")
	}
	stream.Body.Close()

	// Resubscribing to a finished task returns it and ends.
	resubscribe, err := http.DefaultClient.Do(request(alice.ID, "a2a-session-"+alice.ID, aliceToken, http.MethodPost, path,
		rpc("tasks/resubscribe", map[string]any{"id": streamed})))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resubscribe.Body)
	resubscribe.Body.Close()
	if strings.Count(string(body), "data: ") != 1 || !strings.Contains(string(body), `"state":"completed"`) {
		t.Fatalf("resubscribe = %s", body)
	}

	// Cancel, delete push config, and outsiders.
	if canceled, _ := send(alice.ID, aliceToken, "tasks/cancel", map[string]any{"id": id}); canceled["result"].(map[string]any)["status"].(map[string]any)["state"] != "canceled" {
		t.Fatalf("cancel = %v", canceled)
	}
	if response, _ := send(alice.ID, aliceToken, "tasks/cancel", map[string]any{"id": id}); errorCode(response) != a2aTaskNotCancelable {
		t.Fatalf("canceling twice = %v", response)
	}
	if response, _ := send(alice.ID, aliceToken, "tasks/pushNotificationConfig/delete", map[string]any{"id": id, "pushNotificationConfigId": "inbox-1"}); response["error"] != nil {
		t.Fatalf("push config delete = %v", response)
	}
	if response, _ := send(alice.ID, aliceToken, "tasks/unknown", map[string]any{"id": id}); errorCode(response) != a2aMethodNotFound {
		t.Fatalf("unknown method = %v", response)
	}
	if response, _ := send(outsider.ID, strangerToken, "message/send", message("m-9", "hi")); response["error"] == nil {
		t.Fatalf("a non-member reached the agent: %v", response)
	}

	// Disabling the agent stops its tokens at once.
	if err := database.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE misty_ask_identities SET enabled=false WHERE id=$1`, planner.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, status := send(alice.ID, plannerToken, "tasks/get", map[string]any{"id": id}); status != http.StatusUnauthorized {
		t.Fatalf("disabled agent's token = %d", status)
	}

	card, err := http.DefaultClient.Do(request(alice.ID, "a2a-session-"+alice.ID, aliceToken, http.MethodGet, path+"/.well-known/agent-card.json", nil))
	if err != nil {
		t.Fatal(err)
	}
	var described map[string]any
	_ = json.NewDecoder(card.Body).Decode(&described)
	card.Body.Close()
	if described["name"] != "Researcher" || described["capabilities"].(map[string]any)["streaming"] != true ||
		described["capabilities"].(map[string]any)["pushNotifications"] != true {
		t.Fatalf("agent card = %d %v", card.StatusCode, described)
	}
}

// sseEvents yields each event's data until the stream ends.
func sseEvents(t *testing.T, body io.Reader) <-chan string {
	t.Helper()
	events := make(chan string, 16)
	go func() {
		defer close(events)
		scanner := bufio.NewScanner(body)
		scanner.Buffer(make([]byte, 64<<10), 1<<20)
		for scanner.Scan() {
			if data, ok := strings.CutPrefix(scanner.Text(), "data: "); ok {
				events <- data
			}
		}
	}()
	return events
}

func mustJSONForTest(value any) []byte {
	raw, _ := json.Marshal(value)
	return raw
}
