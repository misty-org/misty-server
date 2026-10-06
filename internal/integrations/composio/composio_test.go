package composio

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func respond(status int, raw string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(raw)), Header: http.Header{}}
}

func testClient(t *testing.T, handler func(*http.Request) *http.Response) *Client {
	t.Helper()
	client, err := New(Config{Deployment: "cloud", APIKey: "test-key"})
	if err != nil {
		t.Fatal(err)
	}
	client.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "backend.composio.dev" || r.Header.Get("x-api-key") != "test-key" || !strings.HasPrefix(r.URL.Path, "/api/v3.1/") {
			t.Fatalf("request left the configured API: %s", r.URL)
		}
		return handler(r), nil
	})
	return client
}

func TestConfigRequiresCloudOptIn(t *testing.T) {
	for _, config := range []Config{{APIKey: "key"}, {Deployment: "self_hosted", APIKey: "key"}, {Deployment: "cloud"}, {Deployment: "cloud", APIKey: "bad key"}} {
		if config.Validate() == nil {
			t.Errorf("accepted %+v", config)
		}
	}
	if (Config{Deployment: "cloud", APIKey: "key"}).Validate() != nil {
		t.Fatal("cloud rejected")
	}
	if UserID("owner") != UserID("owner") || UserID("owner") == UserID("other") || !strings.HasPrefix(UserID("owner"), "misty_") {
		t.Fatal("user ID is not a stable pseudonym")
	}
}

func TestClassifyUsesTagsThenTheNamedAction(t *testing.T) {
	cases := map[string]struct {
		tags []string
		want Kind
	}{
		"GMAIL_FETCH_EMAILS":                      {[]string{"readOnlyHint"}, KindRead},
		"SLACK_LIST_POSTS":                        {[]string{"readOnlyHint"}, KindRead},
		"GOOGLEDRIVE_DELETE_FILE":                 {nil, KindDestructive},
		"GITHUB_CREATE_ISSUE":                     {[]string{"createHint"}, KindWrite},
		"GMAIL_SEND_EMAIL":                        {[]string{"createHint", "openWorldHint"}, KindConsequential},
		"GOOGLEDRIVE_ADD_FILE_SHARING_PREFERENCE": {[]string{"updateHint"}, KindConsequential},
		"GOOGLEDRIVE_CREATE_FILE_FROM_TEXT":       {[]string{"createHint"}, KindWrite},
		"NOTION_ARCHIVE_PAGE":                     {[]string{"destructiveHint"}, KindDestructive},
		"GOOGLECALENDAR_EVENTS_LIST":              {nil, KindWrite},
		"HUBSPOT_GET_CONTACT":                     {nil, KindRead},
	}
	for slug, test := range cases {
		if got := Classify(slug, test.tags); got != test.want {
			t.Errorf("%s = %s, want %s", slug, got, test.want)
		}
	}
	if !KindDestructive.NeedsApproval() || !KindConsequential.NeedsApproval() || KindWrite.NeedsApproval() || KindRead.NeedsApproval() {
		t.Fatal("approval classes")
	}
}

func TestSessionDisablesSandboxAndChatConnectionPrompts(t *testing.T) {
	client := testClient(t, func(r *http.Request) *http.Response {
		var body map[string]map[string]any
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		if r.URL.Path != "/api/v3.1/tool_router/session" || body["workbench"]["enable"] != false || body["manage_connections"]["enable"] != false || !strings.Contains(string(raw), UserID("owner")) {
			t.Fatalf("session request = %s %s", r.URL.Path, raw)
		}
		return respond(201, `{"session_id":"trs_abc123"}`)
	})
	if session, err := client.CreateSession(context.Background(), UserID("owner")); err != nil || session != "trs_abc123" {
		t.Fatalf("session = %q, %v", session, err)
	}
}

func TestExecuteSeparatesRejectionsFromUnknownOutcomes(t *testing.T) {
	status := 200
	client := testClient(t, func(r *http.Request) *http.Response {
		raw, _ := io.ReadAll(r.Body)
		if r.URL.Path != "/api/v3.1/tool_router/session/trs_1/execute" || !strings.Contains(string(raw), `"tool_slug":"GMAIL_SEND_EMAIL"`) || !strings.Contains(string(raw), `"account":"work"`) {
			t.Fatalf("execute request = %s %s", r.URL.Path, raw)
		}
		switch status {
		case 400:
			return respond(400, `{"error":{"message":"recipient is required","suggested_fix":"Pass to.","request_id":"req_1"}}`)
		case 502:
			return respond(502, `upstream`)
		}
		return respond(200, `{"data":{"id":"msg_1"},"error":null,"log_id":"log_1"}`)
	})
	result, err := client.Execute(context.Background(), "trs_1", "GMAIL_SEND_EMAIL", json.RawMessage(`{"to":"a@example.com"}`), "work")
	if err != nil || result.LogID != "log_1" || string(result.Data) != `{"id":"msg_1"}` {
		t.Fatalf("execute = %+v, %v", result, err)
	}
	status = 400
	_, err = client.Execute(context.Background(), "trs_1", "GMAIL_SEND_EMAIL", json.RawMessage(`{}`), "work")
	if !Rejected(err) || !strings.Contains(err.Error(), "recipient is required Pass to.") || !strings.Contains(err.Error(), "req_1") {
		t.Fatalf("validation error = %v", err)
	}
	status = 502
	if _, err = client.Execute(context.Background(), "trs_1", "GMAIL_SEND_EMAIL", json.RawMessage(`{}`), "work"); err == nil || Rejected(err) {
		t.Fatalf("a server error must not count as a rejection: %v", err)
	}
}

