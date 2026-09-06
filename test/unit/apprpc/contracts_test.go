package apprpc

import (
	"context"
	"encoding/json"
	. "github.com/kannachi323/misty/server/internal/apprpc"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestMethodResolutionBindsSpaceAndRejectsURLAndMethodInjection(t *testing.T) {
	input := Request{Protocol: 2, Method: "notes.get", Params: Params{Path: map[string]string{"noteID": "note_1"}}}
	target, err := Resolve(input, "space_1")
	if err != nil || target.Verb != "GET" || target.Path != "/spaces/space_1/notes/note_1" {
		t.Fatalf("resolve: %#v, %v", target, err)
	}
	for name, change := range map[string]func(*Request){
		"unknown method":        func(r *Request) { r.Method = "account.delete" },
		"unknown protocol":      func(r *Request) { r.Protocol = 999 },
		"different space":       func(r *Request) { r.Params.Path["spaceID"] = "space_2" },
		"traversal":             func(r *Request) { r.Params.Path["noteID"] = "../secrets" },
		"encoded traversal":     func(r *Request) { r.Params.Path["noteID"] = "%2e%2e%2fsecrets" },
		"unknown path argument": func(r *Request) { r.Params.Path["url"] = "https://evil.invalid" },
		"read body":             func(r *Request) { r.Params.Body = json.RawMessage(`{"write":true}`) },
	} {
		t.Run(name, func(t *testing.T) {
			r := Request{Protocol: 2, Method: "notes.get", Params: Params{Path: map[string]string{"noteID": "note_1"}}}
			change(&r)
			if _, err := Resolve(r, "space_1"); err == nil {
				t.Fatal("invalid call resolved")
			}
		})
	}
}

func TestEnvelopeRejectsUnknownFieldsAndTrailingRequests(t *testing.T) {
	for _, input := range []string{
		`{"protocol":2,"method":"notes.list","url":"/me"}`,
		`{"protocol":2,"method":"notes.list","params":{"verb":"DELETE"}}`,
		`{"protocol":2,"method":"notes.list"} {"protocol":2,"method":"notes.delete"}`,
	} {
		if _, err := Decode(strings.NewReader(input)); err == nil {
			t.Errorf("accepted %s", input)
		}
	}
}

func TestQueriesRemainDataAndCannotChangeRouting(t *testing.T) {
	input := Request{Protocol: 2, Method: "tasks.list", Params: Params{Query: map[string]json.RawMessage{
		"q": json.RawMessage(`"a&spaceID=space_2"`), "limit": json.RawMessage(`10`), "include_archived": json.RawMessage(`false`),
	}}}
	result, err := Resolve(input, "space_1")
	if err != nil {
		t.Fatal(err)
	}
	if result.Path != "/spaces/space_1/tasks" || !strings.Contains(result.Query, "q=a%26spaceID%3Dspace_2") {
		t.Fatalf("unexpected target: %#v", result)
	}
}

func TestDispatchPreservesDomainChecksBodyCancellationAndResponse(t *testing.T) {
	router := chi.NewRouter()
	router.Post("/v1/spaces/{spaceID}/notes", func(w http.ResponseWriter, r *http.Request) {
		if chi.URLParam(r, "spaceID") != "space_1" {
			t.Fatal("lost bound Space")
		}
		if r.Header.Get("Authorization") != "Bearer app-session" || r.Header.Get("Cookie") != "" {
			t.Fatal("incorrect credential forwarding")
		}
		if r.Context().Value(testContextKey{}) != "kept" {
			t.Fatal("lost request context")
		}
		var body map[string]string
		if json.NewDecoder(r.Body).Decode(&body) != nil || body["title"] != "SDK note" {
			t.Fatal("lost body")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"note_1"}`))
	})
	checked := false
	handler := Handler{Prefix: "/v1", Dispatch: router,
		Authenticate: func(_ http.ResponseWriter, _ *http.Request) (Identity, bool) {
			return Identity{AppID: "journal", AccountID: "account_1", SpaceID: "space_1"}, true
		},
		Authorize: func(identity Identity, verb, path string) bool {
			checked = true
			return identity.AccountID == "account_1" && verb == "POST" && path == "/spaces/space_1/notes"
		},
	}
	request := httptest.NewRequest("POST", "/v1/app-runtime/rpc", strings.NewReader(`{"protocol":2,"method":"notes.create","params":{"body":{"title":"SDK note"}}}`))
	request = request.WithContext(context.WithValue(request.Context(), testContextKey{}, "kept"))
	request.Header.Set("Authorization", "Bearer app-session")
	request.Header.Set("Cookie", "misty_session=must-not-forward")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if !checked || response.Code != http.StatusCreated || response.Body.String() != `{"id":"note_1"}` {
		t.Fatalf("dispatch: %d %s", response.Code, response.Body.String())
	}
}

type testContextKey struct{}

func TestDeniedMethodNeverReachesDomainHandler(t *testing.T) {
	dispatched := false
	handler := Handler{Prefix: "/v1", Dispatch: http.HandlerFunc(func(http.ResponseWriter, *http.Request) { dispatched = true }),
		Authenticate: func(http.ResponseWriter, *http.Request) (Identity, bool) { return Identity{SpaceID: "space_1"}, true },
		Authorize:    func(Identity, string, string) bool { return false }}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("POST", "/v1/app-runtime/rpc", strings.NewReader(`{"protocol":2,"method":"notes.list"}`)))
	if dispatched || response.Code != http.StatusForbidden {
		t.Fatalf("denial: %d, dispatched=%v", response.Code, dispatched)
	}
}