func TestTransportFailureIsUnknown(t *testing.T) {
	client, _ := New(Config{Deployment: "cloud", APIKey: "test-key"})
	client.http.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("connection reset") })
	if _, err := client.Execute(context.Background(), "trs_1", "GMAIL_SEND_EMAIL", json.RawMessage(`{}`), ""); !errors.Is(err, ErrTransport) || Rejected(err) {
		t.Fatalf("transport error = %v", err)
	}
}

func TestLinksStayOnComposio(t *testing.T) {
	link := "https://connect.composio.dev/link/lk_123"
	client := testClient(t, func(*http.Request) *http.Response {
		return respond(201, `{"redirect_url":"`+link+`","connected_account_id":"ca_1"}`)
	})
	if got, err := client.Link(context.Background(), "trs_1", "googledrive"); err != nil || got != link {
		t.Fatalf("link = %q, %v", got, err)
	}
	for _, unsafe := range []string{"http://connect.composio.dev/link/x", "https://composio.dev.evil.test/link", "https://user@connect.composio.dev/link", "https://connect.composio.dev:8443/link"} {
		if connectLinkAllowed(unsafe) {
			t.Errorf("accepted %s", unsafe)
		}
	}
}

func TestAccountsBelongToTheRequestingUser(t *testing.T) {
	owner, other := UserID("owner"), UserID("other")
	client := testClient(t, func(r *http.Request) *http.Response {
		if r.Method == http.MethodDelete {
			t.Fatal("deleted another user's account")
		}
		if strings.HasSuffix(r.URL.Path, "/connected_accounts/ca_other") {
			return respond(200, `{"id":"ca_other","user_id":"`+other+`","status":"ACTIVE"}`)
		}
		if r.URL.Query().Get("user_ids") != owner {
			t.Fatalf("listed for %q", r.URL.Query().Get("user_ids"))
		}
		return respond(200, `{"items":[{"id":"ca_1","user_id":"`+owner+`","status":"ACTIVE","toolkit":{"slug":"gmail"},"state":{"val":{"access_token":"secret"}}},{"id":"ca_2","user_id":"`+other+`","status":"ACTIVE","toolkit":{"slug":"gmail"}}],"next_cursor":null}`)
	})
	accounts, err := client.Accounts(context.Background(), owner, "")
	if err != nil || len(accounts) != 1 || accounts[0].ID != "ca_1" || !accounts[0].Active() {
		t.Fatalf("accounts = %+v, %v", accounts, err)
	}
	encoded, _ := json.Marshal(accounts)
	if strings.Contains(string(encoded), "secret") {
		t.Fatal("provider credentials were decoded")
	}
	if err := client.DeleteAccount(context.Background(), owner, "ca_other"); !NotFound(err) {
		t.Fatalf("delete other = %v", err)
	}
}

func TestIdentitiesNameEachAccount(t *testing.T) {
	executed := map[string]string{}
	var mu sync.Mutex
	client := testClient(t, func(r *http.Request) *http.Response {
		var body struct {
			Slug    string `json:"tool_slug"`
			Account string `json:"account"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		executed[body.Account] = body.Slug
		mu.Unlock()
		if body.Slug == "DISCORD_GET_MY_USER" {
			return respond(200, `{"data":{"username":"kannachi","email":"private@example.com"}}`)
		}
		return respond(200, `{"data":null,"error":"insufficient scopes"}`)
	})
	var accounts []Account
	_ = json.Unmarshal([]byte(`[
		{"id":"ca_mail","status":"ACTIVE","toolkit":{"slug":"gmail"},"state":{"val":{"displayName":"me@example.com","access_token":"secret"}}},
		{"id":"ca_chat","status":"ACTIVE","toolkit":{"slug":"discord"}},
		{"id":"ca_cal","status":"ACTIVE","toolkit":{"slug":"googlecalendar"}},
		{"id":"ca_off","status":"EXPIRED","toolkit":{"slug":"discord"}},
		{"id":"ca_doc","status":"ACTIVE","toolkit":{"slug":"notion"}}
	]`), &accounts)
	names := client.Identities(context.Background(), "session", accounts)
	if len(names) != 2 || names["ca_mail"] != "me@example.com" || names["ca_chat"] != "kannachi" {
		t.Fatalf("names = %v", names)
	}
	if len(executed) != 2 || executed["ca_chat"] != "DISCORD_GET_MY_USER" || executed["ca_cal"] != "GOOGLECALENDAR_EVENTS_LIST" {
		t.Fatalf("executed = %v", executed)
	}
	if _, cached := identityCache.Load("ca_cal"); cached {
		t.Fatal("a failed lookup was cached")
	}
}
